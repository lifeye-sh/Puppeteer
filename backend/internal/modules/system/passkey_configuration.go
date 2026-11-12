package system

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/passkeyconfig"
)

type passkeyConfigurationProofKey struct{}

func WithPasskeyConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, passkeyConfigurationProofKey{}, true)
}
func hasPasskeyConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(passkeyConfigurationProofKey{}).(bool)
	return v
}

func PasskeyConfiguration(v ConfigurationValues) (string, []string, error) {
	rp := v.String("passkey-domain")
	var origins []string
	if json.Unmarshal([]byte(v.String("passkey-origins")), &origins) != nil || origins == nil {
		return "", nil, fault.Invalid
	}
	if rp == "" && len(origins) == 0 {
		return "", origins, nil
	}
	if e := passkeyconfig.Validate(rp, origins); e != nil {
		return "", nil, e
	}
	return rp, origins, nil
}
