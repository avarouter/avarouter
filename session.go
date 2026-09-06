// session.go — SIWE-style session tokens.
//
// Why this exists
// ----------------
// Every management endpoint currently requires a per-request
// EIP-191 signature (X-Payer + X-AGW-Timestamp + X-AGW-Signature).
// That's the most secure option, but in practice it means the
// user pops MetaMask on every key mint / revoke / list — unusable
// for a real portal.
//
// Sessions let the user sign ONE challenge (AGW-AUTH) and receive
// a bearer token that authorises management calls for a TTL. The
// signature is still required for the initial /v1/auth call, and
// the token can be revoked at any time.
//
// Wire format
// -----------
// On the auth call (still signed):
//
//   POST /v1/auth
//   Body:    { "from": "0x...", "expiresAt": 1234567890 }
//   Headers: X-AGW-Timestamp + X-AGW-Signature (same shape as
//            management signatures, but the message is
//            AGW-AUTH-v1 ...)
//
// On every subsequent management call:
//
//   POST /v1/keys
//   Headers: X-AGW-Session: ags_<random>
//            X-Payer:        0x...   (must match the session's user)
//
// The session token is a 32-byte secret, base64url-encoded with
// a `ags_` prefix. We store ONLY the SHA-256 hash in memory (and
// in the wallet log), so a leaked log file doesn't yield usable
// tokens. The plaintext is shown to the user once on creation.
//
// Storage
// -------
// Sessions are kept in memory AND persisted in wallet.jsonl as
// `entrySessionCreate` / `entrySessionRevoke` lines. This means
// sessions survive restarts (so a user doesn't have to re-auth
// every deploy) but can be revoked by the user at any time
// (DELETE /v1/auth/sessions/{id}).
//
// TTL
// ---
// - expiresAt:  user-supplied, must be in [now, now+24h]
// - idle timeout: 30 min (no request bumps it; expired sessions
//   are rejected on the next call).
package agw

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// SessionTokenPrefix identifies AGW-issued session tokens.
	SessionTokenPrefix = "ags_"
	// SessionTokenBytes is the random entropy per token (32 bytes
	// = 256 bits → 43 base64url chars + prefix).
	SessionTokenBytes = 32
	// SessionMaxLifetime is the longest a session can be valid
	// for, even if the user requested more.
	SessionMaxLifetime = 24 * time.Hour
	// SessionIdleTimeout is how long a session can sit unused
	// before being rejected. Bumped on each successful use.
	SessionIdleTimeout = 30 * time.Minute
	// AuthVersion is the message-format version, embedded in
	// the signed /v1/auth challenge so future format changes can
	// be detected.
	AuthVersion = "AGW-AUTH-v1"
)

// Session is one active bearer token.
type Session struct {
	ID         string    // sha256(token) hex — NOT the plaintext
	TokenHint  string    // first 8 chars of plaintext, for human ID
	User       string    // the user's from address (lowercase)
	Created    time.Time // when the session was created
	ExpiresAt  time.Time // absolute expiry
	LastSeen   time.Time // bumped on each successful use
	IdleExpiry time.Time // LastSeen + SessionIdleTimeout
}

// Expired reports whether the session is past its absolute expiry
// or its idle timeout. A valid session can still expire on the
// next call if too much time has passed since LastSeen.
func (s *Session) Expired(now time.Time) bool {
	return now.After(s.ExpiresAt) || now.After(s.IdleExpiry)
}

// SessionStore is the in-memory + persisted registry of active
// sessions. Safe for concurrent use.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session // sha256(token) → session
	out      func(kind string, body []byte) error
	clock    func() time.Time
}

// NewSessionStore creates an empty registry. persist is called
// when a session is created or revoked; pass a callback that
// appends to wallet.jsonl.
func NewSessionStore(persist func(kind string, body []byte) error) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		out:      persist,
		clock:    time.Now,
	}
}

