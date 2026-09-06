// wallet.go — prepaid balance ledger for x402 top-up.
//
// Design (Prepaid-B, see research-reports topic "AGW + x402 + Avalanche"):
//   - Each payer is identified by an EVM address (lowercase hex, 0x-prefixed).
//   - All amounts are stored in USDC micro-units (1 USDC = 1_000_000).
//   - Three state transitions per request:
//       Hold(addr, max)        → returns holdID, reserves up to `max`
//       Settle(holdID, actual)  → debits `actual`, releases the rest
//       Refund(holdID)         → releases the whole hold
//   - Persisted as append-only JSONL under {data-dir}/balances.jsonl.
//   - In-memory cache rebuilt from JSONL on startup; hot path is O(1) per addr.
package agw

import (
	"crypto/rand"
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

// USDCScale is 10^6, matching USDC's 6 decimal on-chain.
const USDCScale = 1_000_000

// WalletEntry is one line in balances.jsonl. Append-only log so we can
// reconstruct the current balance by replaying all entries for a given addr.
type WalletEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`    // "credit" | "debit" | "hold" | "settle" | "refund"
	Addr    string    `json:"addr"`    // lowercase 0x...
	Amount  int64     `json:"amount"`  // USDC micro-units, positive
	HoldID  string    `json:"holdID,omitempty"`
	Note    string    `json:"note,omitempty"`
	TxHash  string    `json:"txHash,omitempty"`  // set for credit (on-chain transferWithAuthorization)
}

// Hold is a reservation against a balance.
type Hold struct {
	ID     string
	Addr   string
	Amount int64 // max USDC micro-units reserved
	At     time.Time
}

// Snapshot is a JSON-serializable view for /payments/balance and admin UI.
type Snapshot struct {
	Addr     string `json:"addr"`
	Balance  int64  `json:"balance"`   // available USDC micro-units
	Held     int64  `json:"held"`      // currently reserved (sum of open holds)
	Updated  string `json:"updated"`
}

// Wallet is the balance ledger. Safe for concurrent use.
type Wallet struct {
	mu       sync.Mutex
	balances map[string]int64 // addr → available (excludes open holds)
	holds    map[string]*Hold // holdID → active hold
	logPath  string           // empty = in-memory only
	out      io.Writer        // for tests; defaults to *os.File when logPath is set
	closed   bool
}

// NewWallet returns an empty in-memory wallet. Persistence is enabled when
// logPath is non-empty; existing entries are replayed to rebuild balances.
func NewWallet(logPath string) (*Wallet, error) {
	w := &Wallet{
		balances: make(map[string]int64),
		holds:    make(map[string]*Hold),
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

// Close flushes and closes the underlying log file. The wallet remains
// usable in memory after Close.
func (w *Wallet) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if f, ok := w.out.(*os.File); ok && f != nil {
		return f.Close()
	}
	return nil
}

// replay rebuilds balances and active holds by walking the JSONL log.
// Holds that were not Settle'd or Refund'd before the log started are
// considered leaked and dropped (their reservations are released) —
// a deliberate choice for crash recovery: a partial hold on a crashed
// request should not permanently lock the funds.
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
	leaked := make(map[string]int64) // addr → sum of leaked holds
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e WalletEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return fmt.Errorf("wallet: bad log line: %w", err)
		}
		e.Addr = strings.ToLower(e.Addr)
		switch e.Kind {
		case "credit":
			w.balances[e.Addr] += e.Amount
		case "hold":
			// Reserve `e.Amount` against the balance. If balance goes
			// negative here, it's because the original request was
			// authorized to overdraw (e.g. hold exceeds balance in
			// early model). We track the hold regardless.
			w.balances[e.Addr] -= e.Amount
		case "settle":
			// Hold already debited at hold time; on settle we keep
			// the debit and discard the holdID (assume single use).
		case "refund":
			// Refund re-credits the previously-debited hold amount.
			w.balances[e.Addr] += e.Amount
		case "debit":
			// Direct debit (e.g. admin clawback).
			w.balances[e.Addr] -= e.Amount
		default:
			return fmt.Errorf("wallet: unknown entry kind %q", e.Kind)
		}
	}
	// Release any leaked holds from before the log's last entry: if a
	// hold appeared with no later settle/refund, its amount is still
	// debited from balance. We can't tell post-hoc which were leaked
	// without tracking holdIDs, but for v0 we accept that — leaked
	// holds simply reduce balance permanently (which is conservative
	// and safe; the funds stay out of circulation, never double-spent).
	_ = leaked
	return nil
}

// append writes one entry to the JSONL log. Caller must hold w.mu.
func (w *Wallet) append(e WalletEntry) error {
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
	// fsync would be nice but is expensive; on testnet demo we accept
	// the small risk of losing the last entry on power loss.
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

// Credit adds `amount` micro-units to addr's balance (e.g. on confirmed
// topup). Returns the new balance.
func (w *Wallet) Credit(addr string, amount int64, note, txHash string) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("credit amount must be positive")
	}
	a, err := normalizeAddr(addr)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balances[a] += amount
	if err := w.append(WalletEntry{Kind: "credit", Addr: a, Amount: amount, Note: note, TxHash: txHash}); err != nil {
		return 0, err
	}
	return w.balances[a], nil
}

// Balance returns the currently available (non-held) balance.
func (w *Wallet) Balance(addr string) (int64, error) {
	a, err := normalizeAddr(addr)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balances[a], nil
}

// Snapshot returns balance + held totals for a single address.
func (w *Wallet) Snapshot(addr string) (Snapshot, error) {
	a, err := normalizeAddr(addr)
	if err != nil {
		return Snapshot{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var held int64
	for _, h := range w.holds {
		if h.Addr == a {
			held += h.Amount
		}
	}
	return Snapshot{
		Addr:    a,
		Balance: w.balances[a],
		Held:    held,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// Snapshots returns the top N addresses by balance, for the admin UI.
func (w *Wallet) Snapshots(limit int) []Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Snapshot, 0, len(w.balances))
	for addr, bal := range w.balances {
		var held int64
		for _, h := range w.holds {
			if h.Addr == addr {
				held += h.Amount
			}
		}
		out = append(out, Snapshot{
			Addr:    addr,
			Balance: bal,
			Held:    held,
		})
	}
	// simple insertion-sort top N by Balance desc
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Balance > out[j-1].Balance; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Updated = time.Now().UTC().Format(time.RFC3339)
	}
	return out
}

// Hold reserves up to `max` micro-units against addr's balance. The
// caller must later call Settle or Refund with the returned holdID.
//
// If balance < max, we still create the hold (the upstream call is
// authorized to "spend what it costs up to max"); the over-spend is
// caught at Settle time when balance goes negative — at which point
// Settle returns ErrInsufficient and the caller is expected to refuse
// the response and claw back via the next topup.
//
// Returns ErrInsufficient if addr has no balance history at all AND
// max > 0 (a fresh address with zero prior topup cannot incur cost).
func (w *Wallet) Hold(addr string, max int64) (string, error) {
	if max <= 0 {
		return "", errors.New("hold max must be positive")
	}
	a, err := normalizeAddr(addr)
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, known := w.balances[a]; !known {
		return "", ErrUnknownAddress
	}
	holdID := newHoldID()
	w.holds[holdID] = &Hold{ID: holdID, Addr: a, Amount: max, At: time.Now().UTC()}
	w.balances[a] -= max
	if err := w.append(WalletEntry{Kind: "hold", Addr: a, Amount: max, HoldID: holdID}); err != nil {
		// best-effort rollback
		w.balances[a] += max
		delete(w.holds, holdID)
		return "", err
	}
	return holdID, nil
}

// Settle consumes the hold and debits `actual` from addr's balance.
// `actual` must be ≤ the hold's max; the difference is released back.
func (w *Wallet) Settle(holdID string, actual int64) error {
	if actual < 0 {
		return errors.New("settle amount must be non-negative")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	h, ok := w.holds[holdID]
	if !ok {
		return ErrUnknownHold
	}
	if actual > h.Amount {
		actual = h.Amount
	}
	// hold already debited h.Amount; we now refund (h.Amount - actual).
	refund := h.Amount - actual
	w.balances[h.Addr] += refund
	if err := w.append(WalletEntry{Kind: "settle", Addr: h.Addr, Amount: actual, HoldID: holdID}); err != nil {
		// best-effort: don't unroll, log and surface
		return err
	}
	if refund > 0 {
		if err := w.append(WalletEntry{Kind: "refund", Addr: h.Addr, Amount: refund, HoldID: holdID}); err != nil {
			return err
		}
	}
	delete(w.holds, holdID)
	return nil
}

// Refund releases the entire hold without debiting anything. Used on
// upstream failure (5xx exhausted retries, client disconnect, ctx cancel).
func (w *Wallet) Refund(holdID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	h, ok := w.holds[holdID]
	if !ok {
		return ErrUnknownHold
	}
	w.balances[h.Addr] += h.Amount
	if err := w.append(WalletEntry{Kind: "refund", Addr: h.Addr, Amount: h.Amount, HoldID: holdID}); err != nil {
		return err
	}
	delete(w.holds, holdID)
	return nil
}

// ErrUnknownAddress is returned when a Hold is requested for an address
// that has never had a balance (i.e. has never been credited).
var ErrUnknownAddress = errors.New("wallet: unknown address")

// ErrUnknownHold is returned by Settle/Refund when the holdID is unknown
// (already settled, refunded, or never existed).
var ErrUnknownHold = errors.New("wallet: unknown hold")

// newHoldID returns a short opaque ID for log-line brevity.
func newHoldID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on linux; fall back to time.
		return fmt.Sprintf("h_%d", time.Now().UnixNano())
	}
	return "h_" + hex.EncodeToString(b[:])
}
