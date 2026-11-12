package system

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"strconv"
	"strings"
)

// Watermark is presentation metadata, never a substitute for access control.
// The values are captured at reservation time, not read again by the browser.
type Watermark struct {
	Enabled  bool     `json:"enabled"`
	Content  []string `json:"content"`
	Color    string   `json:"color"`
	Size     int      `json:"size"`
	Revision int64    `json:"revision"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)
var rgbColor = regexp.MustCompile(`^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})(?:\s*,\s*(0(?:\.\d{1,4})?|1(?:\.0{1,4})?))?\s*\)$`)

func validWatermarkColor(value string) bool {
	if hexColor.MatchString(value) {
		return true
	}
	m := rgbColor.FindStringSubmatch(value)
	if m == nil || (strings.HasPrefix(value, "rgba(") != (m[4] != "")) {
		return false
	}
	for _, text := range m[1:4] {
		n, _ := strconv.Atoi(text)
		if n > 255 {
			return false
		}
	}
	return true
}

func validateWatermark(v ConfigurationValues) error {
	if !validWatermarkColor(v.String("watermark-font-color")) {
		return fault.Invalid
	}
	text := v.String("watermark-content")
	for _, variable := range []string{"{user}", "{assetUser}", "{asset}"} {
		text = strings.ReplaceAll(text, variable, "")
	}
	if strings.ContainsAny(text, "{}<>") {
		return fault.Invalid
	}
	if v.Bool("watermark-enabled") && strings.TrimSpace(v.String("watermark-content")) == "" && !v.Bool("watermark-content-user-account") && !v.Bool("watermark-content-asset-username") {
		return fault.Invalid
	}
	return nil
}

func (s *Service) SessionWatermark(ctx context.Context, user, assetUser, asset string) (Watermark, error) {
	r, e := s.Configuration(ctx, 0)
	if e != nil {
		return Watermark{}, e
	}
	if e = ValidateConfiguration(r.Values); e != nil {
		return Watermark{}, e
	}
	v := r.Values
	w := Watermark{Enabled: v.Bool("watermark-enabled"), Content: []string{}, Color: v.String("watermark-font-color"), Size: v.Int("watermark-font-size"), Revision: r.ID}
	if !w.Enabled {
		return w, nil
	}
	text := strings.NewReplacer("{user}", user, "{assetUser}", assetUser, "{asset}", asset).Replace(v.String("watermark-content"))
	if strings.TrimSpace(text) != "" {
		w.Content = append(w.Content, text)
	}
	if v.Bool("watermark-content-user-account") {
		w.Content = append(w.Content, "用户："+user)
	}
	if v.Bool("watermark-content-asset-username") && assetUser != "" {
		w.Content = append(w.Content, "资产账户："+assetUser)
	}
	return w, nil
}
