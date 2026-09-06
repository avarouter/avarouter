// keys.go — API key management endpoints (single-user mode).
//
// POST /v1/keys           — generate a new key, returns plaintext ONCE
// GET  /v1/keys           — list metadata for all keys (no plaintext)
// POST /v1/keys/rotate    — revoke all active keys, issue a new one
//
// Auth model: X-Payer header must equal the locked owner address
// (same as /v1/topup). In strict single-user mode this means only the
// owner can manage keys. The plaintext key is the bearer credential
// used on the hot path (Authorization: Bearer <key>).
package agw

import (
	"encoding/json"
	"net/http"
	"strings"
)

// serveKeys dispatches GET (list) and POST (generate) for /v1/keys.
func (p *Proxy) serveKeys(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if !p.requireOwnerPayer(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.serveKeysList(w, r)
	case http.MethodPost:
		p.serveKeysGenerate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveKeyByPrefix handles per-key operations keyed by the
// 8-char prefix (which is what the UI shows operators). Currently
// supports DELETE for granular revoke, distinct from the global
// /v1/keys/rotate which kills all keys at once.
func (p *Proxy) serveKeyByPrefix(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if !p.requireOwnerPayer(w, r) {
		return
	}
	// /v1/keys/{prefix} — strip the prefix
	prefix := strings.TrimPrefix(r.URL.Path, "/v1/keys/")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || strings.Contains(prefix, "/") {
		http.Error(w, "expected /v1/keys/{prefix}", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "method not allowed (use DELETE)", http.StatusMethodNotAllowed)
		return
	}
	hash, err := p.Wallet.RevokeByPrefix(prefix)
	if err != nil {
		if err == ErrUnknownKey {
			http.Error(w, "no active key with that prefix", http.StatusNotFound)
			return
		}
		http.Error(w, "revoke failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key revoked", "prefix", prefix, "hash", hash[:16])
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"prefix":  prefix,
		"hash":    hash,
		"message": "key revoked; subsequent uses will return 401",
	})
}

// serveKeysRotate handles POST /v1/keys/rotate.
func (p *Proxy) serveKeysRotate(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireOwnerPayer(w, r) {
		return
	}
	plaintext, meta, err := p.Wallet.RotateKey()
	if err != nil {
		http.Error(w, "rotate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key rotated",
		"prefix", meta.Prefix, "hash", meta.Hash[:16])

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"key":     plaintext,
		"meta":    meta,
		"warning": "this is the only time the plaintext key will be shown — store it now",
	})
}

func (p *Proxy) serveKeysList(w http.ResponseWriter, r *http.Request) {
	keys := p.Wallet.ListKeys()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": keys,
		"count": len(keys),
	})
}

func (p *Proxy) serveKeysGenerate(w http.ResponseWriter, r *http.Request) {
	plaintext, meta, err := p.Wallet.GenerateKey()
	if err != nil {
		http.Error(w, "generate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key issued",
		"prefix", meta.Prefix, "hash", meta.Hash[:16])

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"key":     plaintext,
		"meta":    meta,
		"warning": "this is the only time the plaintext key will be shown — store it now",
	})
}

// requireOwnerPayer gates single-user mode: only requests whose
// X-Payer matches the locked owner address are allowed past. On
// failure it writes a 401/403 response and returns false.
func (p *Proxy) requireOwnerPayer(w http.ResponseWriter, r *http.Request) bool {
	payer := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if payer == "" {
		http.Error(w, "X-Payer header required", http.StatusUnauthorized)
		return false
	}
	if payer != p.Wallet.Addr() {
		http.Error(w, "X-Payer does not match this server's owner", http.StatusForbidden)
		return false
	}
	return true
}
