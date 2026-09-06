// wallet.go — multi-tenant prepaid balance ledger + API key store.
//
// Design (v3, multi-tenant):
//   - N users, each identified by their EVM `from` address (which
//     they sign with, proving ownership).
//   - Each user has their own balance and chooses their own `payTo`
//     (deposit) address — typically the same as `from`, or a sub-account.
//   - API keys: opaque tokens, stored as SHA-256 hashes; plaintext
//     only returned at generation. Each key is owned by exactly one
//     user; can only be revoked by that user.
//   - Persistence: append-only JSONL under {data-dir}/wallet.jsonl
//     (one file for users, balances, and keys).
//   - Hot path: credit on topup, debit after upstream usage is known.
//     No hold/settle/refund dance — the chain is offline once the
//     topup is credited (when --payments-server-key is unset).
package agw

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// USDCScale is 10^6, matching USDC's 6 decimal on-chain.
	USDCScale = 1_000_000
	// APIKeyPrefix identifies AGW-issued keys to operators looking
	// at logs / clipboard.
	APIKeyPrefix = "agw_"
	// APIKeyRawBytes is the number of random bytes in a generated key
	// (32 bytes = 256 bits of entropy → 43 base64url chars + prefix).
	APIKeyRawBytes = 32
)

// Entry kinds in wallet.jsonl. Append-only.
const (
	entryUserRegister   = "user_register"
	entryCredit         = "credit"
	entryDebit          = "debit"
	entryKeyIssue       = "key_issue"
	entryKeyRevoke      = "key_revoke"
	entrySessionCreate  = "session_create"
	entrySessionRevoke  = "session_revoke"
)

// Entry is one line in wallet.jsonl.
//
// Addr is the user's `from` address (their identity). PayTo is only
// set on `user_register` entries; the rest inherit PayTo from the
// user record in memory.
type Entry struct {
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	Addr      string    `json:"addr"`
	PayTo     string    `json:"payTo,omitempty"`
	Amount    int64     `json:"amount,omitempty"`
	KeyHash   string    `json:"keyHash,omitempty"`
	KeyMeta   string    `json:"keyMeta,omitempty"`
	Note      string    `json:"note,omitempty"`
	TxHash    string    `json:"txHash,omitempty"`
	// Session fields (only used for session_create / session_revoke).
	SessionID    string    `json:"sessionId,omitempty"`
	SessionHint  string    `json:"sessionHint,omitempty"`
	SessionExp   time.Time `json:"sessionExpiresAt,omitempty"`
	SessionLast  time.Time `json:"sessionLastSeen,omitempty"`
	SessionIdle  time.Time `json:"sessionIdleExpiry,omitempty"`
}

// KeyMeta is what we expose for /v1/keys (never includes the key
// itself, only the hash and a short prefix for identification).
type KeyMeta struct {
	Hash     string `json:"hash"`
	Prefix   string `json:"prefix"`
	Owner    string `json:"owner"`
	IssuedAt string `json:"issuedAt"`
	Active   bool   `json:"active"`
}

// UserSnapshot is a JSON-serializable view of one user.
type UserSnapshot struct {
	From    string `json:"from"`
	PayTo   string `json:"payTo"`
	Balance int64  `json:"balance"`
	Created string `json:"created"`
	Updated string `json:"updated"`
	Active  bool   `json:"active"`
}

// Wallet is the multi-tenant ledger. Safe for concurrent use.
type Wallet struct {
	mu       sync.Mutex
	logPath  string
	out      io.Writer
	users    map[string]*userAccount // from → user
	keys     map[string]*keyRecord   // keyHash → record (with owner)
	sessions *SessionStore           // SIWE-style bearer tokens
}

type userAccount struct {
	from     string
	payTo    string
	balance  int64
	created  time.Time
	updated  time.Time
	active   bool
}

type keyRecord struct {
	hash     string
	prefix   string
	owner    string // from address of the user who owns this key
	issuedAt time.Time
	active   bool
}

