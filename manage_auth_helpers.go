// manage_auth_helpers.go — small utilities used by both
// manage_auth.go and auth.go. Kept separate so the test files
// don't pull in the entire auth flow.

package agw

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// readAndRestoreBody reads up to 1 MiB of the request body and
// replaces r.Body with a fresh reader so downstream handlers can
// re-read it. Returns the bytes (or any read error).
func readAndRestoreBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// hexDecodeLen decodes a hex string (with or without 0x prefix)
// and verifies it is exactly wantLen bytes long.
func hexDecodeLen(s string, wantLen int) ([]byte, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != wantLen {
		return nil, fmt.Errorf("expected %d bytes, got %d", wantLen, len(raw))
	}
	return raw, nil
}

// cryptoKeccak256 computes the keccak256 of its input. Wrapped for
// symmetry with sigToPub / pubkeyToAddress below.
func cryptoKeccak256(s string) []byte {
	return crypto.Keccak256([]byte(s))
}

// sigToPub wraps crypto.SigToPub.
func sigToPub(hash []byte, sig []byte) (common.Address, error) {
	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// pubkeyToAddress returns the EVM address of a public key. Takes
// a common.Address (we already extract it in sigToPub above).
func pubkeyToAddress(addr common.Address) string {
	return addr.Hex()
}

// strconvItoa is a thin wrapper used by both files; mostly so
// that auth.go can avoid importing strconv directly.
func strconvItoa(n int) string {
	return strconvItoaImpl(n)
}

func strconvItoaImpl(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
