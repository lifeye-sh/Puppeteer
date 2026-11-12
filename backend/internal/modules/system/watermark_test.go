package system

import "testing"

func TestWatermarkColorIsBoundedLiteral(t *testing.T) {
	for _, value := range []string{"#AABBcc", "#aabbcc33", "rgb(0, 128,255)", "rgba(128, 128, 128, 0.3)", "rgba(0,0,0,0)", "rgba(255,255,255,1.000)"} {
		if !validWatermarkColor(value) {
			t.Errorf("rejected %q", value)
		}
	}
	for _, value := range []string{"red", "#123", "url(https://invalid)", "rgba(256,0,0,1)", "rgba(-1,0,0,1)", "rgba(0,0,0,1.01)", "rgba(0,0,0)", "rgb(0,0,0,0.3)", "rgba(0,0,0,NaN)", "#aabbcc;display:none"} {
		if validWatermarkColor(value) {
			t.Errorf("accepted %q", value)
		}
	}
}
