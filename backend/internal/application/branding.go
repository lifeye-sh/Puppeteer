package application

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"time"
)

//go:embed branding/puppeteer-128.png
var defaultBrandLogo []byte

// Revalidate branding, but reuse the browser's bytes when the image is unchanged.
// A version query is not assumed immutable: the endpoint serves current branding.
func serveBrandLogo(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "logo.png", time.Time{}, bytes.NewReader(data))
}
