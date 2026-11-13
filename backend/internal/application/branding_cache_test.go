package application

import (
	"bytes"
	"image/png"
	"net/http/httptest"
	"testing"
)

func TestDefaultBrandLogoTransferBudget(t *testing.T) {
	config, err := png.DecodeConfig(bytes.NewReader(defaultBrandLogo))
	if err != nil || config.Width != 128 || config.Height != 128 || len(defaultBrandLogo) > 65536 {
		t.Fatalf("invalid or oversized UI logo: %+v, %d bytes, %v", config, len(defaultBrandLogo), err)
	}
}

func TestBrandLogoConditionalCache(t *testing.T) {
	first := httptest.NewRecorder()
	serveBrandLogo(first, httptest.NewRequest("GET", "/api/logo", nil), []byte("image-one"))
	tag := first.Header().Get("ETag")
	if first.Code != 200 || tag == "" || first.Header().Get("Cache-Control") != "public, no-cache" {
		t.Fatal("missing revalidation headers", first.Result())
	}
	request := httptest.NewRequest("GET", "/api/logo", nil)
	request.Header.Set("If-None-Match", tag)
	cached := httptest.NewRecorder()
	serveBrandLogo(cached, request, []byte("image-one"))
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("unchanged logo was downloaded again", cached.Code)
	}
	changed := httptest.NewRecorder()
	serveBrandLogo(changed, request, []byte("image-two"))
	if changed.Code != 200 || changed.Body.String() != "image-two" || changed.Header().Get("ETag") == tag {
		t.Fatal("updated logo was cached incorrectly")
	}
}
