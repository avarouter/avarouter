// payments.go — payment gate and balance query endpoints.
//
// The gate runs inside Proxy.ServeHTTP, after management routes have
// short-circuited but before the body is read. It enforces:
//
//   - X-Payer header is present and is a valid 0x address
//   - the address has a positive balance (i.e. has at least one topup)
//   - the hold amount (--payment-hold default: 1 USDC) is reserved
//
// On success, two context values are injected for downstream hooks:
//   - ctx.Value("payer")    = lowercase 0x address
//   - ctx.Value("holdID")   = opaque hold id (string)
//
// The actual settle / refund happens in Proxy.debitOnSettle, called
// by access_log.go:requestLogger after the upstream attempt completes
// (success, fail, or client-disconnect).
//
// We also expose:
//   - GET /payments/balance/{addr}  → {addr, balance, held, updated}
//   - GET /payments/balances        → top N addresses (for admin UI / P1)
package agw

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// PerCallHoldMicro is the hold amount per upstream call in USDC
	// micro-units. 1_000_000 = 1 USDC. Production would compute this
	// from the model's pricing + estimated max tokens; for the demo we
	// use a flat reserve so the happy path is easy to reason about.
	defaultPerCallHoldMicro int64 = 1_000_000
)

// servePaymentsBalance handles GET /payments/balance/{addr}.
//
// This endpoint is intentionally unauthenticated — the address itself
// is the identifier and there is no private data leaked. In a public
// deployment, consider rate-limiting by IP.
func (p *Proxy) servePaymentsBalance(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	addr := strings.TrimPrefix(r.URL.Path, "/payments/balance/")
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}
	snap, err := p.Wallet.Snapshot(addr)
	if err != nil {
		http.Error(w, "invalid address: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

// servePaymentsBalances handles GET /payments/balances?limit=N.
//
// Returns the top N addresses by balance, descending. Default limit 50.
func (p *Proxy) servePaymentsBalances(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		// permissive parse: take leading digits only
		var n int
		for _, c := range v {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > 0 && n <= 1000 {
			limit = n
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": p.Wallet.Snapshots(limit),
		"count": limit,
	})
}

// paymentGate is called from Proxy.ServeHTTP. It returns:
//   - holdID, payer: success → request continues
//   - "", "":        success without hold (legacy/no-wallet mode)
//   - "", payer:     held but caller should treat as success
//   - (writeError=true, status): reject
//
// When writeError is true the gate has already written an HTTP response
// and the caller must return immediately.
func (p *Proxy) paymentGate(w http.ResponseWriter, r *http.Request) (payer, holdID string, writeError bool, status int) {
	// Legacy mode: no wallet configured → free for all
	if p.Wallet == nil {
		return "", "", false, 0
	}

	payer = strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if payer == "" {
		// Allow GET /payments/balance and friends through without payer
		// — those paths short-circuit before this gate is reached.
		http.Error(w, "X-Payer header required (top up via POST /v1/topup first)", http.StatusPaymentRequired)
		return "", "", true, http.StatusPaymentRequired
	}
	if _, err := normalizeAddr(payer); err != nil {
		http.Error(w, "invalid X-Payer: "+err.Error(), http.StatusBadRequest)
		return "", "", true, http.StatusBadRequest
	}

	hold, err := p.Wallet.Hold(payer, defaultPerCallHoldMicro)
	if errors.Is(err, ErrUnknownAddress) {
		// Never topped up → 402 with a hint
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  "X-PAYMENT-REQUIRED",
			"reason": "no balance — top up via POST /v1/topup first",
			"payer":  payer,
			"topup":  "POST /v1/topup with X-Payer and {\"amount\": \"<micro-units>\"}",
		})
		return "", "", true, http.StatusPaymentRequired
	}
	if err != nil {
		p.Logger.Error("wallet hold failed", "payer", payer, "error", err.Error())
		http.Error(w, "payment error: "+err.Error(), http.StatusInternalServerError)
		return "", "", true, http.StatusInternalServerError
	}
	return payer, hold, false, 0
}

