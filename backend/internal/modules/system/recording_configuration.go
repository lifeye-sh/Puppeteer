package system

import "context"

type recordingProofKey struct{}

func WithRecordingConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, recordingProofKey{}, true)
}
func hasRecordingConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(recordingProofKey{}).(bool)
	return v
}
