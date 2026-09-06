// wallet.go — single-user prepaid balance ledger + API key store.
//
// Design (revised for single-user demo):
//   - One wallet address (configured via --pay-to, also called "owner").
//   - Balance in USDC micro-units (1 USDC = 1_000_000).
//   - API keys: opaque tokens, stored as SHA-256 hashes; plaintext only
//     returned at generation time. Owner can rotate; old key is
//     immediately invalidated.
//   - Persistence: append-only JSONL under {data-dir}/wallet.jsonl
//     (one file for both balance entries and key entries).
//   - Hot path: credit on topup, debit after upstream usage is known.
//     No hold/settle/refund dance — the chain is offline once the
//     topup is credited.
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
	entryCredit   = "credit"
	entryDebit    = "debit"
	entryKeyIssue = "key_issue"
	entryKeyRevoke = "key_revoke"
)

// Entry is one line in wallet.jsonl.
type Entry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Addr    string    `json:"addr"`
	Amount  int64     `json:"amount,omitempty"`
	KeyHash string    `json:"keyHash,omitempty"`
	KeyMeta string    `json:"keyMeta,omitempty"`
	Note    string    `json:"note,omitempty"`
	TxHash  string    `json:"txHash,omitempty"`
}

// KeyMeta is what we expose for /v1/keys (never includes the key
// itself, only the hash and a short prefix for identification).
type KeyMeta struct {
	Hash     string `json:"hash"`
	Prefix   string `json:"prefix"`     // first 8 chars of plaintext, for human ID
	IssuedAt string `json:"issuedAt"`
	Active   bool   `json:"active"`
}

// Snapshot is a JSON-serializable view for /payments/balance.
type Snapshot struct {
	Addr     string `json:"addr"`
	Balance  int64  `json:"balance"`
	Updated  string `json:"updated"`
}

// Wallet is the single-user ledger. Safe for concurrent use.
type Wallet struct {
	mu       sync.Mutex
	balances map[string]int64      // addr → micro-USDC
	keys     map[string]keyRecord   // keyHash → record
	addr     string                // owner address (locked at construction)
	logPath  string
	out      io.Writer
}

type keyRecord struct {
	hash      string
	prefix    string
	issuedAt  time.Time
	active    bool
}

// NewWallet opens (or creates) a wallet.jsonl and replays the log to
// rebuild the in-memory state. The owner address is locked at
// construction and used to enforce single-user mode.
func NewWallet(logPath, ownerAddr string) (*Wallet, error) {
	if ownerAddr == "" {
		return nil, errors.New("wallet: owner address required")
	}
	owner, err := normalizeAddr(ownerAddr)
	if err != nil {
		return nil, err
	}
	w := &Wallet{
		balances: make(map[string]int64),
		keys:     make(map[string]keyRecord),
		addr:     owner,
		logPath:  logPath,
	}
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

// Addr returns the locked owner address.
func (w *Wallet) Addr() string { return w.addr }

// replay rebuilds balances and key state by walking the JSONL log.
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
		switch e.Kind {
		case entryCredit:
			w.balances[e.Addr] += e.Amount
		case entryDebit:
			w.balances[e.Addr] -= e.Amount
		case entryKeyIssue:
			w.keys[e.KeyHash] = keyRecord{
				hash:     e.KeyHash,
				prefix:   e.KeyMeta,
				issuedAt: e.At,
				active:   true,
			}
		case entryKeyRevoke:
			if r, ok := w.keys[e.KeyHash]; ok {
				r.active = false
				w.keys[e.KeyHash] = r
			}
		}
	}
	return nil
}

// append writes one entry to the JSONL log. Caller holds w.mu.
func (w *Wallet) append(e Entry) error {
	if w.out == nil {
		return nil
	}
	e.Addr = strings.ToLower(e.Addr)
	e.At = time.Now().UTC()
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

// requireOwner returns the locked owner address; callers should use
// this to gate any per-user action.
func (w *Wallet) requireOwner() (string, error) {
	if w.addr == "" {
		return "", errors.New("wallet: no owner configured")
	}
	return w.addr, nil
}

// Credit adds `amount` micro-units to owner's balance (e.g. on a
// confirmed topup). Returns the new balance.
func (w *Wallet) Credit(amount int64, note, txHash string) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("credit amount must be positive")
	}
	owner, err := w.requireOwner()
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balances[owner] += amount
	if err := w.append(Entry{Kind: entryCredit, Addr: owner, Amount: amount, Note: note, TxHash: txHash}); err != nil {
		return 0, err
	}
	return w.balances[owner], nil
}

// Debit subtracts `amount` micro-units from owner's balance. Returns
// the new balance. Negative balance is allowed (we mark the user
// overdrawn; next request is rejected by auth middleware).
func (w *Wallet) Debit(amount int64, note string) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("debit amount must be positive")
	}
	owner, err := w.requireOwner()
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balances[owner] -= amount
	if err := w.append(Entry{Kind: entryDebit, Addr: owner, Amount: amount, Note: note}); err != nil {
		return 0, err
	}
	return w.balances[owner], nil
}