// settleOrRefund runs as a deferred call after the upstream attempt.
// It inspects the response status and either settles the hold (full
// debit on 2xx) or refunds it (on non-2xx, client-disconnect, panic).
//
// settle amount = full hold for now (flat per-call pricing). P1 will
// replace this with usage-based pricing derived from the session's
// usageScanner tally.
func (p *Proxy) settleOrRefund(holdID string, w http.ResponseWriter) {
	if p.Wallet == nil || holdID == "" {
		return
	}
	status := 0
	if aw, ok := w.(*accessWriter); ok {
		status = aw.status
	}
	if status >= 200 && status < 300 {
		if err := p.Wallet.Settle(holdID, defaultPerCallHoldMicro); err != nil {
			p.Logger.Error("payment settle failed", "holdID", holdID, "error", err.Error())
		} else {
			p.Logger.Info("payment settled", "holdID", holdID, "amount", defaultPerCallHoldMicro)
		}
		return
	}
	if err := p.Wallet.Refund(holdID); err != nil {
		p.Logger.Error("payment refund failed", "holdID", holdID, "error", err.Error())
	} else {
		p.Logger.Info("payment refunded", "holdID", holdID, "status", status)
	}
}

// initPayment wires the wallet + USDC config into the proxy. Called
// from RunWithOptions when --pay-to is set.
func initPayment(proxy *Proxy, opts *Options, logger *slog.Logger) error {
	// Defaults for Fuji testnet
	if opts.USDCAddress == "" {
		opts.USDCAddress = DefaultFujiUSDC
	}
	if opts.USDCRPCURL == "" {
		opts.USDCRPCURL = DefaultFujiRPC
	}
	if opts.USDCChainID == 0 {
		opts.USDCChainID = DefaultFujiChainID
	}

	// Wallet: persistence under data-dir if available, else in-memory.
	var walletPath string
	if opts.DataDir != "" {
		walletPath = filepath.Join(opts.DataDir, "balances.jsonl")
	}
	wallet, err := NewWallet(walletPath)
	if err != nil {
		return fmt.Errorf("init wallet: %w", err)
	}
	proxy.Wallet = wallet
	mode := "in-memory"
	if walletPath != "" {
		mode = "persistent"
	}
	logger.Info("payment wallet initialized", "path", walletPath, "mode", mode)

	// Server key (optional — empty = offline mode, accept signatures without settlement)
	var serverKey *ecdsa.PrivateKey
	if opts.ServerKeyPath != "" {
		keyHex, err := os.ReadFile(opts.ServerKeyPath)
		if err != nil {
			return fmt.Errorf("read server key: %w", err)
		}
		keyHexStr := strings.TrimSpace(string(keyHex))
		keyHexStr = strings.TrimPrefix(keyHexStr, "0x")
		keyBytes, err := hex.DecodeString(keyHexStr)
		if err != nil {
			return fmt.Errorf("decode server key: %w", err)
		}
		k, err := ecdsaKeyFromBytes(keyBytes)
		if err != nil {
			return fmt.Errorf("parse server key: %w", err)
		}
		serverKey = k
		logger.Info("server key loaded (on-chain settlement enabled)", "path", opts.ServerKeyPath)
	} else {
		logger.Warn("server key not set — topups accepted by signature only, no on-chain settlement")
	}

	usdc, err := NewUSDC(opts.USDCAddress, opts.USDCRPCURL, opts.USDCChainID, serverKey)
	if err != nil {
		return fmt.Errorf("init usdc: %w", err)
	}
	proxy.USDC = usdc
	proxy.PayTo = strings.ToLower(opts.PayTo)

	logger.Info("payments enabled",
		"network", usdc.CAIP2(),
		"asset", usdc.Address.Hex(),
		"payTo", proxy.PayTo,
		"onChainSettlement", serverKey != nil,
		"holdPerCall", defaultPerCallHoldMicro,
	)
	return nil
}
