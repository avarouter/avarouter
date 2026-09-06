package agw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test addresses — these are paired with the priv keys below so
// the manage-auth tests can sign on their behalf. The key+address
// pairs are real (secp256k1-derived), the values just look like
// hex.
const (
	alice       = "0xa4da2ab7a6dcd8d60030900e95027aaa3af22d18"
	alicePriv   = "4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6"
	bob         = "0x2c125abfad46eadcd323ce64d8ed1f9c6ba29c56"
	bobPriv     = "20e790bcec70cda7fd671f7d3b779722fd709cd73a8c15b77c77cca71a026f43"
	carol       = "0x9999999999999999999999999999999999999999"
)

func TestWalletCreditDebit(t *testing.T) {
	w, _ := NewWallet("")

	// Initial balance for unknown user is 0.
	if bal, _ := w.Balance(alice); bal != 0 {
		t.Fatalf("initial balance: got %d, want 0", bal)
	}

	// Credit auto-registers the user.
	newBal, err := w.Credit(alice, 1000, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if newBal != 1000 {
		t.Fatalf("after credit: got %d, want 1000", newBal)
	}
	if !w.HasUser(alice) {
		t.Fatal("alice should be auto-registered after first credit")
	}

	// Debit reduces.
	newBal, err = w.Debit(alice, 250, "test")
	if err != nil {
		t.Fatal(err)
	}
	if newBal != 750 {
		t.Fatalf("after debit: got %d, want 750", newBal)
	}
}

func TestWalletDebitRejectsNonPositive(t *testing.T) {
	w, _ := NewWallet("")
	_, err := w.Credit(alice, 100, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Debit(alice, 0, "x"); err == nil {
		t.Fatal("debit 0 should fail")
	}
	if _, err := w.Debit(alice, -10, "x"); err == nil {
		t.Fatal("debit negative should fail")
	}
}

func TestWalletCreditRejectsNonPositive(t *testing.T) {
	w, _ := NewWallet("")
	if _, err := w.Credit(alice, 0, "x", ""); err == nil {
		t.Fatal("credit 0 should fail")
	}
	if _, err := w.Credit(alice, -10, "x", ""); err == nil {
		t.Fatal("credit negative should fail")
	}
}

func TestWalletKeyGenerateLookupRotate(t *testing.T) {
	w, _ := NewWallet("")
	// First mint a key for alice (auto-registers alice).
	plain, meta, err := w.GenerateKey(alice)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, APIKeyPrefix) {
		t.Fatalf("key missing prefix: %q", plain)
	}
	if !meta.Active {
		t.Fatal("newly issued key should be active")
	}
	if meta.Owner != "0xa4da2ab7a6dcd8d60030900e95027aaa3af22d18" {
		t.Fatalf("meta.Owner: got %q, want normalized alice", meta.Owner)
	}
	owner, err := w.LookupKey(plain)
	if err != nil {
		t.Fatal(err)
	}
	if owner != "0xa4da2ab7a6dcd8d60030900e95027aaa3af22d18" {
		t.Fatalf("LookupKey: owner=%q err=%v", owner, err)
	}

	// Rotate: old key should be revoked, new key works.
	plain2, _, err := w.RotateKey(alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.LookupKey(plain); err != ErrUnknownKey {
		t.Fatalf("old key should be revoked, got err=%v", err)
	}
	if _, err := w.LookupKey(plain2); err != nil {
		t.Fatalf("new key should be valid, got err=%v", err)
	}
}

func TestWalletKeyRejectsEmpty(t *testing.T) {
	w, _ := NewWallet("")
	if _, err := w.LookupKey(""); err != ErrUnknownKey {
		t.Fatalf("empty key: err=%v, want ErrUnknownKey", err)
	}
	if _, err := w.LookupKey("agw_notreallyrandom"); err != ErrUnknownKey {
		t.Fatalf("bogus key: err=%v, want ErrUnknownKey", err)
	}
}

// Multi-key: each user has their own keys; revocation is per-user.
func TestWalletMultiKeyAndRevokeByPrefix(t *testing.T) {
	w, _ := NewWallet("")

	plainA, metaA, err := w.GenerateKey(alice)
	if err != nil {
		t.Fatal(err)
	}
	plainB, metaB, err := w.GenerateKey(alice)
	if err != nil {
		t.Fatal(err)
	}
	if metaA.Prefix == metaB.Prefix {
		t.Skipf("prefixes collided (%q); retry", metaA.Prefix)
	}
	plainBob, metaBob, err := w.GenerateKey(bob)
	if err != nil {
		t.Fatal(err)
	}

	// Both alice's keys should be active and belong to her.
	if owner, _ := w.LookupKey(plainA); owner != strings.ToLower(alice) {
		t.Fatalf("plainA owner: %q, want %q", owner, strings.ToLower(alice))
	}
	if owner, _ := w.LookupKey(plainB); owner != strings.ToLower(alice) {
		t.Fatalf("plainB owner: %q, want %q", owner, strings.ToLower(alice))
	}
	if owner, _ := w.LookupKey(plainBob); owner != strings.ToLower(bob) {
		t.Fatalf("plainBob owner: %q, want %q", owner, strings.ToLower(bob))
	}

	// Alice revokes her key with prefixA. Her other key + bob's key
	// must survive.
	hash, err := w.RevokeByPrefix(alice, metaA.Prefix)
	if err != nil {
		t.Fatalf("RevokeByPrefix: %v", err)
	}
	if hash == "" {
		t.Fatal("RevokeByPrefix returned empty hash")
	}
	if _, err := w.LookupKey(plainA); err != ErrUnknownKey {
		t.Fatalf("plainA should be revoked")
	}
	if _, err := w.LookupKey(plainB); err != nil {
		t.Fatalf("plainB should still be valid")
	}
	if _, err := w.LookupKey(plainBob); err != nil {
		t.Fatalf("plainBob (different user) should be unaffected")
	}

	// alice cannot revoke bob's key (ownership check).
	if _, err := w.RevokeByPrefix(alice, metaBob.Prefix); err != ErrUnknownKey {
		t.Fatalf("alice should not be able to revoke bob's key")
	}
	// bob can revoke his own.
	if _, err := w.RevokeByPrefix(bob, metaBob.Prefix); err != nil {
		t.Fatalf("bob should be able to revoke his key: %v", err)
	}

	// Re-revoking the same prefix should fail (already inactive).
	if _, err := w.RevokeByPrefix(alice, metaA.Prefix); err != ErrUnknownKey {
		t.Fatalf("second revoke should fail")
	}

	// Bogus prefix also fails.
	if _, err := w.RevokeByPrefix(alice, "agw_doesntexist"); err != ErrUnknownKey {
		t.Fatalf("bogus prefix should fail")
	}
}

func TestWalletPerUserKeys(t *testing.T) {
	w, _ := NewWallet("")
	for _, u := range []string{alice, bob, carol} {
		if _, _, err := w.GenerateKey(u); err != nil {
			t.Fatalf("generate for %s: %v", u, err)
		}
	}
	keys := w.ListKeys("")
	if len(keys) != 3 {
		t.Fatalf("ListKeys(all): got %d, want 3", len(keys))
	}
	aliceKeys := w.ListKeys(alice)
	if len(aliceKeys) != 1 {
		t.Fatalf("ListKeys(alice): got %d, want 1", len(aliceKeys))
	}
	if aliceKeys[0].Owner != strings.ToLower(alice) {
		t.Fatalf("alice key owner: %q", aliceKeys[0].Owner)
	}
}

func TestWalletUsersList(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 100, "x", "")
	w.Credit(bob, 200, "x", "")
	w.Credit(alice, 50, "x", "")
	users := w.ListUsers()
	if len(users) != 2 {
		t.Fatalf("ListUsers: got %d, want 2", len(users))
	}
	// alice first (created first)
	if users[0].From != strings.ToLower(alice) {
		t.Fatalf("first user: %q, want %q", users[0].From, strings.ToLower(alice))
	}
	if users[0].Balance != 150 {
		t.Fatalf("alice balance: %d, want 150", users[0].Balance)
	}
	if users[1].Balance != 200 {
		t.Fatalf("bob balance: %d, want 200", users[1].Balance)
	}
}

func TestWalletPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.jsonl")
	w, err := NewWallet(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Credit(alice, 1000, "first", "")
	w.Credit(bob, 500, "first", "")
	plain, _, _ := w.GenerateKey(alice)
	w.Close()

	// Re-open and check state.
	w2, err := NewWallet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if bal, _ := w2.Balance(alice); bal != 1000 {
		t.Fatalf("reopen alice balance: %d, want 1000", bal)
	}
	if bal, _ := w2.Balance(bob); bal != 500 {
		t.Fatalf("reopen bob balance: %d, want 500", bal)
	}
	if owner, err := w2.LookupKey(plain); err != nil || owner != strings.ToLower(alice) {
		t.Fatalf("reopen key: err=%v owner=%q", err, owner)
	}
	users := w2.ListUsers()
	if len(users) != 2 {
		t.Fatalf("reopen ListUsers: %d, want 2", len(users))
	}
}

func TestWalletSnapshotUnknownUser(t *testing.T) {
	w, _ := NewWallet("")
	snap, err := w.Snapshot(alice)
	if err != nil {
		t.Fatal(err)
	}
	if snap.From != strings.ToLower(alice) {
		t.Fatalf("snapshot.From: %q", snap.From)
	}
	if snap.Balance != 0 {
		t.Fatalf("snapshot.Balance: %d, want 0", snap.Balance)
	}
	if snap.Active {
		t.Fatal("unknown user should not be active")
	}
}

func TestWalletGetUser(t *testing.T) {
	w, _ := NewWallet("")
	// unknown
	if _, ok := w.GetUser(alice); ok {
		t.Fatal("unknown user should not be found")
	}
	// after register
	if _, err := w.RegisterUser(alice, ""); err != nil {
		t.Fatal(err)
	}
	u, ok := w.GetUser(alice)
	if !ok {
		t.Fatal("alice should be found after register")
	}
	if u.PayTo != strings.ToLower(alice) {
		t.Fatalf("default payTo: %q, want %q", u.PayTo, strings.ToLower(alice))
	}
	// explicit payTo
	if _, err := w.RegisterUser(bob, carol); err != nil {
		t.Fatal(err)
	}
	u2, _ := w.GetUser(bob)
	if u2.PayTo != strings.ToLower(carol) {
		t.Fatalf("explicit payTo: %q, want %q", u2.PayTo, strings.ToLower(carol))
	}
}

func TestNormalizeAddr(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"0xABCDEF1234567890ABCDEF1234567890ABCDEF12", "0xabcdef1234567890abcdef1234567890abcdef12", false},
		{"  0xABCDEF1234567890ABCDEF1234567890ABCDEF12  ", "0xabcdef1234567890abcdef1234567890abcdef12", false},
		{"0xZZZ", "", true},
		{"", "", true},
		{"0x1234", "", true},
	}
	for _, c := range cases {
		got, err := normalizeAddr(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeAddr(%q): err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("normalizeAddr(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

// Smoke test: time precision for key ordering.
func TestWalletKeyTimeOrdering(t *testing.T) {
	w, _ := NewWallet("")
	keys := []string{}
	for i := 0; i < 3; i++ {
		k, _, _ := w.GenerateKey(alice)
		keys = append(keys, k)
		time.Sleep(2 * time.Millisecond)
	}
	list := w.ListKeys(alice)
	if len(list) != 3 {
		t.Fatalf("ListKeys: %d, want 3", len(list))
	}
	// newest first
	if list[0].IssuedAt < list[1].IssuedAt {
		t.Errorf("expected newest first, got %s < %s", list[0].IssuedAt, list[1].IssuedAt)
	}
	_ = os.Getenv("PATH") // suppress unused
}
