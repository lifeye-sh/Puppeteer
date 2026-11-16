package guac

import "strconv"

// Quality affects only display negotiation; authorization and channels stay separate.
type Quality struct {
	Name                 string
	Width, Height, Depth int
	Smooth               bool
}

func DisplayQuality(name string) (Quality, bool) {
	switch name {
	case "": // Older clients retain their previous limits and appearance.
		return Quality{"legacy", 3840, 2160, 24, false}, true
	case "economy":
		return Quality{name, 1280, 720, 16, false}, true
	case "balanced":
		return Quality{name, 1920, 1080, 24, false}, true
	case "sharp":
		return Quality{name, 3840, 2160, 24, true}, true
	}
	return Quality{}, false
}

func (q Quality) Apply(values map[string]string) {
	values["color-depth"] = strconv.Itoa(q.Depth)
	values["enable-font-smoothing"] = strconv.FormatBool(q.Smooth)
	for _, key := range []string{"enable-wallpaper", "enable-theming", "enable-full-window-drag", "enable-menu-animations"} {
		values[key] = "false"
	}
}

// ClampSize preserves aspect ratio and aligns dimensions for RDP. InputAllowed
// must be checked first. The browser uses the same rule to avoid resize loops.
func (q Quality) ClampSize(width, height int) (int, int) {
	scale := min(1.0, float64(q.Width)/float64(width), float64(q.Height)/float64(height))
	return max(4, int(float64(width)*scale)/4*4), max(2, int(float64(height)*scale)/2*2)
}
