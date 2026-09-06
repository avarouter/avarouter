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
// returns false. When accepted, the owner address is stashed on the
// context for downstream use.
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
	return true
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
