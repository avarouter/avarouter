package agw

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// helper: build & sign a manage request, return the headers map
func signedHeaders(t *testing.T, keyHex, payer, method, path string, body []byte) http.Header {
	t.Helper()
	ts := time.Now().Unix()
	priv, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	digest := HashManageMessage(method, path, body, ts, strings.ToLower(payer))
	sig, err := crypto.Sign(digest.Bytes(), priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	hdr := http.Header{}
	hdr.Set("X-Payer", strings.ToLower(payer))
	hdr.Set("X-AGW-Timestamp", itoa(ts))
	hdr.Set("X-AGW-Signature", "0x"+hexEncode(sig))
	return hdr
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// (no discardLogger; using silentLogger from manage_auth_helpers_test.go)

func TestVerifyManagementSignatureOK(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	priv := privFromHex("4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6")
	wantAddr := strings.ToLower(crypto.PubkeyToAddress(priv.PublicKey).Hex())

	hdr := signedHeaders(t, hexKey(priv), wantAddr, "POST", "/v1/keys", []byte(`{}`))

	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	r.Header = hdr
	w2 := httptest.NewRecorder()
	user := p.verifyManagementSignature(w2, r, true)
	if user != wantAddr {
		t.Fatalf("got user %q, want %q (response: %s)", user, wantAddr, w2.Body.String())
	}
}

func TestVerifyManagementSignatureRejectsTamperedBody(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	priv := privFromHex("4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6")
	wantAddr := strings.ToLower(crypto.PubkeyToAddress(priv.PublicKey).Hex())

	// Sign over body A, send body B.
	bodyA := []byte(`{"hello":"world"}`)
	bodyB := []byte(`{"hello":"WORLD"}`)
	hdr := signedHeaders(t, hexKey(priv), wantAddr, "POST", "/v1/keys", bodyA)
	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader(bodyB))
	r.Header = hdr
	w2 := httptest.NewRecorder()
	user := p.verifyManagementSignature(w2, r, true)
	if user != "" {
		t.Fatalf("tampered body should be rejected, got user %q", user)
	}
	if !strings.Contains(w2.Body.String(), "signature does not match") &&
		!strings.Contains(w2.Body.String(), "ecrecover") {
		t.Fatalf("expected signature failure, got: %s", w2.Body.String())
	}
}

func TestVerifyManagementSignatureRejectsExpiredTimestamp(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	priv := privFromHex("4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6")
	wantAddr := strings.ToLower(crypto.PubkeyToAddress(priv.PublicKey).Hex())

	oldTS := time.Now().Add(-10 * time.Minute).Unix()
	digest := HashManageMessage("POST", "/v1/keys", []byte(`{}`), oldTS, wantAddr)
	sig, _ := crypto.Sign(digest.Bytes(), priv)
	if sig[64] < 27 {
		sig[64] += 27
	}
	hdr := http.Header{}
	hdr.Set("X-Payer", wantAddr)
	hdr.Set("X-AGW-Timestamp", itoa(oldTS))
	hdr.Set("X-AGW-Signature", "0x"+hexEncode(sig))
	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	r.Header = hdr
	w2 := httptest.NewRecorder()
	user := p.verifyManagementSignature(w2, r, true)
	if user != "" {
		t.Fatalf("expired timestamp should be rejected, got user %q", user)
	}
	if !strings.Contains(w2.Body.String(), "out of window") {
		t.Fatalf("expected out-of-window error, got: %s", w2.Body.String())
	}
}

func TestVerifyManagementSignatureRejectsCrossUser(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	// bob signs, but X-Payer claims alice — should be rejected.
	bobPriv := privFromHex("20e790bcec70cda7fd671f7d3b779722fd709cd73a8c15b77c77cca71a026f43")
	aliceAddr := strings.ToLower("0xabcdef1234567890abcdef1234567890abcdef12")
	hdr := signedHeaders(t, hexKey(bobPriv), aliceAddr, "POST", "/v1/keys", []byte(`{}`))
	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	r.Header = hdr
	w2 := httptest.NewRecorder()
	user := p.verifyManagementSignature(w2, r, true)
	if user != "" {
		t.Fatalf("cross-user forgery should be rejected, got user %q", user)
	}
	if !strings.Contains(w2.Body.String(), "signature does not match") {
		t.Fatalf("expected mismatch error, got: %s", w2.Body.String())
	}
}

func TestVerifyManagementSignatureRejectsUnknownUser(t *testing.T) {
	w, _ := NewWallet("")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	priv := privFromHex("4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6")
	addr := strings.ToLower("0x" + crypto.PubkeyToAddress(priv.PublicKey).Hex()[2:])
	// addr is registered? no. Should reject when requireRegistered=true
	hdr := signedHeaders(t, hexKey(priv), addr, "POST", "/v1/keys", []byte(`{}`))
	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	r.Header = hdr
	w2 := httptest.NewRecorder()
	user := p.verifyManagementSignature(w2, r, true)
	if user != "" {
		t.Fatalf("unknown user should be rejected, got %q", user)
	}
	if !strings.Contains(w2.Body.String(), "not registered") {
		t.Fatalf("expected not-registered error, got: %s", w2.Body.String())
	}

	// But accept when requireRegistered=false (topup first call)
	w3 := httptest.NewRecorder()
	user = p.verifyManagementSignature(w3, r, false)
	if user != addr {
		t.Fatalf("topup 1st call should accept unknown user; got %q (resp: %s)", user, w3.Body.String())
	}
}

func TestVerifyManagementSignatureMissingHeader(t *testing.T) {
	w, _ := NewWallet("")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"X-Payer", "X-Payer", ""},
		{"X-AGW-Timestamp", "X-AGW-Timestamp", ""},
		{"X-AGW-Signature", "X-AGW-Signature", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr := http.Header{}
			if c.name != "X-Payer" {
				hdr.Set("X-Payer", "0xabcdef1234567890abcdef1234567890abcdef12")
			}
			if c.name != "X-AGW-Timestamp" {
				hdr.Set("X-AGW-Timestamp", itoa(time.Now().Unix()))
			}
			if c.name != "X-AGW-Signature" {
				hdr.Set("X-AGW-Signature", "0x"+strings.Repeat("00", 65))
			}
			r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
			r.Header = hdr
			w2 := httptest.NewRecorder()
			user := p.verifyManagementSignature(w2, r, true)
			if user != "" {
				t.Fatalf("missing %s should be rejected, got %q", c.name, user)
			}
		})
	}
}

