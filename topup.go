// topup.go — POST /v1/topup is the single x402-protected endpoint.
//
// Multi-tenant design:
//   - X-Payer is the user's `from` address. Any valid address is
//     accepted (open registration).
//   - On the first call (no payment yet), the 402 challenge uses
//     the user's existing payTo if they're already registered, or
//     the `payTo` field from the request body (defaulting to the
//     `from` address itself).
//   - When the client submits the signed payment, the server
//     auto-registers the user with payTo = auth.To (or keeps the
//     existing one if it matches), verifies the EIP-3009 signature,
//     optionally settles on-chain, and credits the user's balance.
package agw

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// TopupRequest is the client-side body for POST /v1/topup.
type TopupRequest struct {
	Amount  string       `json:"amount"`
	PayTo   string       `json:"payTo,omitempty"` // optional: only used on first 402 to specify deposit addr
	Payment *PaymentAuth `json:"payment,omitempty"`
}

// TopupResponse is the success body after a topup completes.
type TopupResponse struct {
	OK       bool   `json:"ok"`
	Balance  string `json:"balance"`
	TxHash   string `json:"txHash,omitempty"`
	Note     string `json:"note,omitempty"`
	User     string `json:"user,omitempty"`
	PayTo    string `json:"payTo,omitempty"`
}

func (p *Proxy) serveTopup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if p.Wallet == nil || p.USDC == nil {
		http.Error(w, "payments not enabled on this gateway", http.StatusServiceUnavailable)
		return
	}

	payer := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if payer == "" {
		http.Error(w, "X-Payer header required (your from address)", http.StatusBadRequest)
		return
	}
	if _, err := normalizeAddr(payer); err != nil {
		http.Error(w, "invalid X-Payer: "+err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	r.Body.Close()
	var req TopupRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.Amount == "" {
		http.Error(w, `missing "amount" (USDC micro-units)`, http.StatusBadRequest)
		return
	}
	amount, ok := parsePositiveInt(req.Amount)
	if !ok {
		http.Error(w, `invalid "amount"`, http.StatusBadRequest)
		return
	}

	// Resolve the payTo address for the 402 challenge.
	//
	// Priority: 1) explicit `payTo` in the request body, 2) existing
	// user's registered payTo, 3) the user's `from` (self-custody).
	payTo := strings.ToLower(strings.TrimSpace(req.PayTo))
	if payTo == "" {
		if u, ok := p.Wallet.GetUser(payer); ok {
			payTo = u.PayTo
		}
	}
	if payTo == "" {
		payTo = payer
	}
	if _, err := normalizeAddr(payTo); err != nil {
		http.Error(w, "invalid payTo: "+err.Error(), http.StatusBadRequest)
		return
	}

	// No payment yet → emit 402
	if req.Payment == nil {
		requirements, nonce, err := p.USDC.BuildRequirements(
			"/v1/topup",
			"AGW prepaid topup (USDC on "+p.USDC.CAIP2()+")",
			payTo,
			amount,
		)
		if err != nil {
			http.Error(w, "build requirements: "+err.Error(), http.StatusInternalServerError)
			return
		}
		p.logTopupChallenge(payer, amount, nonce)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"x402Version": 2,
			"error":       "X-PAYMENT-REQUIRED",
			"accepts":     []PaymentRequirements{requirements},
		})
		return
	}

	// Payment present → verify + (auto-register) + settle + credit
	auth := *req.Payment
	expectedValue, ok := parsePositiveInt(auth.Value)
	if !ok {
		http.Error(w, "payment.value invalid", http.StatusBadRequest)
		return
	}
	if expectedValue != amount {
		http.Error(w, "payment.value does not match requested amount", http.StatusBadRequest)
		return
	}
	// auth.from must equal X-Payer (proves ownership)
	if !strings.EqualFold(strings.TrimPrefix(auth.From, "0x"), strings.TrimPrefix(payer, "0x")) {
		http.Error(w, "payment.from does not match X-Payer", http.StatusBadRequest)
		return
	}
	// auth.to must equal the payTo we promised in the 402
	if !strings.EqualFold(strings.TrimPrefix(auth.To, "0x"), strings.TrimPrefix(payTo, "0x")) {
		http.Error(w, "payment.to does not match server payTo", http.StatusBadRequest)
		return
	}

	// Auto-register the user (idempotent: returns existing if known).
	if _, err := p.Wallet.RegisterUser(payer, payTo); err != nil {
		http.Error(w, "register user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify signature (off-chain; no RPC needed for this step).
	if _, err := p.USDC.VerifyPaymentAuth(payer, auth); err != nil {
		p.Logger.Warn("topup signature rejected", "payer", payer, "error", err.Error())
		http.Error(w, "signature verification failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Settle on-chain (if server key is configured).
	var txHash string
	if p.USDC.ServerKey != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		txHash, err = p.USDC.SettlePaymentAuth(ctx, auth)
		if err != nil {
			p.Logger.Error("topup settlement failed", "payer", payer, "error", err.Error())
			http.Error(w, "settlement failed: "+err.Error(), http.StatusBadGateway)
			return
		}
	} else {
		p.Logger.Warn("topup accepted without on-chain settlement (offline mode)",
			"payer", payer, "amount", amount)
	}

	// Credit user's balance.
	newBal, err := p.Wallet.Credit(payer, amount, "x402 topup", txHash)
	if err != nil {
		p.Logger.Error("wallet credit failed", "payer", payer, "error", err.Error())
		http.Error(w, "credit failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("topup credited",
		"payer", payer, "amount", amount, "balance", newBal, "txHash", txHash)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := TopupResponse{
		OK:      true,
		Balance: formatInt(newBal),
		TxHash:  txHash,
		User:    payer,
		PayTo:   payTo,
	}
	if p.USDC.ServerKey == nil {
		resp.Note = "offline mode: signature verified but not settled on-chain"
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (p *Proxy) logTopupChallenge(payer string, amount int64, nonce [32]byte) {
	p.Logger.Info("topup challenge issued",
		"payer", payer,
		"amount", amount,
		"nonce", "0x"+encodeNonce(nonce[:]),
		"network", p.USDC.CAIP2(),
		"asset", p.USDC.Address.Hex(),
	)
}

// parsePositiveInt parses a decimal string of a positive integer (>0).
func parsePositiveInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if n == 0 {
		return 0, false
	}
	return n, true
}

// formatInt renders a non-negative integer as a decimal string.
func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// encodeNonce renders bytes as 0x-prefixed lowercase hex.
func encodeNonce(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