// NewWallet opens (or creates) a wallet.jsonl and replays the log to
// rebuild the in-memory state. No owner address is required at
// construction — users self-register on first topup.
func NewWallet(logPath string) (*Wallet, error) {
	w := &Wallet{
		users:   make(map[string]*userAccount),
		keys:    make(map[string]*keyRecord),
		logPath: logPath,
	}
	// Session store: persists via the same wallet.jsonl. We need
	// a stable clock function (time.Now) and a persist callback
	// that appends to w.out (set later, if logPath is non-empty).
	w.sessions = NewSessionStore(nil)
	if logPath == "" {
		return w, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("wallet: mkdir: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("wallet: open log: %w", err)
	}
	w.out = f
	// Now wire the session persist callback.
	w.sessions.out = func(kind string, body []byte) error {
		var e Entry
		e.Kind = kind
		switch kind {
		case entrySessionCreate:
			// body is the JSON we synthesized; parse minimally
			// to populate Entry fields. body looks like:
			//   {"id":"<sha256hex>","user":"0x...","hint":"ags_xxx",
			//    "expiresAt":"...","created":"...","lastSeen":"...","idleExpiry":"..."}
			var p struct {
				ID         string `json:"id"`
				User       string `json:"user"`
				Hint       string `json:"hint"`
				ExpiresAt  string `json:"expiresAt"`
				Created    string `json:"created"`
				LastSeen   string `json:"lastSeen"`
				IdleExpiry string `json:"idleExpiry"`
			}
			_ = json.Unmarshal(body, &p)
			e.Addr = p.User
			e.SessionID = p.ID // sha256(token) — used to look up on restart
			e.SessionHint = p.Hint
			if t, err := time.Parse(time.RFC3339Nano, p.ExpiresAt); err == nil {
				e.SessionExp = t
			}
			if t, err := time.Parse(time.RFC3339Nano, p.Created); err == nil {
				e.At = t
			}
			if t, err := time.Parse(time.RFC3339Nano, p.LastSeen); err == nil {
				e.SessionLast = t
			}
			if t, err := time.Parse(time.RFC3339Nano, p.IdleExpiry); err == nil {
				e.SessionIdle = t
			}
		case entrySessionRevoke:
			// body: {"id":"<sha256hex>","user":"...","hint":"..."}
			var p struct {
				ID   string `json:"id"`
				User string `json:"user"`
				Hint string `json:"hint"`
			}
			_ = json.Unmarshal(body, &p)
			e.SessionID = p.ID // sha256(token) — must match create entry
			e.Addr = p.User
			e.SessionHint = p.Hint
		}
		return w.append(e)
	}
	if err := w.replay(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Close flushes and closes the underlying log file.
func (w *Wallet) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if f, ok := w.out.(*os.File); ok && f != nil {
		return f.Close()
	}
	return nil
}

// ---------- user management ----------

// RegisterUser creates a new user (or returns the existing one if
// `from` already registered). payTo defaults to `from` if empty.
// Updating an existing user's payTo is allowed (operator-driven
// rotation, e.g. moving to a new hot wallet).
func (w *Wallet) RegisterUser(from, payTo string) (*UserSnapshot, error) {
	fromN, err := normalizeAddr(from)
	if err != nil {
		return nil, err
	}
	if payTo == "" {
		payTo = fromN
	}
	payToN, err := normalizeAddr(payTo)
	if err != nil {
		return nil, fmt.Errorf("payTo: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if u, ok := w.users[fromN]; ok {
		// Update payTo if it changed (allowed).
		if u.payTo != payToN {
			u.payTo = payToN
			u.updated = time.Now().UTC()
			_ = w.append(Entry{Kind: entryUserRegister, Addr: fromN, PayTo: payToN})
		}
		return userSnapshot(u), nil
	}
	now := time.Now().UTC()
	u := &userAccount{
		from:    fromN,
		payTo:   payToN,
		balance: 0,
		created: now,
		updated: now,
		active:  true,
	}
	w.users[fromN] = u
	if err := w.append(Entry{Kind: entryUserRegister, Addr: fromN, PayTo: payToN}); err != nil {
		delete(w.users, fromN)
		return nil, err
	}
	return userSnapshot(u), nil
}

// GetUser returns a copy of the user record, or false if unknown.
func (w *Wallet) GetUser(from string) (*UserSnapshot, bool) {
	fromN, err := normalizeAddr(from)
	if err != nil {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.users[fromN]
	if !ok {
		return nil, false
	}
	return userSnapshot(u), true
}

// ListUsers returns all users, sorted by Created ASC.
func (w *Wallet) ListUsers() []*UserSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*UserSnapshot, 0, len(w.users))
	for _, u := range w.users {
		out = append(out, userSnapshot(u))
	}
	// stable sort by created
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Created < out[j-1].Created; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// HasUser reports whether the address is registered. Useful for
// requireUserPayer gates.
func (w *Wallet) HasUser(from string) bool {
	fromN, err := normalizeAddr(from)
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.users[fromN]
	return ok
}

// Sessions returns the session store, for token validation /
// creation / revocation. The store is shared with the wallet's
// persistence layer, so sessions are durable across restarts.
func (w *Wallet) Sessions() *SessionStore {
	return w.sessions
}

// Snapshot returns balance + timestamp for one user.
func (w *Wallet) Snapshot(from string) (UserSnapshot, error) {
	fromN, err := normalizeAddr(from)
	if err != nil {
		return UserSnapshot{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.users[fromN]
	if !ok {
		return UserSnapshot{From: fromN}, nil // zero balance, not registered
	}
	return *userSnapshot(u), nil
}

// ---------- balance operations ----------

// Credit adds `amount` micro-units to a user's balance. If the user
// is not yet registered, it's auto-registered with payTo = from
// (the standard "self-custody" case). Returns the new balance.
func (w *Wallet) Credit(from string, amount int64, note, txHash string) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("credit amount must be positive")
	}
	fromN, err := normalizeAddr(from)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.users[fromN]; !ok {
		// auto-register with payTo = from (self-custody default)
		now := time.Now().UTC()
		w.users[fromN] = &userAccount{
			from: fromN, payTo: fromN,
			balance: 0, created: now, updated: now, active: true,
		}
		_ = w.append(Entry{Kind: entryUserRegister, Addr: fromN, PayTo: fromN})
	}
	u := w.users[fromN]
	u.balance += amount
	u.updated = time.Now().UTC()
	if err := w.append(Entry{Kind: entryCredit, Addr: fromN, Amount: amount, Note: note, TxHash: txHash}); err != nil {
		u.balance -= amount // rollback
		return 0, err
	}
	return u.balance, nil
}

// Debit subtracts `amount` micro-units from a user's balance. Returns
// the new balance. Negative balance is allowed (the user is overdrawn
// for this call; the next call is rejected by auth middleware).
func (w *Wallet) Debit(from string, amount int64, note string) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("debit amount must be positive")
	}
	fromN, err := normalizeAddr(from)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.users[fromN]
	if !ok {
		// Debit against an unknown user is a no-op with 0 — the
		// caller (charge) should have already filtered out unknown
		// keys. We log a zero-amount debit for forensics.
		_ = w.append(Entry{Kind: entryDebit, Addr: fromN, Amount: amount, Note: "ORPHAN: " + note})
		return 0, nil
	}
	u.balance -= amount
	u.updated = time.Now().UTC()
	if err := w.append(Entry{Kind: entryDebit, Addr: fromN, Amount: amount, Note: note}); err != nil {
		u.balance += amount
		return 0, err
	}
	return u.balance, nil
}

// Balance returns a user's current balance. Zero (no error) for
// unknown users — call HasUser first if you need to distinguish.
func (w *Wallet) Balance(from string) (int64, error) {
	fromN, err := normalizeAddr(from)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.users[fromN]
	if !ok {
		return 0, nil
	}
	return u.balance, nil
}

// ---------- API key management ----------

// GenerateKey returns a brand-new API key for the given user and
// stores its hash. The plaintext is shown once and never recoverable.
// If the user is not registered, it's auto-registered (defensive).
func (w *Wallet) GenerateKey(owner string) (plaintext string, meta KeyMeta, err error) {
	ownerN, err := normalizeAddr(owner)
	if err != nil {
		return "", KeyMeta{}, err
	}
	raw := make([]byte, APIKeyRawBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", KeyMeta{}, fmt.Errorf("key: rand: %w", err)
	}
	plaintext = APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashKey(plaintext)
	prefix := plaintext
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().UTC()
	meta = KeyMeta{
		Hash:     hash,
		Prefix:   prefix,
		Owner:    ownerN,
		IssuedAt: now.Format(time.RFC3339),
		Active:   true,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.users[ownerN]; !ok {
		// defensive: registering a key for an unknown user
		w.users[ownerN] = &userAccount{
			from: ownerN, payTo: ownerN,
			balance: 0, created: now, updated: now, active: true,
		}
		_ = w.append(Entry{Kind: entryUserRegister, Addr: ownerN, PayTo: ownerN})
	}
	w.keys[hash] = &keyRecord{
		hash: hash, prefix: prefix, owner: ownerN, issuedAt: now, active: true,
	}
	if err := w.append(Entry{
		Kind: entryKeyIssue, Addr: ownerN,
		KeyHash: hash, KeyMeta: prefix,
	}); err != nil {
		delete(w.keys, hash)
		return "", KeyMeta{}, err
	}
	return plaintext, meta, nil
}

// LookupKey returns the owner address of a valid active key, or
// ErrUnknownKey. The owner's `from` address is what bearer auth
// uses to charge the correct account.
func (w *Wallet) LookupKey(plaintext string) (string, error) {
	if plaintext == "" {
		return "", ErrUnknownKey
	}
	hash := hashKey(plaintext)
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.keys[hash]
	if !ok || !r.active {
		return "", ErrUnknownKey
	}
	return r.owner, nil
}

// RotateKey invalidates all of `owner`'s active keys and returns a
// new one. Other users' keys are untouched.
func (w *Wallet) RotateKey(owner string) (plaintext string, meta KeyMeta, err error) {
	ownerN, err := normalizeAddr(owner)
	if err != nil {
		return "", KeyMeta{}, err
	}
	raw := make([]byte, APIKeyRawBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", KeyMeta{}, fmt.Errorf("key: rand: %w", err)
	}
	plaintext = APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashKey(plaintext)
	prefix := plaintext
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().UTC()
	meta = KeyMeta{
		Hash:     hash,
		Prefix:   prefix,
		Owner:    ownerN,
		IssuedAt: now.Format(time.RFC3339),
		Active:   true,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.users[ownerN]; !ok {
		// defensive
		w.users[ownerN] = &userAccount{
			from: ownerN, payTo: ownerN,
			balance: 0, created: now, updated: now, active: true,
		}
		_ = w.append(Entry{Kind: entryUserRegister, Addr: ownerN, PayTo: ownerN})
	}
	for h, r := range w.keys {
		if r.owner != ownerN || !r.active {
			continue
		}
		r.active = false
		w.keys[h] = r
		_ = w.append(Entry{Kind: entryKeyRevoke, Addr: ownerN, KeyHash: h, At: now})
	}
	w.keys[hash] = &keyRecord{
		hash: hash, prefix: prefix, owner: ownerN, issuedAt: now, active: true,
	}
	if err := w.append(Entry{
		Kind: entryKeyIssue, Addr: ownerN,
		KeyHash: hash, KeyMeta: prefix,
	}); err != nil {
		delete(w.keys, hash)
		return "", KeyMeta{}, err
	}
	return plaintext, meta, nil
}

// RevokeByPrefix deactivates the key whose stored prefix matches AND
// belongs to `owner`. If owner is empty, no ownership check is done
// (admin mode). Returns the revoked key's hash on success, or
// ErrUnknownKey if no active key matches.
func (w *Wallet) RevokeByPrefix(owner, prefix string) (string, error) {
	ownerN := ""
	if owner != "" {
		n, err := normalizeAddr(owner)
		if err != nil {
			return "", err
		}
		ownerN = n
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var target string
	var targetRec *keyRecord
	for h, r := range w.keys {
		if !r.active {
			continue
		}
		if r.prefix != prefix {
			continue
		}
		if ownerN != "" && r.owner != ownerN {
			continue
		}
		if targetRec == nil || r.issuedAt.Before(targetRec.issuedAt) {
			target = h
			targetRec = r
		}
	}
	if target == "" {
		return "", ErrUnknownKey
	}
	targetRec.active = false
	if err := w.append(Entry{Kind: entryKeyRevoke, Addr: targetRec.owner, KeyHash: target}); err != nil {
		targetRec.active = true
		return "", err
	}
	return target, nil
}

// ListKeys returns metadata for one user's keys (active and revoked),
// newest first. If owner is empty, returns all keys across all users
// (admin view).
func (w *Wallet) ListKeys(owner string) []KeyMeta {
	w.mu.Lock()
	defer w.mu.Unlock()
	ownerN := ""
	if owner != "" {
		n, err := normalizeAddr(owner)
		if err == nil {
			ownerN = n
		}
	}
	out := make([]KeyMeta, 0, len(w.keys))
	for _, r := range w.keys {
		if ownerN != "" && r.owner != ownerN {
			continue
		}
		out = append(out, KeyMeta{
			Hash:     r.hash,
			Prefix:   r.prefix,
			Owner:    r.owner,
			IssuedAt: r.issuedAt.Format(time.RFC3339),
			Active:   r.active,
		})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].IssuedAt > out[j-1].IssuedAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---------- persistence ----------

// replay rebuilds the in-memory state from the JSONL log.
func (w *Wallet) replay() error {
	if w.logPath == "" {
		return nil
	}
	data, err := os.ReadFile(w.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("wallet: read log: %w", err)
	}
	var replay []SessionReplayEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return fmt.Errorf("wallet: bad log line: %w", err)
		}
		e.Addr = strings.ToLower(e.Addr)
		e.PayTo = strings.ToLower(e.PayTo)
		switch e.Kind {
		case entryUserRegister:
			if _, ok := w.users[e.Addr]; !ok {
				pt := e.PayTo
				if pt == "" {
					pt = e.Addr
				}
				w.users[e.Addr] = &userAccount{
					from: e.Addr, payTo: pt,
					created: e.At, updated: e.At, active: true,
				}
			}
		case entryCredit:
			u := w.ensureUserLocked(e.Addr, e.PayTo)
			u.balance += e.Amount
			u.updated = e.At
		case entryDebit:
			u := w.ensureUserLocked(e.Addr, e.PayTo)
			u.balance -= e.Amount
			u.updated = e.At
		case entryKeyIssue:
			owner := e.Addr
			if owner == "" {
				continue
			}
			_ = w.ensureUserLocked(owner, "")
			w.keys[e.KeyHash] = &keyRecord{
				hash:     e.KeyHash,
				prefix:   e.KeyMeta,
				owner:    owner,
				issuedAt: e.At,
				active:   true,
			}
		case entryKeyRevoke:
			if r, ok := w.keys[e.KeyHash]; ok {
				r.active = false
			}
		case entrySessionCreate, entrySessionRevoke:
			replay = append(replay, SessionReplayEntry{
				Kind:       e.Kind,
				ID:         e.SessionID,
				User:       e.Addr,
				Hint:       e.SessionHint,
				Created:    e.At,
				ExpiresAt:  e.SessionExp,
				LastSeen:   e.SessionLast,
				IdleExpiry: e.SessionIdle,
			})
		}
	}
	// Rebuild the session table from the collected entries.
	if w.sessions != nil && len(replay) > 0 {
		w.sessions.Replay(replay)
	}
	return nil
}

// ensureUserLocked returns the user for addr, creating one if needed
// (defensive: old log lines or replayed data without a prior register
// entry). Caller must hold w.mu.
func (w *Wallet) ensureUserLocked(addr, payTo string) *userAccount {
	u, ok := w.users[addr]
	if ok {
		if payTo != "" && u.payTo == "" {
			u.payTo = payTo
		}
		return u
	}
	pt := payTo
	if pt == "" {
		pt = addr
	}
	u = &userAccount{
		from: addr, payTo: pt,
		created: time.Now().UTC(), updated: time.Now().UTC(), active: true,
	}
	w.users[addr] = u
	return u
}

// append writes one entry to the JSONL log. Caller holds w.mu.
func (w *Wallet) append(e Entry) error {
	if w.out == nil {
		return nil
	}
	e.Addr = strings.ToLower(e.Addr)
	e.PayTo = strings.ToLower(e.PayTo)
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := w.out.Write(data); err != nil {
		return fmt.Errorf("wallet: append log: %w", err)
	}
	return nil
}

// ---------- helpers ----------

// normalizeAddr lowercases and validates an EVM address.
func normalizeAddr(addr string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(addr))
	if !strings.HasPrefix(a, "0x") || len(a) != 42 {
		return "", fmt.Errorf("invalid address %q", addr)
	}
	for _, c := range a[2:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("invalid hex in address %q", addr)
		}
	}
	return a, nil
}

func userSnapshot(u *userAccount) *UserSnapshot {
	return &UserSnapshot{
		From:    u.from,
		PayTo:   u.payTo,
		Balance: u.balance,
		Created: u.created.Format(time.RFC3339),
		Updated: u.updated.Format(time.RFC3339),
		Active:  u.active,
	}
}

// hashKey returns the SHA-256 of the plaintext key, hex-encoded.
func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ErrUnknownKey is returned by LookupKey when the key is unknown
// or has been revoked.
var ErrUnknownKey = errors.New("wallet: unknown or revoked key")