// Smoke: the manage-auth path on a real /v1/keys request via the
// full http handler.
func TestKeysEndpointRequiresSignature(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	// without headers → 401
	r := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	w2 := httptest.NewRecorder()
	p.serveKeys(w2, r)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without headers, got %d (%s)", w2.Code, w2.Body.String())
	}
	// with headers → 200
	hdr := signedHeaders(t,
		"4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6",
		strings.ToLower(alice), "POST", "/v1/keys", []byte(`{}`))
	r2 := httptest.NewRequest("POST", "/v1/keys", bytes.NewReader([]byte(`{}`)))
	r2.Header = hdr
	w3 := httptest.NewRecorder()
	p.serveKeys(w3, r2)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid sig, got %d (%s)", w3.Code, w3.Body.String())
	}
	var resp struct {
		OK   bool   `json:"ok"`
		Key  string `json:"key"`
		Meta struct {
			Hash   string `json:"hash"`
			Prefix string `json:"prefix"`
			Owner  string `json:"owner"`
		} `json:"meta"`
	}
	body, _ := io.ReadAll(w3.Body)
	json.Unmarshal(body, &resp)
	if !resp.OK || resp.Key == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.HasPrefix(resp.Key, APIKeyPrefix) {
		t.Fatalf("key missing prefix: %q", resp.Key)
	}
	if !common.IsHexAddress(resp.Meta.Owner) {
		t.Fatalf("meta.owner not a valid address: %q", resp.Meta.Owner)
	}
}

func TestRotateEndpointRequiresSignature(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	// no headers → 401
	r := httptest.NewRequest("POST", "/v1/keys/rotate", nil)
	w2 := httptest.NewRecorder()
	p.serveKeysRotate(w2, r)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w2.Code)
	}

	// with sig → 200
	hdr := signedHeaders(t,
		"4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6",
		strings.ToLower(alice), "POST", "/v1/keys/rotate", nil)
	r2 := httptest.NewRequest("POST", "/v1/keys/rotate", nil)
	r2.Header = hdr
	w3 := httptest.NewRecorder()
	p.serveKeysRotate(w3, r2)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w3.Code, w3.Body.String())
	}
}

func TestRevokeByPrefixRequiresSignature(t *testing.T) {
	w, _ := NewWallet("")
	w.Credit(alice, 1000, "test", "")
	p := &Proxy{Wallet: w, Logger: silentLogger()}

	// mint a key first
	_, meta, _ := w.GenerateKey(alice)

	// no headers → 401
	r := httptest.NewRequest("DELETE", "/v1/keys/"+meta.Prefix, nil)
	w2 := httptest.NewRecorder()
	p.serveKeyByPrefix(w2, r)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w2.Code)
	}

	// with sig from BOB trying to revoke Alice's key → 401 (sig check)
	bobHdr := signedHeaders(t,
		"20e790bcec70cda7fd671f7d3b779722fd709cd73a8c15b77c77cca71a026f43",
		strings.ToLower(bob), "DELETE", "/v1/keys/"+meta.Prefix, nil)
	r2 := httptest.NewRequest("DELETE", "/v1/keys/"+meta.Prefix, nil)
	r2.Header = bobHdr
	w3 := httptest.NewRecorder()
	p.serveKeyByPrefix(w3, r2)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("bob should not pass sig check, got %d (%s)", w3.Code, w3.Body.String())
	}

	// with sig from ALICE → 200
	aliceHdr := signedHeaders(t,
		"4caaf54b1f52fc5940a87be6198229afb450e91d4c2703581ca8d028e66fbce6",
		strings.ToLower(alice), "DELETE", "/v1/keys/"+meta.Prefix, nil)
	r3 := httptest.NewRequest("DELETE", "/v1/keys/"+meta.Prefix, nil)
	r3.Header = aliceHdr
	w4 := httptest.NewRecorder()
	p.serveKeyByPrefix(w4, r3)
	if w4.Code != http.StatusOK {
		t.Fatalf("alice revoke should succeed, got %d (%s)", w4.Code, w4.Body.String())
	}
}
