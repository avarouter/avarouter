// init_payment.go — wire up the multi-tenant payment system.
//
// Multi-tenant design (v3):
//   - There is NO single owner / pay-to address. Users self-register
//     on their first topup by signing an EIP-3009 transfer.
//   - Each user chooses their own payTo (defaults to their from).
//   - The server key (if configured) is a HOT wallet used to
//     broadcast EIP-3009 transactions as the spender, with gas paid
//     by the server. The server key's address is NOT a "user" —
//     it doesn't appear in wallet.jsonl.
package agw

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func initPayment(proxy *Proxy, opts *Options, logger *slog.Logger) error {
	// Defaults for Fuji testnet (only apply if no flag / env override).
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
	// No owner addr in construction — users self-register.
	var walletPath string
	if opts.DataDir != "" {
		walletPath = filepath.Join(opts.DataDir, "wallet.jsonl")
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
	logger.Info("payment wallet initialized",
		"path", walletPath,
		"mode", mode,
		"users", len(wallet.ListUsers()),
	)

	// Server key (optional — empty = offline mode, accept signatures
	// without settlement). This is the gas-paying hot wallet, not a
	// "user" — server key addresses are NOT auto-registered.
	var serverKey *ecdsa.PrivateKey
	if opts.ServerKeyPath != "" {
		serverKey, err = loadServerKey(opts.ServerKeyPath)
		if err != nil {
			return fmt.Errorf("server key: %w", err)
		}
		logger.Info("server key loaded (on-chain settlement enabled)", "source", keySourceLabel(opts.ServerKeyPath))
	} else {
		logger.Warn("server key not set — topups accepted by signature only, no on-chain settlement")
	}

	usdc, err := NewUSDC(opts.USDCAddress, opts.USDCRPCURL, opts.USDCChainID, serverKey)
	if err != nil {
		return fmt.Errorf("init usdc: %w", err)
	}
	proxy.USDC = usdc
	// proxy.PayTo is gone — USDC has no fixed destination; each user
	// supplies their own payTo via EIP-3009 `to` field.

	logger.Info("payments enabled (multi-tenant open registration)",
		"network", usdc.CAIP2(),
		"asset", usdc.Address.Hex(),
		"onChainSettlement", serverKey != nil,
	)
	return nil
}

// loadServerKey accepts either a 0x-prefixed / raw 64-char hex key, or
// a path to a file containing it.
func loadServerKey(spec string) (*ecdsa.PrivateKey, error) {
	var raw []byte
	if strings.HasPrefix(spec, "0x") || len(spec) == 64 {
		h := strings.TrimPrefix(spec, "0x")
		raw, _ = hex.DecodeString(h)
	} else {
		data, err := os.ReadFile(spec)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", spec, err)
		}
		raw, _ = hex.DecodeString(strings.TrimSpace(strings.TrimPrefix(string(data), "0x")))
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (got %d)", len(raw))
	}
	return ecdsaKeyFromBytes(raw)
}

func keySourceLabel(spec string) string {
	if strings.HasPrefix(spec, "0x") || len(spec) == 64 {
		return "inline"
	}
	return "file:" + spec
}
