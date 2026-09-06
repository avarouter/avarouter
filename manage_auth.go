// manage_auth.go — per-request signature verification for management endpoints.
//
// Why this exists
// ----------------
// X-Payer is a *header* — it identifies the user but does NOT
// prove the caller controls the address. Without a signature, any
// HTTP client can claim to be any user. So the only safe management
// endpoints in v3 are those that already require proof-of-funds
// (the second /v1/topup call, which carries an EIP-3009 signature).
//
// The remaining management surfaces — /v1/keys (POST/GET/DELETE),
// /v1/keys/rotate, and any future operator-only endpoints — must
// require the caller to actually sign with the private key matching
// X-Payer.
//
// Wire format
// -----------
//   X-Payer:        0xabc...     (the user's from address)
//   X-AGW-Timestamp: 1757260800   (unix seconds; ±5 min window)
//   X-AGW-Signature: 0x...        (65-byte EIP-191 personal_sign)
//
// The signed message is a deterministic string built from the
// request itself, so signatures can't be replayed across methods,
// paths, bodies, or users:
//
//   AGW-MANAGE-v1
//   method=POST
//   path=/v1/keys
//   body-sha256=0x<hex of keccak256(body)>
//   timestamp=1757260800
//   payer=0xabc...
//
// Verification
// ------------
// 1. Parse X-Payer, X-AGW-Timestamp, X-AGW-Signature.
// 2. Check timestamp is within ±5 min (replay window).
// 3. Recompute the expected message string from the request.
// 4. personal_ecrecover(message, sig) → recovered address.
// 5. Lowercase(recovered) must equal lowercase(X-Payer).
// 6. The recovered address must be a registered user (or, for
//    /v1/topup first call, any valid address — the signature IS
//    the proof of identity).
//
// On any failure: 401 with a reason.
//
// Notes
// -----
// personal_sign uses Ethereum's prefix "\x19Ethereum Signed Message:\n<len>"
// before keccak256. ethers.js's `wallet.signMessage(text)` does
// exactly this; use that from the client.
package agw

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ManageSignatureWindow is the maximum allowed clock skew between
// client and server for X-AGW-Timestamp. Requests outside the
// window are rejected as replays.
const ManageSignatureWindow = 5 * time.Minute

// ManageAuthVersion is the message-format version, embedded in the
// signed string so future format changes can be detected.
const ManageAuthVersion = "AGW-MANAGE-v1"

// verifyManagementSignature authenticates a management request.
// It accepts EITHER:
//
//   1. A per-request EIP-191 signature (X-Payer + X-AGW-Timestamp
//      + X-AGW-Signature) — the strict, no-state option.
//
//   2. A bearer session token (X-AGW-Session) — proves the user
//      previously signed an /v1/auth challenge; faster for
//      repeated management calls.
//
// requireRegistered: if true, the authenticated user must be in
// the wallet's user list. /v1/topup passes false (the signature IS
// the registration); /v1/keys passes true.
//
// On success: returns the normalized user address.
// On failure: writes a 401 response and returns "".
func (p *Proxy) verifyManagementSignature(w http.ResponseWriter, r *http.Request, requireRegistered bool) string {
	// Path 2: bearer session token.
	if tok := strings.TrimSpace(r.Header.Get("X-AGW-Session")); tok != "" {
		return p.verifySessionToken(w, r, tok, requireRegistered)
	}
	// Path 1: per-request EIP-191 signature.
	return p.verifySignedRequest(w, r, requireRegistered)
}

// verifySessionToken validates X-AGW-Session against the wallet's
// session store. The X-Payer header must match the session's user
// (defence in depth: a leaked token from one user doesn't grant
// access to a different user's account even if headers are mixed).
func (p *Proxy) verifySessionToken(w http.ResponseWriter, r *http.Request, tok string, requireRegistered bool) string {
	if p.Wallet == nil || p.Wallet.Sessions() == nil {
		writeAuthError(w, "session store not initialized")
		return ""
	}
	rec, err := p.Wallet.Sessions().Lookup(tok)
	if err != nil {
		switch err {
		case ErrSessionNotFound:
			writeAuthError(w, "X-AGW-Session unknown or revoked")
		case ErrSessionExpired:
			writeAuthError(w, "X-AGW-Session expired — re-auth via POST /v1/auth")
		default:
			writeAuthError(w, "session lookup failed: "+err.Error())
		}
		return ""
	}
	// X-Payer (if present) must match the session's user.
	xp := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if xp != "" && xp != rec.User {
		writeAuthError(w, fmt.Sprintf("X-Payer (%s) does not match session user (%s)", xp, rec.User))
		return ""
	}
	if requireRegistered && !p.Wallet.HasUser(rec.User) {
		writeAuthError(w, "user not registered — topup first to register")
		return ""
	}
	return rec.User
}

