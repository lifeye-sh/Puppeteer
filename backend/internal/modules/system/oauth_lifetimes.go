package system

import "context"

func IsOAuthLifetimeKey(key string) bool {
	switch key {
	case "oidc-server-access-token-ttl", "oidc-server-refresh-token-ttl", "oidc-server-id-token-ttl", "oidc-server-auth-code-ttl":
		return true
	}
	return false
}

type oauthLifetimeProofKey struct{}

func WithOAuthLifetimeProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauthLifetimeProofKey{}, true)
}

func hasOAuthLifetimeProof(ctx context.Context) bool {
	v, _ := ctx.Value(oauthLifetimeProofKey{}).(bool)
	return v
}
