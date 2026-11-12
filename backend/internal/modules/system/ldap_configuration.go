package system

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/ldapconfig"
)

const DefaultLDAPMapping = `{"username":"uid","nickname":"cn","mail":"mail"}`

type ldapConfigurationProofKey struct{}

func WithLDAPConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, ldapConfigurationProofKey{}, true)
}
func hasLDAPConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(ldapConfigurationProofKey{}).(bool)
	return v
}

func ldapConfigurationProvider(v ConfigurationValues) (ldapconfig.Provider, error) {
	var mapping ldapconfig.Mapping
	d := json.NewDecoder(bytes.NewBufferString(v.String("ldap-user-property-mapping")))
	d.DisallowUnknownFields()
	if d.Decode(&mapping) != nil || d.Decode(new(any)) != io.EOF || ldapconfig.ValidateMapping(mapping) != nil {
		return ldapconfig.Provider{}, fault.Invalid
	}
	size := v.Int("ldap-user-search-size-limit")
	return ldapconfig.Provider{Name: "平台默认 LDAP", URL: v.String("ldap-url"), BaseDN: v.String("ldap-base-dn"), BindDN: v.String("ldap-user"), SearchFilter: v.String("ldap-user-search-filter"), LoginAttribute: mapping.Username, IdentityAttribute: v.String("ldap-identity-attribute"), CAPEM: v.String("ldap-ca-pem"), SearchSizeLimit: &size, PropertyMapping: &mapping, Enabled: v.Bool("ldap-enabled"), BindingEnabled: v.Bool("ldap-binding-enabled")}, nil
}

func validateLDAPConfiguration(v ConfigurationValues) error {
	p, e := ldapConfigurationProvider(v)
	if e != nil {
		return e
	}
	// Validate optional configuration even while disabled. An entirely empty
	// connection uses placeholders solely for shared syntax validation.
	if p.URL == "" && p.BaseDN == "" && p.BindDN == "" && !p.Enabled && !p.BindingEnabled && v.String("ldap-password") == "" {
		p.URL = "ldaps://ldap.example.invalid:636"
		p.BaseDN = "dc=example,dc=invalid"
		p.BindDN = "cn=service,dc=example,dc=invalid"
	}
	if ldapconfig.Validate(p) != nil || ((p.Enabled || p.BindingEnabled) && v.String("ldap-password") == "") {
		return fault.Invalid
	}
	return nil
}

// Identity namespaces are immutable once created. A different directory must
// have a distinct provider ID; existing stable subjects may never be rebound.
func (s *Service) applyLDAPConfiguration(ctx context.Context, v ConfigurationValues) error {
	p, e := ldapConfigurationProvider(v)
	if e != nil {
		return e
	}
	var exists bool
	if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM ldap_identity_providers WHERE settings_managed)`).Scan(&exists); e != nil {
		return e
	}
	if !exists && p.URL == "" {
		return nil
	}
	mapping, _ := json.Marshal(p.PropertyMapping)
	if !exists {
		_, e = s.db.Exec(ctx, `INSERT INTO ldap_identity_providers(name,url,base_dn,bind_dn,search_filter,login_attribute,identity_attribute,ca_pem,search_size_limit,property_mapping,settings_managed,configuration_secret_id,enabled,binding_enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,NULLIF($11,'')::uuid,false,false)`, p.Name, p.URL, p.BaseDN, p.BindDN, p.SearchFilter, p.LoginAttribute, p.IdentityAttribute, p.CAPEM, p.DirectoryLimit(), mapping, v.String("ldap-password"))
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE ldap_identity_providers SET bind_dn=$1,search_filter=$2,login_attribute=$3,ca_pem=$4,search_size_limit=$5,property_mapping=$6,configuration_secret_id=NULLIF($7,'')::uuid,version=version+1 WHERE settings_managed AND url=$8 AND base_dn=$9 AND identity_attribute=$10`, p.BindDN, p.SearchFilter, p.LoginAttribute, p.CAPEM, p.DirectoryLimit(), mapping, v.String("ldap-password"), p.URL, p.BaseDN, p.IdentityAttribute)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

func (s *Service) enforceLDAPConfiguration(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE ldap_identity_providers p SET version=p.version+1,enabled=(s.enforced->>'ldap-enabled')::boolean,binding_enabled=(s.enforced->>'ldap-binding-enabled')::boolean FROM configuration_security s WHERE s.singleton=1 AND p.settings_managed AND (p.enabled IS DISTINCT FROM (s.enforced->>'ldap-enabled')::boolean OR p.binding_enabled IS DISTINCT FROM (s.enforced->>'ldap-binding-enabled')::boolean)`)
	return e
}
