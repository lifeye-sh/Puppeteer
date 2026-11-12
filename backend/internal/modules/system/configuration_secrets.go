package system

import (
	"github.com/google/uuid"
	"maps"
)

const SecretMask = "*****"

type SecretStatus struct {
	HasValue bool `json:"hasValue"`
}

func IsConfigurationSecret(key string) bool {
	for _, p := range PropertySchema() {
		if p.Key == key {
			return p.Type == "secret"
		}
	}
	return false
}
func validSecretReference(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	if s == "" {
		return true
	}
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}
func (v Revision) Redacted() Revision {
	v.Values = maps.Clone(v.Values)
	v.Secrets = map[string]SecretStatus{}
	for _, p := range PropertySchema() {
		if p.Type != "secret" {
			continue
		}
		value, exists := v.Values[p.Key]
		if !exists {
			continue
		}
		has := value != ""
		v.Secrets[p.Key] = SecretStatus{HasValue: has}
		if has {
			v.Values[p.Key] = SecretMask
		}
	}
	return v
}
func (v ConfigView) Redacted() ConfigView { v.Revision = v.Revision.Redacted(); return v }

func MaskConfigurationSecret(value any) any {
	if value == nil || value == "" {
		return value
	}
	return SecretMask
}
