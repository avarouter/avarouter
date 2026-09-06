// payments.go — multi-tenant payment gate, balance queries, stats.
//
// Auth model (v3, multi-tenant):
//   - /v1/topup, /v1/keys, /v1/keys/rotate, /v1/keys/{prefix} (DELETE):
//     X-Payer header is the user's `from` address (a registered or
//     soon-to-be-registered user).
//   - /v1/keys also requires X-Payer; on first call to /v1/keys
//     before any topup, the user is auto-registered (defensive).
//   - Hot path under /v1/: Authorization: Bearer <key>; the owning
//     user's balance must be > 0 or the request is rejected with
//     402 Payment Required.
//   - /payments/balance: own balance (Bearer or X-Payer). Optional
//     ?user=0x... to look up another user (no auth for MVP).
//   - /payments/users: list all users + their balances (no auth).
//   - /payments/keys: per-key stats. ?user=0x... filters to one
//     user. ?window=1h|24h|7d|30d|all.
//
// On the hot path, every successful upstream call debits the OWNER
// of the API key used (not a global "owner") by the cost derived
// from the upstream `usage` field.
package agw

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// servePaymentsBalance handles GET /payments/balance.
//
// Behavior:
//   - Bearer auth: returns the key-owner's balance.
//   - X-Payer header: returns that user's balance (must be valid addr).
//   - ?user=0x... query: returns that user's balance (admin view, no
//     auth required in MVP).
//   - Default (no auth, no query): returns aggregate of all users.
func (p *Proxy) servePaymentsBalance(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	// (a) ?user=0x... — admin lookup
	if q := r.URL.Query().Get("user"); q != "" {
		snap, _ := p.Wallet.Snapshot(q)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from":        snap.From,
			"payTo":       snap.PayTo,
			"balance":     snap.Balance,
			"balanceUSDC": float64(snap.Balance) / 1_000_000.0,
			"created":     snap.Created,
			"updated":     snap.Updated,
			"registered":  snap.Active,
		})
		return
	}

	// (b) X-Payer header — the caller's own balance
	if xp := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer"))); xp != "" {
		snap, _ := p.Wallet.Snapshot(xp)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from":        snap.From,
			"payTo":       snap.PayTo,
			"balance":     snap.Balance,
			"balanceUSDC": float64(snap.Balance) / 1_000_000.0,
			"created":     snap.Created,
			"updated":     snap.Updated,
			"registered":  snap.Active,
		})
		return
	}

	// (c) Bearer — the key-owner's balance
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		plaintext := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
		if userAddr, err := p.Wallet.LookupKey(plaintext); err == nil {
			snap, _ := p.Wallet.Snapshot(userAddr)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"from":        snap.From,
				"payTo":       snap.PayTo,
				"balance":     snap.Balance,
				"balanceUSDC": float64(snap.Balance) / 1_000_000.0,
				"created":     snap.Created,
				"updated":     snap.Updated,
				"registered":  snap.Active,
			})
			return
		}
	}

	// (c2) X-AGW-Session — the session owner's balance
	if s := r.Header.Get("X-AGW-Session"); s != "" {
		ss := p.Wallet.Sessions()
		if ss != nil {
			if rec, err := ss.Lookup(s); err == nil {
				userAddr := strings.ToLower(rec.User)
				snap, _ := p.Wallet.Snapshot(userAddr)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"from":        snap.From,
					"payTo":       snap.PayTo,
					"balance":     snap.Balance,
					"balanceUSDC": float64(snap.Balance) / 1_000_000.0,
					"created":     snap.Created,
					"updated":     snap.Updated,
					"registered":  snap.Active,
				})
				return
			}
		}
	}

	// (d) No auth — aggregate
	users := p.Wallet.ListUsers()
	var total int64
	for _, u := range users {
		total += u.Balance
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"userCount":   len(users),
		"total":       total,
		"totalUSDC":   float64(total) / 1_000_000.0,
		"network":     p.USDC.CAIP2(),
		"asset":       p.USDC.Address.Hex(),
	})
}

