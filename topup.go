// topup.go — POST /v1/topup is the single x402-protected endpoint in
// this gateway. It accepts an EIP-3009 signed authorization, verifies
// the signature, optionally settles on-chain, and credits the
// internal wallet balance.
//
// Wire format (x402 V2 compatible):
//   Request 1 (no payment yet):
//     POST /v1/topup
//     Content-Type: application/json
//     X-Payer: 0xABC...         (the address to credit)
//     {"amount": "1000000"}     (USDC micro-units, e.g. 1 USDC = 1_000_000)
//
//   Response 1 (402 Payment Required):
//     HTTP/1.1 402 Payment Required
//     Content-Type: application/json
//     {
//       "x402Version": 2,
//       "accepts": [{...PaymentRequirements with nonce + payTo...}],
//       "error": "X-PAYMENT-REQUIRED"
//     }
//
//   Request 2 (with signed authorization):
//     POST /v1/topup
//     X-Payer: 0xABC...
//     {"amount": "1000000", "payment": {"from":"0x..", "to":"0x..", "value":"...", ...}}
//
//   Response 2 (200 OK, balance credited):
//     {"ok": true, "balance": "1000000", "txHash": "0x..."}
package agw

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// TopupRequest is the client-side body for POST /v1/topup.
//
// Amount is the requested credit in USDC micro-units. Payment (optional
// in the first round-trip) is the EIP-3009 signed authorization.
type TopupRequest struct {
	Amount  string      `json:"amount"`
	Payment *PaymentAuth `json:"payment,omitempty"`
}

// TopupResponse is the success body after a topup completes.
type TopupResponse struct {
	OK      bool   `json:"ok"`
	Balance string `json:"balance"`     // new balance, micro-units
	TxHash  string `json:"txHash,omitempty"`
	Note    string `json:"note,omitempty"`
}

// serveTopup is the HTTP handler. It is NOT a management route, so it
// is gated by X-Payer and runs through the wallet, not basicAuth.
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
		http.Error(w, "X-Payer header required", http.StatusBadRequest)
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

	// No payment yet → emit 402
	if req.Payment == nil {
		amount, ok := parsePositiveInt(req.Amount)
		if !ok {
			http.Error(w, `invalid "amount"`, http.StatusBadRequest)
			return
		}
		requirements, nonce, err := p.USDC.BuildRequirements(
			"/v1/topup",
			"AGW prepaid topup (USDC on "+p.USDC.CAIP2()+")",
			p.PayTo,
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

	// Payment present → verify + settle + credit
	auth := *req.Payment
	amount, ok := parsePositiveInt(req.Amount)
	if !ok {
		http.Error(w, `invalid "amount"`, http.StatusBadRequest)
		return
	}
	expectedValue, ok := parsePositiveInt(auth.Value)
	if !ok {
		http.Error(w, "payment.value invalid", http.StatusBadRequest)
		return
	}
	if expectedValue != amount {
		http.Error(w, "payment.value does not match requested amount", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(strings.TrimPrefix(auth.To, "0x"), strings.TrimPrefix(p.PayTo, "0x")) {
		http.Error(w, "payment.to does not match server payTo", http.StatusBadRequest)
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
		// Offline mode: trust the signed authorization. Acceptable for
		// demo only; production must settle on-chain.
		p.Logger.Warn("topup accepted without on-chain settlement (offline mode)",
			"payer", payer, "amount", amount)
	}

	// Credit wallet
	newBal, err := p.Wallet.Credit(payer, amount, "x402 topup", txHash)
	if err != nil {
		p.Logger.Error("wallet credit failed", "payer", payer, "error", err.Error())
		http.Error(w, "credit failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("topup credited",
		"payer", payer, "amount", amount, "balance", newBal, "txHash", txHash)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := TopupResponse{OK: true, Balance: formatInt(newBal), TxHash: txHash}
	if p.USDC.ServerKey == nil {
		resp.Note = "offline mode: signature verified but not settled on-chain"
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// logTopupChallenge records the 402 emit for /logs.
func (p *Proxy) logTopupChallenge(payer string, amount int64, nonce [32]byte) {
	p.Logger.Info("topup challenge issued",
		"payer", payer,
		"amount", amount,
		"nonce", "0x"+encodeNonce(nonce[:]),
		"network", p.USDC.CAIP2(),
		"asset", p.USDC.Address.Hex(),
		"payTo", p.PayTo,
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

// errMissingPayer is exported for tests.
var errMissingPayer = errors.New("X-Payer header required")
