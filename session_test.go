package agw

import (
	"errors"
	"testing"
	"time"
)

func TestSessionCreateAndLookup(t *testing.T) {
	store := NewSessionStore(nil)
	tok, rec, err := store.Create(alice, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !startsWith(tok, SessionTokenPrefix) {
		t.Fatalf("token missing prefix: %q", tok)
	}
	if rec.User != alice {
		t.Fatalf("rec.User: %q, want %q", rec.User, alice)
	}
	// Lookup with the same plaintext should succeed.
	got, err := store.Lookup(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.User != alice {
		t.Fatalf("Lookup user: %q", got.User)
	}
	// Lookup with a wrong token should fail.
	if _, err := store.Lookup("ags_wrong"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("wrong token: err=%v, want ErrSessionNotFound", err)
	}
}

func TestSessionExpired(t *testing.T) {
	store := NewSessionStore(nil)
	// backdate the clock to 0
	store.clock = func() time.Time { return time.Unix(0, 0) }
	tok, _, err := store.Create(alice, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	// At t=5: valid (not yet expired, not idle-expired)
	store.clock = func() time.Time { return time.Unix(5, 0) }
	if _, err := store.Lookup(tok); err != nil {
		t.Fatalf("at t=5 should be valid, got err=%v", err)
	}
	// At t=12: absolute expired
	store.clock = func() time.Time { return time.Unix(12, 0) }
	if _, err := store.Lookup(tok); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("at t=12 should be expired, got err=%v", err)
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	store := NewSessionStore(nil)
	store.clock = func() time.Time { return time.Unix(0, 0) }
	tok, _, err := store.Create(alice, time.Unix(3600, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Bump lastSeen at t=0
	if _, err := store.Lookup(tok); err != nil {
		t.Fatal(err)
	}
	// 31 minutes later: idle expired (SessionIdleTimeout = 30m)
	store.clock = func() time.Time { return time.Unix(31*60, 0) }
	if _, err := store.Lookup(tok); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("after 31m idle should be expired, got err=%v", err)
	}
}

func TestSessionIdleBumpOnUse(t *testing.T) {
	store := NewSessionStore(nil)
	store.clock = func() time.Time { return time.Unix(0, 0) }
	tok, _, err := store.Create(alice, time.Unix(3600, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Use at t=0, t=20m, t=40m (each within idle window from previous)
	for _, t0 := range []int64{0, 20 * 60, 40 * 60} {
		store.clock = func() time.Time { return time.Unix(t0, 0) }
		if _, err := store.Lookup(tok); err != nil {
			t.Fatalf("at t=%d should be valid (last bump extends idle), got err=%v", t0, err)
		}
	}
}

func TestSessionRevoke(t *testing.T) {
	store := NewSessionStore(nil)
	tok, rec, err := store.Create(alice, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !store.Revoke(rec.ID) {
		t.Fatal("Revoke should return true for known ID")
	}
	if _, err := store.Lookup(tok); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("after revoke, lookup should fail, got err=%v", err)
	}
	// Revoking twice is a no-op.
	if store.Revoke(rec.ID) {
		t.Fatal("Revoke should return false for unknown ID")
	}
}

func TestSessionListByUser(t *testing.T) {
	store := NewSessionStore(nil)
	_, _, _ = store.Create(alice, time.Now().Add(time.Hour))
	_, _, _ = store.Create(alice, time.Now().Add(time.Hour))
	_, _, _ = store.Create(bob, time.Now().Add(time.Hour))
	if got := len(store.ListByUser(alice)); got != 2 {
		t.Errorf("alice sessions: got %d, want 2", got)
	}
	if got := len(store.ListByUser(bob)); got != 1 {
		t.Errorf("bob sessions: got %d, want 1", got)
	}
}

func TestSessionSweep(t *testing.T) {
	store := NewSessionStore(nil)
	store.clock = func() time.Time { return time.Unix(0, 0) }
	_, _, _ = store.Create(alice, time.Unix(10, 0))
	_, _, _ = store.Create(bob, time.Unix(100, 0))
	store.clock = func() time.Time { return time.Unix(50, 0) }
	if n := store.Sweep(); n != 1 {
		t.Errorf("Sweep: got %d, want 1", n)
	}
	if got := len(store.ListByUser(alice)); got != 0 {
		t.Errorf("alice: got %d, want 0", got)
	}
	if got := len(store.ListByUser(bob)); got != 1 {
		t.Errorf("bob: got %d, want 1", got)
	}
}

func TestSessionMaxLifetimeClamp(t *testing.T) {
	store := NewSessionStore(nil)
	// Try to create a session 30 days out — should clamp to 24h.
	_, rec, err := store.Create(alice, time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	delta := rec.ExpiresAt.Sub(rec.Created)
	if delta > SessionMaxLifetime+time.Minute {
		t.Fatalf("ExpiresAt should be clamped to ≤ %s, got %s", SessionMaxLifetime, delta)
	}
}

func TestSessionReplayRebuildsStore(t *testing.T) {
	// Round-trip: persist via the wallet, replay into a new store.
	dir := t.TempDir()
	path := joinPath(dir, "wallet.jsonl")
	w, _ := NewWallet(path)
	// Wipe default out — NewWallet set it.
	_, _, _ = w.Sessions().Create(alice, time.Now().Add(time.Hour))
	_, _, _ = w.Sessions().Create(bob, time.Now().Add(time.Hour))
	// Revoke the first one.
	sessions := w.Sessions().ListByUser(alice)
	if len(sessions) != 1 {
		t.Fatalf("alice: got %d, want 1", len(sessions))
	}
	w.Sessions().Revoke(sessions[0].ID)
	w.Close()

	// Re-open and check.
	w2, err := NewWallet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if got := len(w2.Sessions().ListByUser(alice)); got != 0 {
		t.Errorf("after restart: alice sessions: got %d, want 0 (revoked)", got)
	}
	if got := len(w2.Sessions().ListByUser(bob)); got != 1 {
		t.Errorf("after restart: bob sessions: got %d, want 1", got)
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	if a[len(a)-1] == '/' {
		return a + b
	}
	return a + "/" + b
}