// servePaymentsUsers handles GET /payments/users — list all users
// with their balances and per-user request/cost aggregates.
func (p *Proxy) servePaymentsUsers(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	window := r.URL.Query().Get("window")
	cutoff := statsCutoff(window)

	users := p.Wallet.ListUsers()
	rows := make([]userStatsRow, 0, len(users))
	for _, u := range users {
		row := userStatsRow{
			From:    u.From,
			PayTo:   u.PayTo,
			Balance: u.Balance,
			Created: u.Created,
			Updated: u.Updated,
			Active:  u.Active,
		}
		// Aggregate per-user usage stats from the in-memory hub.
		if p.Sessions != nil {
			p.Sessions.mu.Lock()
			for _, e := range p.Sessions.history {
				if !cutoff.IsZero() && e.started.Before(cutoff) {
					continue
				}
				if e.userAddr == "" || e.userAddr != u.From {
					continue
				}
				row.RequestCount++
				if e.isError {
					row.ErrorCount++
				} else {
					row.SuccessCount++
				}
				row.TotalCostUSDC += e.cost
				row.TotalTokensIn += e.tokens.InputTokens
				row.TotalTokensOut += e.tokens.OutputTokens
				if e.completed.After(row.LastUsed) {
					row.LastUsed = e.completed
				}
			}
			p.Sessions.mu.Unlock()
		}
		if !row.LastUsed.IsZero() {
			row.LastUsedStr = row.LastUsed.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	// Sort: most recently active first, then by balance desc.
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			ri, rj := rows[i], rows[j]
			swap := false
			switch {
			case !ri.LastUsed.Equal(rj.LastUsed):
				swap = ri.LastUsed.Before(rj.LastUsed)
			case ri.Balance != rj.Balance:
				swap = ri.Balance < rj.Balance
			case ri.From > rj.From:
				swap = true
			}
			if swap {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"window": window,
		"cutoff": formatCutoff(cutoff),
		"count":  len(rows),
		"items":  rows,
	})
}

// userStatsRow is the per-user rollup returned by /payments/users.
type userStatsRow struct {
	From           string  `json:"from"`
	PayTo          string  `json:"payTo"`
	Balance        int64   `json:"balance"`
	BalanceUSDC    float64 `json:"balanceUSDC"`
	Created        string  `json:"created"`
	Updated        string  `json:"updated"`
	Active         bool    `json:"active"`
	RequestCount   int64   `json:"requestCount"`
	SuccessCount   int64   `json:"successCount"`
	ErrorCount     int64   `json:"errorCount"`
	TotalCostUSDC  float64 `json:"totalCostUSDC"`
	TotalTokensIn  int64   `json:"totalTokensIn"`
	TotalTokensOut int64   `json:"totalTokensOut"`
	LastUsed       time.Time `json:"-"`
	LastUsedStr    string  `json:"lastUsed,omitempty"`
}

// bearerAuth validates the Authorization: Bearer <key> header and
// returns true on success. On failure it writes a 401 response and
// returns false. The plaintext is also stashed on the request
// context so downstream hooks (session tagging, charge) can
// identify which key was used.
//
// Additionally, the user's balance is checked: if it's <= 0, the
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
	userAddr, err := p.Wallet.LookupKey(plaintext)
	if err != nil {
		writeAuthError(w, "invalid or revoked API key")
		return false
	}
	// Balance check
	bal, err := p.Wallet.Balance(userAddr)
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
			"user":    userAddr,
			"topup":   "POST /v1/topup with X-Payer=<your-address> and {\"amount\": \"<micro-units>\"}",
		})
		return false
	}
	// Stash the key hash + prefix + user on the request context.
	hash := hashKey(plaintext)
	r.Header.Set("X-AGW-Key-Hash", hash)
	r.Header.Set("X-AGW-User-Addr", userAddr)
	if len(plaintext) >= 8 {
		r.Header.Set("X-AGW-Key-Prefix", plaintext[:8])
	}
	if session := trackedSessionFromContext(r.Context()); session != nil {
		session.setKey(hash, plaintext[:min(8, len(plaintext))], userAddr)
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
	Owner          string `json:"owner"`
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

// servePaymentsKeys returns per-key usage rollups, with optional
// filtering by user.
//
//   - ?user=0x... : only keys for that user
//   - ?window=1h|24h|7d|30d|all : time window for live stats
//
// The wallet's canonical key list is the source of truth (so unused
// keys still appear); the in-memory hub provides the request counts
// and cost.
func (p *Proxy) servePaymentsKeys(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	window := r.URL.Query().Get("window")
	cutoff := statsCutoff(window)

	ownerFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("user")))

	// Aggregate live stats by key hash, optionally filtered to a user.
	type agg struct {
		hash      string
		prefix    string
		owner     string
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
			if ownerFilter != "" && e.userAddr != ownerFilter {
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
				a = &agg{hash: e.apiKeyHash, prefix: e.apiKeyPrefix, owner: e.userAddr}
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

	// Pull the canonical key list from the wallet.
	walletKeys := p.Wallet.ListKeys(ownerFilter)
	rows := make([]keyStatsRow, 0, len(walletKeys))
	for _, k := range walletKeys {
		row := keyStatsRow{
			Hash:     k.Hash,
			Prefix:   k.Prefix,
			Owner:    k.Owner,
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
		"owner":  ownerFilter,
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
