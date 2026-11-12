package system

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// Domain consumers read the current revision for each new object. They never
// silently fall back to a default if a committed value is missing or malformed.
func (s *Service) NewStorageQuota(ctx context.Context) (int64, error) {
	v, e := s.Configuration(ctx, 0)
	if e != nil {
		return 0, e
	}
	n, ok := v.Values["user-default-storage-size"].(int)
	if !ok || n < 0 || int64(n) > 1<<40 {
		return 0, fault.Invalid
	}
	return int64(n), nil
}
func (s *Service) ClientCertificateDays(ctx context.Context) (int, error) {
	v, e := s.Configuration(ctx, 0)
	if e != nil {
		return 0, e
	}
	n, ok := v.Values["user-client-cert-valid-days"].(int)
	if !ok || n < 1 || n > 825 {
		return 0, fault.Invalid
	}
	return n, nil
}
