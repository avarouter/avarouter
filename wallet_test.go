// wallet_test.go — exercises the balance ledger's hold/settle/refund
// state machine and JSONL persistence. Uses an in-memory wallet (no
// logPath) to keep the tests hermetic.
package agw

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWalletCreditDebit(t *testing.T) {
	w, err := NewWallet("")
	if err != nil {
		t.Fatal(err)
	}
	addr := "0xABCDEF1234567890ABCDEF1234567890ABCDEF12"
	if _, err := w.Credit(addr, 5_000_000, "test", ""); err != nil {
		t.Fatal(err)
	}
	bal, err := w.Balance(addr)
	if err != nil || bal != 5_000_000 {
		t.Fatalf("balance: got %d, err %v", bal, err)
	}
	snap, _ := w.Snapshot(addr)
	if snap.Balance != 5_000_000 || snap.Held != 0 {
		t.Fatalf("snapshot: %+v", snap)
	}
}

func TestWalletHoldSettleRefund(t *testing.T) {
	w, _ := NewWallet("")
	addr := "0x1111111111111111111111111111111111111111"
	if _, err := w.Credit(addr, 10_000_000, "", ""); err != nil {
		t.Fatal(err)
	}
	// Hold 4 USDC
	holdID, err := w.Hold(addr, 4_000_000)
	if err != nil {
		t.Fatal(err)
	}
	bal, _ := w.Balance(addr)
	if bal != 6_000_000 {
		t.Fatalf("after hold: balance=%d, want 6M", bal)
	}
	snap, _ := w.Snapshot(addr)
	if snap.Held != 4_000_000 {
		t.Fatalf("held=%d, want 4M", snap.Held)
	}
	// Settle 2 USDC → release 2
	if err := w.Settle(holdID, 2_000_000); err != nil {
		t.Fatal(err)
	}
	bal, _ = w.Balance(addr)
	if bal != 8_000_000 {
		t.Fatalf("after settle: balance=%d, want 8M", bal)
	}
	// Refund the rest
	holdID2, _ := w.Hold(addr, 1_000_000)
	if err := w.Refund(holdID2); err != nil {
		t.Fatal(err)
	}
	bal, _ = w.Balance(addr)
	if bal != 8_000_000 {
		t.Fatalf("after refund: balance=%d, want 8M", bal)
	}
}

func TestWalletHoldUnknownAddress(t *testing.T) {
	w, _ := NewWallet("")
	_, err := w.Hold("0xDEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEF", 1_000_000)
	if err != ErrUnknownAddress {
		t.Fatalf("want ErrUnknownAddress, got %v", err)
	}
}

func TestWalletSettleUnknownHold(t *testing.T) {
	w, _ := NewWallet("")
	if err := w.Settle("h_doesnotexist", 0); err != ErrUnknownHold {
		t.Fatalf("want ErrUnknownHold, got %v", err)
	}
}

func TestWalletPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "balances.jsonl")
	addr := "0x2222222222222222222222222222222222222222"

	w1, err := NewWallet(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Credit(addr, 3_000_000, "first", ""); err != nil {
		t.Fatal(err)
	}
	holdID, _ := w1.Hold(addr, 1_000_000)
	w1.Settle(holdID, 500_000) // net: +3M -0.5M = 2.5M
	w1.Close()

	// Reload
	w2, err := NewWallet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	bal, _ := w2.Balance(addr)
	if bal != 2_500_000 {
		t.Fatalf("reloaded balance: got %d, want 2.5M", bal)
	}

	// Verify the log file is valid JSONL
	data, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) < 3 {
		t.Fatalf("expected ≥3 log lines, got %d", len(lines))
	}
	for _, line := range lines {
		var e WalletEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("bad line: %s (%v)", line, err)
		}
	}
}

func TestWalletConcurrent(t *testing.T) {
	w, _ := NewWallet("")
	addr := "0x3333333333333333333333333333333333333333"
	w.Credit(addr, 100_000_000, "", "") // 100 USDC

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hold, err := w.Hold(addr, 1_000_000)
			if err != nil {
				return
			}
			w.Settle(hold, 1_000_000)
		}()
	}
	wg.Wait()
	bal, _ := w.Balance(addr)
	if bal != 50_000_000 {
		t.Fatalf("after 50 holds+settles of 1M each from 100M: got %d, want 50M", bal)
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
	cases := []struct{ in int64; want string }{
		{0, "0"},
		{1, "1"},
		{1000000, "1000000"},
		{1_000_000_000, "1000000000"},
	}
	for _, c := range cases {
		if got := formatInt(c.in); got != c.want {
			t.Errorf("formatInt(%d): got %q, want %q", c.in, got, c.want)
		}
	}
}

// Sanity check: make sure PaymentRequirements JSON contains the
// expected x402-V2 fields.
func TestPaymentRequirementsShape(t *testing.T) {
	u, err := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, nonce, err := u.BuildRequirements("/v1/topup", "desc", "0xRecipient", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if nonce == [32]byte{} {
		t.Fatal("nonce must be non-zero")
	}
	if req.Scheme != "exact" {
		t.Errorf("scheme: got %q, want exact", req.Scheme)
	}
	if !strings.HasPrefix(req.Network, "eip155:") {
		t.Errorf("network: got %q, want eip155:*", req.Network)
	}
	if req.MaxAmountRequired != "1000000" {
		t.Errorf("amount: got %q, want 1000000", req.MaxAmountRequired)
	}
}
