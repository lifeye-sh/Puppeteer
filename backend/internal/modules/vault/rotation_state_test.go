package vault

import "testing"

func TestRotationDoesNotReplayAmbiguousWrite(t *testing.T) {
	for _, from := range []RotationResult{RotationUnknown, RotationSucceeded, RotationFailed, RotationCancelled} {
		if RotationTransition(from, RotationApplying) {
			t.Fatalf("unsafe replay from %s", from)
		}
	}
	for _, from := range []RotationResult{RotationApplying, RotationVerifying} {
		if RotationTransition(from, RotationFailed) || RotationTransition(from, RotationCancelled) {
			t.Fatalf("lost result cannot establish failure/cancellation from %s", from)
		}
		if !RotationTransition(from, RotationUnknown) {
			t.Fatalf("missing uncertain outcome from %s", from)
		}
	}
}

func TestRotationReconcileRequiresExclusiveAuthenticationEvidence(t *testing.T) {
	for _, c := range []struct {
		candidate, previous bool
		want                RotationResult
	}{
		{true, false, RotationSucceeded}, {false, true, RotationFailed}, {true, true, RotationUnknown}, {false, false, RotationUnknown},
	} {
		if got := ReconciledRotation(c.candidate, c.previous); got != c.want {
			t.Fatalf("candidate=%t previous=%t: got %s want %s", c.candidate, c.previous, got, c.want)
		}
	}
}
