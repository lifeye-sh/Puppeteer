package system

import "context"

type accessMFAProofKey struct{}

func IsAccessMFAConfigurationKey(key string) bool {
	return key == "access-require-mfa" || key == "access-mfa-expires-at"
}
func WithAccessMFAConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, accessMFAProofKey{}, true)
}
func hasAccessMFAConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(accessMFAProofKey{}).(bool)
	return v
}
