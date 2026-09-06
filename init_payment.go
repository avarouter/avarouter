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
	proxy.PayTo = owner

	logger.Info("payments enabled (single-user mode)",
		"network", usdc.CAIP2(),
		"asset", usdc.Address.Hex(),
		"payTo", owner,
		"onChainSettlement", serverKey != nil,
	)
	return nil
}