// Balance returns the owner's current balance.
func (w *Wallet) Balance() (int64, error) {
	owner, err := w.requireOwner()
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balances[owner], nil
}

// Snapshot returns balance + timestamp for the owner.
func (w *Wallet) Snapshot() (Snapshot, error) {
	bal, err := w.Balance()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Addr:    w.addr,
		Balance: bal,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ---------- API key management ----------

// GenerateKey returns a brand-new API key for the owner and stores
// its hash. The plaintext is shown once and never recoverable; callers
// must surface it to the user immediately.
func (w *Wallet) GenerateKey() (plaintext string, meta KeyMeta, err error) {
	owner, err := w.requireOwner()
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
		IssuedAt: now.Format(time.RFC3339),
		Active:   true,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.keys[hash] = keyRecord{
		hash:     hash,
		prefix:   prefix,
		issuedAt: now,
		active:   true,
	}
	if err := w.append(Entry{
		Kind:    entryKeyIssue,
		Addr:    owner,
		KeyHash: hash,
		KeyMeta: prefix,
	}); err != nil {
		// roll back
		delete(w.keys, hash)
		return "", KeyMeta{}, err
	}
	return plaintext, meta, nil
}

// LookupKey returns the owner address if the plaintext is a valid,
// currently active key. Returns ErrUnknownKey otherwise.
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
	return w.addr, nil
}

// RotateKey invalidates all currently active keys and returns a new
// plaintext. Use this when a key is leaked.
func (w *Wallet) RotateKey() (plaintext string, meta KeyMeta, err error) {
	owner, err := w.requireOwner()
	if err != nil {
		return "", KeyMeta{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// Revoke all currently active keys (write to log so it survives
	// across restarts).
	now := time.Now().UTC()
	for h, r := range w.keys {
		if !r.active {
			continue
		}
		r.active = false
		w.keys[h] = r
		_ = w.append(Entry{Kind: entryKeyRevoke, Addr: owner, KeyHash: h, At: now})
	}
	// Issue a new one (without holding the lock again — already held).
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
	meta = KeyMeta{
		Hash:     hash,
		Prefix:   prefix,
		IssuedAt: now.Format(time.RFC3339),
		Active:   true,
	}
	w.keys[hash] = keyRecord{
		hash:     hash,
		prefix:   prefix,
		issuedAt: now,
		active:   true,
	}
	if err := w.append(Entry{
		Kind:    entryKeyIssue,
		Addr:    owner,
		KeyHash: hash,
		KeyMeta: prefix,
		At:      now,
	}); err != nil {
		delete(w.keys, hash)
		return "", KeyMeta{}, err
	}
	return plaintext, meta, nil
}

// RevokeByPrefix deactivates the key whose stored prefix matches.
// Returns the revoked key's hash on success, or ErrUnknownKey if
// no active key has that prefix. To revoke ALL active keys, use
// RotateKey instead.
//
// Matching is done against the stored prefix field (the first 8
// characters of the plaintext). If multiple active keys share the
// same prefix (vanishingly unlikely with 8 base64url chars of
// entropy), the oldest is revoked.
func (w *Wallet) RevokeByPrefix(prefix string) (string, error) {
	owner, err := w.requireOwner()
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var target string
	var oldestIssued time.Time
	for h, r := range w.keys {
		if !r.active {
			continue
		}
		if r.prefix != prefix {
			continue
		}
		if target == "" || r.issuedAt.Before(oldestIssued) {
			target = h
			oldestIssued = r.issuedAt
		}
	}
	if target == "" {
		return "", ErrUnknownKey
	}
	r := w.keys[target]
	r.active = false
	w.keys[target] = r
	if err := w.append(Entry{Kind: entryKeyRevoke, Addr: owner, KeyHash: target}); err != nil {
		// best-effort rollback (so future lookups still work)
		r.active = true
		w.keys[target] = r
		return "", err
	}
	return target, nil
}

// ListKeys returns metadata for all keys ever issued (active and
// revoked). For the demo this is a small set; production might cap.
func (w *Wallet) ListKeys() []KeyMeta {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]KeyMeta, 0, len(w.keys))
	for _, r := range w.keys {
		out = append(out, KeyMeta{
			Hash:     r.hash,
			Prefix:   r.prefix,
			IssuedAt: r.issuedAt.Format(time.RFC3339),
			Active:   r.active,
		})
	}
	// newest first
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].IssuedAt > out[j-1].IssuedAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// hashKey returns the SHA-256 of the plaintext key, hex-encoded.
func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ErrUnknownKey is returned by LookupKey when the key is unknown
// or has been revoked.
var ErrUnknownKey = errors.New("wallet: unknown or revoked key")
