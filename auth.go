// auth.go — POST /v1/auth (sign-in) and /v1/auth/logout.
//
// The auth flow:
//
//   POST /v1/auth
//   Body:    {"from": "0x...", "expiresAt": 1234567890}
//   Headers: X-Payer, X-AGW-Timestamp, X-AGW-Signature
//            (same wire format as management signatures, but the
//             message is AGW-AUTH-v1 from=... expiresAt=...
//             timestamp=...)
//
//   Response: {
//     "sessionToken": "ags_...",
//     "user":         "0x...",
//     "expiresAt":    1234567890,
//     "idleExpiresAt": 1234570890
//   }
//
// The plaintext token is shown ONCE. The client must store it
// securely (browser localStorage is OK for a demo; for production
// use a HttpOnly cookie or a dedicated session manager).
//
// Logout:
//
//   POST /v1/auth/logout
//   Headers: X-AGW-Session: ags_...
//
//   Response: {"ok": true, "revoked": "ags_xxx..."}
//
// List active sessions:
//
//   GET /v1/auth/sessions
//   Headers: X-AGW-Session: ags_...   (or signed headers)
//
//   Response: {"user": "0x...", "sessions": [{hint, expiresAt, lastSeen}, ...]}

package agw

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AuthRequest is the body for POST /v1/auth.
type AuthRequest struct {
	From      string `json:"from"`
	ExpiresAt int64  `json:"expiresAt"` // unix seconds
}

// AuthResponse is the success body for POST /v1/auth.
type AuthResponse struct {
	OK            bool   `json:"ok"`
	SessionToken  string `json:"sessionToken"`
	User          string `json:"user"`
	ExpiresAt     int64  `json:"expiresAt"`
	IdleExpiresAt int64  `json:"idleExpiresAt"`
	Warning       string `json:"warning,omitempty"`
}

