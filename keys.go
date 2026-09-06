// keys.go — per-user API key management.
//
// In multi-tenant mode, each user has their own keys. X-Payer is the
// user's `from` address (must be a valid address; if not yet
// registered, the user is auto-registered on first key request).
//
// Endpoints:
//   POST   /v1/keys              — generate a new key (X-Payer = user)
//   GET    /v1/keys              — list keys for X-Payer user
//   POST   /v1/keys/rotate       — revoke all X-Payer's keys + issue new
//   DELETE /v1/keys/{prefix}     — revoke a single key owned by X-Payer
package agw

import (
	"encoding/json"
	"net/http"
	"strings"
)

// requireUserPayer gates the management endpoints: X-Payer must be
// a valid EVM address. The user need not be registered yet (topup
// will auto-register), but key/rotate endpoints also auto-register
// defensively.
func (p *Proxy) requireUserPayer(w http.ResponseWriter, r *http.Request) string {
	xp := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Payer")))
	if xp == "" {
		writeAuthError(w, "X-Payer header required (your from address)")
		return ""
	}
	if _, err := normalizeAddr(xp); err != nil {
		writeAuthError(w, "invalid X-Payer: "+err.Error())
		return ""
	}
	return xp
}

// serveKeys dispatches GET (list) and POST (generate) for /v1/keys.
func (p *Proxy) serveKeys(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	user := p.requireUserPayer(w, r)
	if user == "" {
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.serveKeysList(w, r, user)
	case http.MethodPost:
		p.serveKeysGenerate(w, r, user)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveKeyByPrefix handles per-key operations keyed by the
// 8-char prefix. Currently supports DELETE for granular revoke.
// Only the key's owner (X-Payer) can revoke it.
func (p *Proxy) serveKeyByPrefix(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil {
		http.Error(w, "payments not enabled", http.StatusServiceUnavailable)
		return
	}
	user := p.requireUserPayer(w, r)
	if user == "" {
		return
	}
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
	// Restrict to the user's own keys.
	hash, err := p.Wallet.RevokeByPrefix(user, prefix)
	if err != nil {
		if err == ErrUnknownKey {
			http.Error(w, "no active key with that prefix for this user", http.StatusNotFound)
			return
		}
		http.Error(w, "revoke failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key revoked",
		"user", user, "prefix", prefix, "hash", hash[:16])
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"prefix":  prefix,
		"hash":    hash,
		"user":    user,
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
	user := p.requireUserPayer(w, r)
	if user == "" {
		return
	}
	plaintext, meta, err := p.Wallet.RotateKey(user)
	if err != nil {
		http.Error(w, "rotate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key rotated",
		"user", user, "prefix", meta.Prefix, "hash", meta.Hash[:16])

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"key":     plaintext,
		"meta":    meta,
		"warning": "this is the only time the plaintext key will be shown — store it now",
	})
}

func (p *Proxy) serveKeysGenerate(w http.ResponseWriter, r *http.Request, user string) {
	plaintext, meta, err := p.Wallet.GenerateKey(user)
	if err != nil {
		http.Error(w, "generate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.Logger.Info("api key issued", "user", user, "prefix", meta.Prefix, "hash", meta.Hash[:16])
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"key":     plaintext,
		"meta":    meta,
		"warning": "this is the only time the plaintext key will be shown — store it now",
	})
}

func (p *Proxy) serveKeysList(w http.ResponseWriter, r *http.Request, user string) {
	keys := p.Wallet.ListKeys(user)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user":  user,
		"count": len(keys),
		"items": keys,
	})
}
