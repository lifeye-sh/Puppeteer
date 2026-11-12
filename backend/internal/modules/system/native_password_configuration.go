package system

import "context"

const NativePasswordDisabledKey = "ssh-server-disable-password-auth"

type nativePasswordProofKey struct{}

func WithNativePasswordConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativePasswordProofKey{}, true)
}
func hasNativePasswordConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(nativePasswordProofKey{}).(bool)
	return v
}
