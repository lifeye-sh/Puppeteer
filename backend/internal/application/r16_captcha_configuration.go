package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/system"
)

func (a *App) loginCaptchaEnabled(ctx context.Context) (bool, error) {
	// The production entry point always requires the configured policy. False is
	// retained solely for explicit in-process protocol/browser test fixtures.
	if !a.config.CaptchaRequired {
		return false, nil
	}
	v, e := a.system.SecurityConfiguration(ctx)
	if e != nil {
		return true, e
	}
	return v.Values.Bool(system.LoginCaptchaKey), nil
}
func (a *App) checkLoginCaptcha(r *http.Request, key string) error {
	enabled, e := a.loginCaptchaEnabled(r.Context())
	if e != nil {
		return e
	}
	if enabled {
		return a.identity.ConsumeCaptcha(r.Context(), key, captchaBinding(r))
	}
	return nil
}