// verifySignedRequest reads X-Payer / X-AGW-Timestamp /
// X-AGW-Signature from r, computes the expected message from
// (method, path, body, timestamp, payer), and verifies the signature
// against the recovered address.
func (p *Proxy) verifySignedRequest(w http.ResponseWriter, r *http.Request, requireRegistered bool) string {
	// (1) Parse headers.
	payer := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if payer == "" {
		writeAuthError(w, "X-Payer header required")
		return ""
	}
	if _, err := normalizeAddr(payer); err != nil {
		writeAuthError(w, "invalid X-Payer: "+err.Error())
		return ""
	}

	tsStr := strings.TrimSpace(r.Header.Get("X-AGW-Timestamp"))
	if tsStr == "" {
		writeAuthError(w, "X-AGW-Timestamp header required (unix seconds)")
		return ""
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		writeAuthError(w, "invalid X-AGW-Timestamp: "+err.Error())
		return ""
	}

	sigHex := strings.TrimSpace(r.Header.Get("X-AGW-Signature"))
	if sigHex == "" {
		writeAuthError(w, "X-AGW-Signature header required (EIP-191 personal_sign of the AGW-MANAGE message)")
		return ""
	}
	sigHex = strings.TrimPrefix(sigHex, "0x")
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		writeAuthError(w, "invalid X-AGW-Signature (expected 65-byte hex)")
		return ""
	}
	// Normalize v: EIP-191 signatures carry v ∈ {27, 28}; go-ethereum
	// expects v ∈ {0, 1}.
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if sig[64] > 1 {
		writeAuthError(w, fmt.Sprintf("invalid v: %d (must be 0/1 or 27/28)", sig[64]))
		return ""
	}

	// (2) Time window.
	now := time.Now().Unix()
	if delta := now - ts; delta > int64(ManageSignatureWindow.Seconds()) || delta < -int64(ManageSignatureWindow.Seconds()) {
		writeAuthError(w, fmt.Sprintf("X-AGW-Timestamp out of window (now=%d, ts=%d, ±%ds)",
			now, ts, int(ManageSignatureWindow.Seconds())))
		return ""
	}

	// (3) Read body (must be replayed into the message).
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeAuthError(w, "read body: "+err.Error())
		return ""
	}
	r.Body.Close()
	// Replace the body so downstream handlers can re-read it.
	r.Body = io.NopCloser(bytes.NewReader(body))

	// (4) Build expected message and recover signer.
	hash := HashManageMessage(r.Method, r.URL.Path, body, ts, payer)
	pub, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		writeAuthError(w, "ecrecover: "+err.Error())
		return ""
	}
	recovered := strings.ToLower(crypto.PubkeyToAddress(*pub).Hex())
	if recovered != payer {
		writeAuthError(w, fmt.Sprintf("signature does not match X-Payer (recovered %s, claimed %s)",
			recovered, payer))
		return ""
	}

	// (5) Optional registration check.
	if requireRegistered && p.Wallet != nil && !p.Wallet.HasUser(payer) {
		writeAuthError(w, "user not registered — topup first to register")
		return ""
	}
	return payer
}

// buildManageMessage renders the deterministic message string that
// the client must sign with personal_sign.
//
// Format:
//
//	AGW-MANAGE-v1
//	method=POST
//	path=/v1/keys
//	body-sha256=0x<hex>
//	timestamp=1757260800
//	payer=0xabc...
func buildManageMessage(method, path string, body []byte, ts int64, payer string) string {
	bodyHash := sha256.Sum256(body)
	return strings.Join([]string{
		ManageAuthVersion,
		"method=" + strings.ToUpper(method),
		"path=" + path,
		"body-sha256=0x" + hex.EncodeToString(bodyHash[:]),
		"timestamp=" + strconv.FormatInt(ts, 10),
		"payer=" + strings.ToLower(payer),
	}, "\n")
}

// EIP191Prefix is the standard "\x19Ethereum Signed Message:\n"
// prefix used by personal_sign and wallet_signMessage. The full
// EIP-191 prefix includes the message length (which the EIP-191
// spec requires to prevent message-confusion attacks), so
// HashManageMessage below appends strconv.Itoa(len(msg)) before
// hashing.
const EIP191Prefix = "\x19Ethereum Signed Message:\n"

// HashManageMessage returns the 32-byte digest that the client
// signature should commit to. Compatible with
// `wallet.signMessage(buildManageMessage(method,path,body,ts,payer))`
// from ethers.js (which itself uses the EIP-191 prefix internally).
func HashManageMessage(method, path string, body []byte, ts int64, payer string) common.Hash {
	msg := buildManageMessage(method, path, body, ts, payer)
	prefixed := EIP191Prefix + strconv.Itoa(len(msg)) + msg
	return crypto.Keccak256Hash([]byte(prefixed))
}
