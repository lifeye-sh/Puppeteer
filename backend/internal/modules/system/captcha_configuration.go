package system

import "context"

const LoginCaptchaKey = "login-captcha-enabled"

type captchaConfigurationProofKey struct{}

func WithCaptchaConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, captchaConfigurationProofKey{}, true)
}
func hasCaptchaConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(captchaConfigurationProofKey{}).(bool)
	return v
}
