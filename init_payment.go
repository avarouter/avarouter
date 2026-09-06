// init_payment.go — wires the wallet + USDC config into the proxy
// at startup. Called from RunWithOptions when --pay-to is set.
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
	// env-var application happens in run.go's paymentEnv, before we
	// get here.
	if opts.USDCAddress == "" {
		opts.USDCAddress = DefaultFujiUSDC
	}
	if opts.USDCRPCURL == "" {
		opts.USDCRPCURL = DefaultFujiRPC
	}
	if opts.USDCChainID == 0 {
		opts.USDCChainID = DefaultFujiChainID
	}

	// Owner = --pay-to (single-user mode)
	owner, err := normalizeAddr(opts.PayTo)
	if err != nil {
		return fmt.Errorf("init payment: invalid --pay-to: %w", err)
	}

	// Wallet: persistence under data-dir if available, else in-memory.
	var walletPath string
	if opts.DataDir != "" {
		walletPath = filepath.Join(opts.DataDir, "wallet.jsonl")
	}
	wallet, err := NewWallet(walletPath, owner)
	if err != nil {
		return fmt.Errorf("init wallet: %w", err)
	}
	proxy.Wallet = wallet
	mode := "in-memory"
	if walletPath != "" {
		mode = "persistent"
	}
	bal, _ := wallet.Balance()
	logger.Info("payment wallet initialized",
		"owner", owner,
		"path", walletPath,
		"mode", mode,
		"balance", bal,
	)

	// Server key (optional — empty = offline mode, accept signatures without settlement)
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
	proxy.PayTo = owner

	logger.Info("payments enabled (single-user mode)",
		"network", usdc.CAIP2(),
		"asset", usdc.Address.Hex(),
		"payTo", owner,
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