func (p *Proxy) serveAuth(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil || p.Wallet.Sessions() == nil {
		http.Error(w, "auth not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Step 1: verify the signature (same wire as management; uses
	// AGW-AUTH-v1 message format because of the user-supplied
	// expiresAt in the body).
	payer := p.verifyAuthSignature(w, r)
	if payer == "" {
		return
	}
	// Step 2: read body, parse AuthRequest.
	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.From == "" {
		http.Error(w, `missing "from"`, http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(req.From, payer) {
		http.Error(w, "from does not match X-Payer", http.StatusBadRequest)
		return
	}
	if req.ExpiresAt == 0 {
		http.Error(w, `missing "expiresAt" (unix seconds)`, http.StatusBadRequest)
		return
	}
	exp := time.Unix(req.ExpiresAt, 0)
	// Step 3: mint a session token.
	token, rec, err := p.Wallet.Sessions().Create(payer, exp)
	if err != nil {
		http.Error(w, "create session: "+err.Error(), http.StatusBadRequest)
		return
	}
	p.Logger.Info("session created",
		"user", payer, "hint", rec.TokenHint, "expiresAt", exp)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(AuthResponse{
		OK:            true,
		SessionToken:  token,
		User:          rec.User,
		ExpiresAt:     rec.ExpiresAt.Unix(),
		IdleExpiresAt: rec.IdleExpiry.Unix(),
		Warning:       "this is the only time the plaintext token will be shown — store it now",
	})
}

// serveAuthLogout deletes the session identified by X-AGW-Session.
func (p *Proxy) serveAuthLogout(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil || p.Wallet.Sessions() == nil {
		http.Error(w, "auth not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok := strings.TrimSpace(r.Header.Get("X-AGW-Session"))
	if tok == "" {
		http.Error(w, "X-AGW-Session required", http.StatusBadRequest)
		return
	}
	rec, err := p.Wallet.Sessions().Lookup(tok)
	if err != nil {
		// Already gone — idempotent success.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "revoked": "(already gone)"})
		return
	}
	if !p.Wallet.Sessions().Revoke(rec.ID) {
		http.Error(w, "revoke failed", http.StatusInternalServerError)
		return
	}
	p.Logger.Info("session revoked", "user", rec.User, "hint", rec.TokenHint)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"revoked": rec.TokenHint,
		"user":    rec.User,
	})
}

// serveAuthSessions lists the user's active sessions. Auth via
// X-AGW-Session (any one of the user's) or per-request signature.
func (p *Proxy) serveAuthSessions(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil || p.Wallet.Sessions() == nil {
		http.Error(w, "auth not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := p.verifyManagementSignature(w, r, true)
	if user == "" {
		return
	}
	sessions := p.Wallet.Sessions().ListByUser(user)
	type sessionRow struct {
		Hint          string `json:"hint"`
		Created       string `json:"created"`
		ExpiresAt     string `json:"expiresAt"`
		LastSeen      string `json:"lastSeen"`
		IdleExpiresAt string `json:"idleExpiresAt"`
	}
	rows := make([]sessionRow, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, sessionRow{
			Hint:          s.TokenHint,
			Created:       s.Created.UTC().Format(time.RFC3339),
			ExpiresAt:     s.ExpiresAt.UTC().Format(time.RFC3339),
			LastSeen:      s.LastSeen.UTC().Format(time.RFC3339),
			IdleExpiresAt: s.IdleExpiry.UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user":     user,
		"count":    len(rows),
		"sessions": rows,
	})
}

// verifyAuthSignature is like verifySignedRequest but uses the
// AGW-AUTH-v1 message format (which includes from + expiresAt in
// addition to the standard method/path/body/timestamp/payer).
func (p *Proxy) verifyAuthSignature(w http.ResponseWriter, r *http.Request) string {
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
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		writeAuthError(w, "invalid X-AGW-Timestamp: "+err.Error())
		return ""
	}
	now := time.Now().Unix()
	if delta := now - ts; delta > int64(ManageSignatureWindow.Seconds()) || delta < -int64(ManageSignatureWindow.Seconds()) {
		writeAuthError(w, fmt.Sprintf("X-AGW-Timestamp out of window (now=%d, ts=%d)", now, ts))
		return ""
	}
	sigHex := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("X-AGW-Signature")), "0x")
	sig, err := hexDecodeLen(sigHex, 65)
	if err != nil {
		writeAuthError(w, "invalid X-AGW-Signature: "+err.Error())
		return ""
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if sig[64] > 1 {
		writeAuthError(w, fmt.Sprintf("invalid v: %d", sig[64]))
		return ""
	}
	body, err := readAndRestoreBody(r)
	if err != nil {
		writeAuthError(w, "read body: "+err.Error())
		return ""
	}
	// Build AGW-AUTH-v1 message.
	from, expAt := extractAuthFields(body)
	expected := buildAuthMessage(from, expAt, ts, payer)
	digest := cryptoKeccak256(EIP191Prefix + strconvItoa(len(expected)) + expected)
	pub, err := sigToPub(digest, sig)
	if err != nil {
		writeAuthError(w, "ecrecover: "+err.Error())
		return ""
	}
	recovered := strings.ToLower(pubkeyToAddress(pub))
	if recovered != payer {
		writeAuthError(w, fmt.Sprintf("signature does not match X-Payer (recovered %s, claimed %s)", recovered, payer))
		return ""
	}
	return payer
}

// buildAuthMessage renders the AGW-AUTH-v1 message that the client
// must sign.
//
//   AGW-AUTH-v1
//   from=0xAlice
//   expiresAt=1757...
//   timestamp=1757...
//   payer=0xAlice
func buildAuthMessage(from string, expiresAt int64, ts int64, payer string) string {
	return strings.Join([]string{
		AuthVersion,
		"from=" + strings.ToLower(from),
		"expiresAt=" + strconv.FormatInt(expiresAt, 10),
		"timestamp=" + strconv.FormatInt(ts, 10),
		"payer=" + strings.ToLower(payer),
	}, "\n")
}

// extractAuthFields pulls the from + expiresAt out of the auth body
// (for inclusion in the signed message). We need this BEFORE
// verifyManagementSignature can run, since the body itself is
// part of what's signed.
func extractAuthFields(body []byte) (string, int64) {
	var r AuthRequest
	_ = json.Unmarshal(body, &r)
	return strings.ToLower(r.From), r.ExpiresAt
}
