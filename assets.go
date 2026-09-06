package agw

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed favicon.ico icon-192.png icon-512.png manifest.json
var pwaAssets embed.FS

var pwaContentTypes = map[string]string{
	"/favicon.ico":   "image/x-icon",
	"/icon-192.png":  "image/png",
	"/icon-512.png":  "image/png",
	"/manifest.json": "application/manifest+json; charset=utf-8",
}

// servePWAFile serves the embedded PWA/OG assets. These are intentionally
// public (browsers and crawlers fetch them without management credentials).
func servePWAFile(w http.ResponseWriter, r *http.Request) {
	contentType, ok := pwaContentTypes[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := pwaAssets.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
