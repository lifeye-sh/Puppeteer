package vault

// RotationResult describes evidence, not an assumption that disconnecting SSH
// rolled back a remote operation. Reconciliation may only probe credentials.
type RotationResult string

const (
	RotationPending   RotationResult = "pending"
	RotationApplying  RotationResult = "applying"
	RotationVerifying RotationResult = "verifying"
	RotationSucceeded RotationResult = "succeeded"
	RotationFailed    RotationResult = "failed"
	RotationUnknown   RotationResult = "unknown"
	RotationCancelled RotationResult = "cancelled"
)

// RotationTransition rejects replay of a remote write whose outcome is unknown.
func RotationTransition(from, to RotationResult) bool {
	switch from {
	case RotationPending:
		return to == RotationApplying || to == RotationFailed || to == RotationCancelled
	case RotationApplying:
		return to == RotationVerifying || to == RotationUnknown
	case RotationVerifying:
		return to == RotationSucceeded || to == RotationUnknown
	case RotationUnknown:
		// Only an authenticated probe can establish these terminal outcomes.
		return to == RotationSucceeded || to == RotationFailed
	default:
		return false
	}
}

func ReconciledRotation(candidateWorks, previousWorks bool) RotationResult {
	// Two working credentials can mean a key was added but the old key remains,
	// or the remote accepted authentication without consulting either secret.
	if candidateWorks && !previousWorks {
		return RotationSucceeded
	}
	if previousWorks && !candidateWorks {
		return RotationFailed
	}
	return RotationUnknown
}
