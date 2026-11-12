package system

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestLogoPixelAndByteLimits(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1025, 1))); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{nil, []byte("<svg/>"), b.Bytes(), make([]byte, 1024*1024+1)} {
		if _, err := SanitizeLogo(input); err == nil {
			t.Fatal("accepted invalid logo")
		}
	}
}
