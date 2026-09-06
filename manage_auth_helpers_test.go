package agw

import (
	"crypto/ecdsa"
	"encoding/hex"
	"io"
	"log/slog"

	"github.com/ethereum/go-ethereum/crypto"
)

// privFromHex converts a hex private key to *ecdsa.PrivateKey.
// Hex may be 0x-prefixed or not. Panics on bad hex.
func privFromHex(s string) *ecdsa.PrivateKey {
	k, err := crypto.HexToECDSA(s)
	if err != nil {
		panic("privFromHex: " + err.Error())
	}
	return k
}

// hexKey returns the 0x-prefixed hex of the private key bytes.
func hexKey(k *ecdsa.PrivateKey) string {
	return "0x" + hex.EncodeToString(crypto.FromECDSA(k))
}

// silentLogger returns a *slog.Logger that writes to io.Discard.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
