package systemupdate

import "errors"

// OperationError may contain only a fixed, sanitized host diagnostic. Ordinary
// subprocess/database errors are never copied into a public transaction journal.
type OperationError struct{ Reason string }

func (e *OperationError) Error() string { return e.Reason }
func operationReason(err error) string {
	var safe *OperationError
	if errors.As(err, &safe) && len(safe.Reason) <= 1000 {
		return "：" + safe.Reason
	}
	return ""
}
