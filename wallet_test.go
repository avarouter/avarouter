// wallet_test.go — covers the simplified single-user wallet: credit /
// debit / balance / key generation / lookup / rotation / persistence.
package agw

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWalletCreditDebit(t *testing.T) {
	w, err := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Credit(5_000_000, "test", ""); err != nil {
		t.Fatal(err)
	}
	bal, _ := w.Balance()
	if bal != 5_000_000 {
		t.Fatalf("balance after credit: %d, want 5M", bal)
	}
	if _, err := w.Debit(2_000_000, "spend"); err != nil {
		t.Fatal(err)
	}
	bal, _ = w.Balance()
	if bal != 3_000_000 {
		t.Fatalf("balance after debit: %d, want 3M", bal)
	}
}

func TestWalletDebitRejectsNonPositive(t *testing.T) {
	w, _ := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if _, err := w.Debit(0, "x"); err == nil {
		t.Fatal("Debit(0) should error")
	}
	if _, err := w.Debit(-1, "x"); err == nil {
		t.Fatal("Debit(-1) should error")
	}
}

func TestWalletCreditRejectsNonPositive(t *testing.T) {
	w, _ := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if _, err := w.Credit(0, "x", ""); err == nil {
		t.Fatal("Credit(0) should error")
	}
}

func TestWalletKeyGenerateLookupRotate(t *testing.T) {
	w, _ := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	plain, meta, err := w.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, APIKeyPrefix) {
		t.Fatalf("key missing prefix: %q", plain)
	}
	if !meta.Active {
		t.Fatal("newly issued key should be active")
	}
	addr, err := w.LookupKey(plain)
	if err != nil || addr != "0xabcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("LookupKey: addr=%q err=%v", addr, err)
	}

	// Rotate: old key should be revoked, new key works.
	plain2, _, err := w.RotateKey()
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
	w, _ := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if _, err := w.LookupKey(""); err != ErrUnknownKey {
		t.Fatalf("empty key: err=%v, want ErrUnknownKey", err)
	}
	if _, err := w.LookupKey("agw_notreallyrandom"); err != ErrUnknownKey {
		t.Fatalf("bogus key: err=%v, want ErrUnknownKey", err)
	}
}

func TestWalletListKeys(t *testing.T) {
	w, _ := NewWallet("", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	plain1, _, _ := w.GenerateKey()
	_, _, _ = w.RotateKey() // revokes plain1, issues new (active)
	plain3, _, _ := w.GenerateKey()

	keys := w.ListKeys()
	if len(keys) != 3 {
		t.Fatalf("ListKeys: %d keys, want 3", len(keys))
	}
	// plain1 is revoked by the RotateKey; rotated key and plain3 are active
	active := 0
	for _, k := range keys {
		if k.Active {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("expected 2 active keys, got %d", active)
	}
	_ = plain1
	_ = plain3
}

func TestWalletPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.jsonl")
	owner := "0x2222222222222222222222222222222222222222"

	w1, err := NewWallet(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Credit(7_000_000, "first", "0xabc"); err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Debit(2_000_000, "spend"); err != nil {
		t.Fatal(err)
	}
	plain, _, _ := w1.GenerateKey()
	w1.Close()

	// Reload
	w2, err := NewWallet(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	bal, _ := w2.Balance()
	if bal != 5_000_000 {
		t.Fatalf("reloaded balance: %d, want 5M", bal)
	}
	if _, err := w2.LookupKey(plain); err != nil {
		t.Fatalf("reloaded key invalid: %v", err)
	}
}

func TestWalletRejectsInvalidOwner(t *testing.T) {
	if _, err := NewWallet("", ""); err == nil {
		t.Fatal("empty owner should error")
	}
	if _, err := NewWallet("", "notanaddress"); err == nil {
		t.Fatal("invalid owner should error")
	}
}

func TestNormalizeAddr(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"0xABCDEF1234567890ABCDEF1234567890ABCDEF12", "0xabcdef1234567890abcdef1234567890abcdef12", false},
		{" 0xAbCdEf1234567890ABCDEF1234567890ABCDEF12 ", "0xabcdef1234567890abcdef1234567890abcdef12", false},
		{"notanaddress", "", true},
		{"0x1234", "", true},
		{"0xGGGGGG1234567890ABCDEF1234567890ABCDEF12", "", true},
	}
	for _, c := range cases {
		got, err := normalizeAddr(c.in)
		if c.err {
			if err == nil {
				t.Errorf("normalizeAddr(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeAddr(%q): unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeAddr(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePositiveInt(t *testing.T) {
	if n, ok := parsePositiveInt("12345"); !ok || n != 12345 {
		t.Errorf("parsePositiveInt(12345): got %d, ok=%v", n, ok)
	}
	if _, ok := parsePositiveInt("0"); ok {
		t.Error("parsePositiveInt(0): should reject zero")
	}
	if _, ok := parsePositiveInt("-1"); ok {
		t.Error("parsePositiveInt(-1): should reject negative")
	}
	if _, ok := parsePositiveInt("12a"); ok {
		t.Error("parsePositiveInt(12a): should reject non-digit")
	}
	if _, ok := parsePositiveInt(""); ok {
		t.Error("parsePositiveInt(empty): should reject empty")
	}
}

func TestFormatInt(t *testing.T) {
	if formatInt(0) != "0" {
		t.Errorf("formatInt(0): %q", formatInt(0))
	}
	if formatInt(1_000_000) != "1000000" {
		t.Errorf("formatInt(1M): %q", formatInt(1_000_000))
	}
}
