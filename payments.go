// payments.go — single-user payment gate and balance queries.
//
// Auth model:
//   - /v1/topup, /v1/keys, /v1/keys/rotate: X-Payer header must
//     equal the locked owner address (requireOwnerPayer).
//   - Everything else under /v1/: Authorization: Bearer <key>
//     validated against the wallet's hashed key store.
//   - /payments/balance: open (read-only) for the owner.
//
// On the hot path, every successful upstream call debits the balance
// by the cost derived from the upstream `usage` field (via
// usageScanner + pricingCost). When the balance hits 0, the next
// request is rejected with 402 Payment Required.
package agw

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// servePaymentsBalance handles GET /payments/balance.
//
// Open to anyone (the address is the identifier; no private data
// leaked). In a public deployment, rate-limit by IP.
func (p *Proxy) servePaymentsBalance(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap, err := p.Wallet.Snapshot()
	if err != nil {
		http.Error(w, "balance unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

// bearerAuth validates the Authorization: Bearer <key> header and
// returns true on success. On failure it writes a 401 response and
// returns false. The plaintext is also stashed on the request
// context so downstream hooks (session tagging, charge) can
// identify which key was used.
//
// Additionally, the owner's balance is checked: if it's <= 0, the
// request is rejected with 402 Payment Required.
func (p *Proxy) bearerAuth(w http.ResponseWriter, r *http.Request) bool {
	if p.Wallet == nil {
		// legacy mode: no wallet → free for all
		return true
	}
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		writeAuthError(w, "missing or malformed Authorization: Bearer <key>")
		return false
	}
	plaintext := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
	addr, err := p.Wallet.LookupKey(plaintext)
	if err != nil {
		writeAuthError(w, "invalid or revoked API key")
		return false
	}
	if addr != p.Wallet.Addr() {
		writeAuthError(w, "key does not belong to this gateway")
		return false
	}
	// Balance check
	bal, err := p.Wallet.Balance()
	if err != nil {
		writeAuthError(w, "balance lookup failed")
		return false
	}
	if bal <= 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "X-PAYMENT-REQUIRED",
			"reason":  "balance exhausted — top up via POST /v1/topup",
			"balance": bal,
			"topup":   "POST /v1/topup with X-Payer=<owner> and {\"amount\": \"<micro-units>\"}",
		})
		return false
	}
	// Stash the key hash + prefix on the request context for session
	// tagging. The hash is the SHA-256 of the plaintext (same as
	// what the wallet stores) so /payments/keys can roll it up.
	hash := hashKey(plaintext)
	r.Header.Set("X-AGW-Key-Hash", hash)
	if len(plaintext) >= 8 {
		r.Header.Set("X-AGW-Key-Prefix", plaintext[:8])
	}
	// Propagate to the tracked session, if any.
	if session := trackedSessionFromContext(r.Context()); session != nil {
		session.setKey(hash, plaintext[:min(8, len(plaintext))])
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// writeAuthError renders a uniform 401 response.
func writeAuthError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="agw"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  "unauthorized",
		"reason": reason,
	})
}

// keyStatsRow is the per-key rollup returned by /payments/keys.
type keyStatsRow struct {
	Hash           string `json:"hash"`
	Prefix         string `json:"prefix"`
	Active         bool   `json:"active"`
	IssuedAt       string `json:"issuedAt"`
	RequestCount   int64  `json:"requestCount"`
	SuccessCount   int64  `json:"successCount"`
	ErrorCount     int64  `json:"errorCount"`
	TotalCostUSDC  float64 `json:"totalCostUSDC"`
	TotalTokensIn  int64  `json:"totalTokensIn"`
	TotalTokensOut int64  `json:"totalTokensOut"`
	LastUsed       string `json:"lastUsed,omitempty"`
}

// servePaymentsKeys returns per-key usage rollups, merging live
// stats (in-memory) with the canonical key list from the wallet.
//
// Live stats: each in-memory sessionRequest has APIKeyHash; we
// aggregate over a window (default = all time, also "1h", "24h", "7d").
//
// Wallet list: keys that have never been used still appear (with
// requestCount=0) so operators can see "we minted 5 keys but only
// 3 are active."
func (p *Proxy) servePaymentsKeys(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	cutoff := statsCutoff(window)

	// Aggregate live stats by key hash.
	type agg struct {
		hash      string
		prefix    string
		requests  int64
		success   int64
		err       int64
		cost      float64
		tokensIn  int64
		tokensOut int64
		lastUsed  time.Time
	}
	live := make(map[string]*agg)
	if p.Sessions != nil {
		p.Sessions.mu.Lock()
		entries := make([]statsEntry, 0, len(p.Sessions.history))
		for _, e := range p.Sessions.history {
			if !cutoff.IsZero() && e.started.Before(cutoff) {
				continue
			}
			entries = append(entries, *e)
		}
		p.Sessions.mu.Unlock()
		for _, e := range entries {
			if e.apiKeyHash == "" {
				continue
			}
			a, ok := live[e.apiKeyHash]
			if !ok {
				a = &agg{hash: e.apiKeyHash, prefix: e.apiKeyPrefix}
				live[e.apiKeyHash] = a
			}
			a.requests++
			if e.isError {
				a.err++
			} else {
				a.success++
			}
			a.cost += e.cost
			a.tokensIn += e.tokens.InputTokens
			a.tokensOut += e.tokens.OutputTokens
			if e.completed.After(a.lastUsed) {
				a.lastUsed = e.completed
			}
		}
	}

	// Pull the canonical key list from the wallet (so unused keys
	// also show up).
	walletKeys := p.Wallet.ListKeys()
	rows := make([]keyStatsRow, 0, len(walletKeys))
	for _, k := range walletKeys {
		row := keyStatsRow{
			Hash:     k.Hash,
			Prefix:   k.Prefix,
			Active:   k.Active,
			IssuedAt: k.IssuedAt,
		}
		if a, ok := live[k.Hash]; ok {
			row.RequestCount = a.requests
			row.SuccessCount = a.success
			row.ErrorCount = a.err
			row.TotalCostUSDC = a.cost
			row.TotalTokensIn = a.tokensIn
			row.TotalTokensOut = a.tokensOut
			if !a.lastUsed.IsZero() {
				row.LastUsed = a.lastUsed.UTC().Format(time.RFC3339)
			}
		}
		rows = append(rows, row)
	}
	// Sort: active first, then by request count desc, then prefix asc.
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			ri, rj := rows[i], rows[j]
			swap := false
			switch {
			case ri.Active != rj.Active:
				swap = !ri.Active
			case ri.RequestCount != rj.RequestCount:
				swap = ri.RequestCount < rj.RequestCount
			case ri.Prefix > rj.Prefix:
				swap = true
			}
			if swap {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"owner":  p.Wallet.Addr(),
		"window": window,
		"cutoff": formatCutoff(cutoff),
		"count":  len(rows),
		"items":  rows,
	})
}

func formatCutoff(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