// Create issues a new session for the given user. Returns the
// plaintext token (shown to user ONCE) and the stored record.
//
// The user must request an absolute expiresAt no later than
// now+SessionMaxLifetime; we clamp anything beyond that.
func (s *SessionStore) Create(user string, expiresAt time.Time) (plaintext string, rec *Session, err error) {
	if user == "" {
		return "", nil, errors.New("session: user required")
	}
	now := s.clock()
	if expiresAt.Before(now) {
		return "", nil, errors.New("session: expiresAt must be in the future")
	}
	maxExp := now.Add(SessionMaxLifetime)
	if expiresAt.After(maxExp) {
		expiresAt = maxExp
	}
	raw := make([]byte, SessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("session: rand: %w", err)
	}
	plaintext = SessionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	id := hashSessionToken(plaintext)
	hint := plaintext
	if len(hint) > 8 {
		hint = hint[:8]
	}
	rec = &Session{
		ID:         id,
		TokenHint:  hint,
		User:       user,
		Created:    now,
		ExpiresAt:  expiresAt,
		LastSeen:   now,
		IdleExpiry: now.Add(SessionIdleTimeout),
	}
	s.mu.Lock()
	s.sessions[id] = rec
	s.mu.Unlock()
	// Persist (best-effort; log entry is append-only).
	if s.out != nil {
		body := fmt.Sprintf(`{"id":%q,"user":%q,"hint":%q,"expiresAt":%q,"created":%q,"lastSeen":%q,"idleExpiry":%q}`,
			id, user, hint,
			expiresAt.UTC().Format(time.RFC3339Nano),
			now.UTC().Format(time.RFC3339Nano),
			now.UTC().Format(time.RFC3339Nano),
			now.Add(SessionIdleTimeout).UTC().Format(time.RFC3339Nano),
		)
		_ = s.out("session_create", []byte(body))
	}
	return plaintext, rec, nil
}

// Lookup validates a plaintext token and bumps its LastSeen. The
// returned session is the one the token authorises. Returns
// ErrSessionNotFound if the token is unknown, ErrSessionExpired
// if it's past its expiry.
func (s *SessionStore) Lookup(plaintext string) (*Session, error) {
	if plaintext == "" {
		return nil, ErrSessionNotFound
	}
	id := hashSessionToken(plaintext)
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	now := s.clock()
	if rec.Expired(now) {
		return nil, ErrSessionExpired
	}
	rec.LastSeen = now
	rec.IdleExpiry = now.Add(SessionIdleTimeout)
	return rec, nil
}

// Revoke deletes a session by ID. No-op if not found.
func (s *SessionStore) Revoke(id string) bool {
	s.mu.Lock()
	rec, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	delete(s.sessions, id)
	s.mu.Unlock()
	if s.out != nil && rec != nil {
		body := fmt.Sprintf(`{"id":%q,"user":%q,"hint":%q}`,
			rec.ID, rec.User, rec.TokenHint)
		_ = s.out("session_revoke", []byte(body))
	}
	return true
}

// ListByUser returns all non-expired sessions for a user.
func (s *SessionStore) ListByUser(user string) []*Session {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0)
	for _, rec := range s.sessions {
		if rec.User != user {
			continue
		}
		if rec.Expired(now) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Sweep removes expired sessions. Safe to call periodically.
func (s *SessionStore) Sweep() int {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, rec := range s.sessions {
		if rec.Expired(now) {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}

// Replay rebuilds the in-memory session table from persisted
// entries. Call this on startup.
func (s *SessionStore) Replay(entries []SessionReplayEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		if e.Kind == "session_create" {
			s.sessions[e.ID] = &Session{
				ID:         e.ID,
				TokenHint:  e.Hint,
				User:       e.User,
				Created:    e.Created,
				ExpiresAt:  e.ExpiresAt,
				LastSeen:   e.LastSeen,
				IdleExpiry: e.IdleExpiry,
			}
		} else if e.Kind == "session_revoke" {
			delete(s.sessions, e.ID)
		}
	}
}

// SessionReplayEntry is the persisted shape used during startup.
type SessionReplayEntry struct {
	Kind       string
	ID         string
	User       string
	Hint       string
	Created    time.Time
	ExpiresAt  time.Time
	LastSeen   time.Time
	IdleExpiry time.Time
}

func hashSessionToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ErrSessionNotFound is returned by Lookup when the token is
// unknown or has been revoked.
var ErrSessionNotFound = errors.New("session: unknown or revoked token")

// ErrSessionExpired is returned by Lookup when the token is past
// its absolute or idle expiry.
var ErrSessionExpired = errors.New("session: expired")
