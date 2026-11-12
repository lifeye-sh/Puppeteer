package system

import "context"

type loginPolicyProofKey struct{}

// Only trusted domain/application code may grant this after password/MFA and
// alternate-login validation, or the explicit local recovery command. Generic
// settings/CLI writes cannot silently bypass the domain's anti-lockout checks.
func WithLoginPolicyProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, loginPolicyProofKey{}, true)
}

func hasLoginPolicyProof(ctx context.Context) bool {
	v, _ := ctx.Value(loginPolicyProofKey{}).(bool)
	return v
}
