package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/nativeclient"
	"sync"
)

// Only the application protocol test uses memory. Windows storage and
// cross-process serialization are verified separately against Credential Manager.
type r14MemoryCredential struct {
	mu         sync.Mutex
	data       []byte
	failWrites bool
}

func (s *r14MemoryCredential) WithLock(ctx context.Context, f func(nativeclient.SecretSlot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	return f(s)
}
func (s *r14MemoryCredential) Read() ([]byte, error) {
	if s.data == nil {
		return nil, nativeclient.ErrCredentialMissing
	}
	return append([]byte(nil), s.data...), nil
}
func (s *r14MemoryCredential) Write(b []byte) error {
	if s.failWrites {
		return errors.New("fixture credential store unavailable")
	}
	s.data = append([]byte(nil), b...)
	return nil
}
func (s *r14MemoryCredential) Delete() error { clear(s.data); s.data = nil; return nil }
