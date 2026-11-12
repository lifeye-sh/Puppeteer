package identity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
)

func (s *Service) LDAPProviders(ctx context.Context) ([]LDAPProvider, error) {
	rows, err := s.db.Rows(ctx, `SELECT id,name,url,base_dn,bind_dn,search_filter,login_attribute,identity_attribute,ca_pem,(secret_sealed IS NOT NULL OR configuration_secret_id IS NOT NULL),version,enabled,binding_enabled,search_size_limit,property_mapping,settings_managed FROM ldap_identity_providers ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LDAPProvider{}
	for rows.Next() {
		var p LDAPProvider
		var mapping []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.URL, &p.BaseDN, &p.BindDN, &p.SearchFilter, &p.LoginAttribute, &p.IdentityAttribute, &p.CAPEM, &p.HasSecret, &p.Version, &p.Enabled, &p.BindingEnabled, &p.SearchSizeLimit, &mapping, &p.Managed); err != nil {
			return nil, err
		}
		if len(mapping) > 0 {
			if err = json.Unmarshal(mapping, &p.PropertyMapping); err != nil {
				return nil, err
			}
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
func (s *Service) LDAPProvider(ctx context.Context, id string) (LDAPProvider, error) {
	var p LDAPProvider
	var mapping []byte
	err := s.db.Row(ctx, `SELECT id,name,url,base_dn,bind_dn,search_filter,login_attribute,identity_attribute,ca_pem,(secret_sealed IS NOT NULL OR configuration_secret_id IS NOT NULL),version,enabled,binding_enabled,search_size_limit,property_mapping,settings_managed FROM ldap_identity_providers WHERE id=$1`, id).Scan(&p.ID, &p.Name, &p.URL, &p.BaseDN, &p.BindDN, &p.SearchFilter, &p.LoginAttribute, &p.IdentityAttribute, &p.CAPEM, &p.HasSecret, &p.Version, &p.Enabled, &p.BindingEnabled, &p.SearchSizeLimit, &mapping, &p.Managed)
	if err == nil && len(mapping) > 0 {
		err = json.Unmarshal(mapping, &p.PropertyMapping)
	}
	return p, err
}
func (s *Service) SaveLDAPProvider(ctx context.Context, p LDAPProvider, password string) (string, error) {
	if p.Managed {
		return "", fault.Conflict
	}
	if p.ID != "" {
		var managed bool
		if e := s.db.Row(ctx, `SELECT settings_managed FROM ldap_identity_providers WHERE id=$1`, p.ID).Scan(&managed); e != nil {
			return "", e
		}
		if managed {
			return "", fault.Conflict
		}
	}
	// Older clients omit these fields. Preserve existing bounds/mappings rather
	// than silently widening a configured directory on a name/password edit.
	if p.ID != "" && (p.SearchSizeLimit == nil || p.PropertyMapping == nil) {
		old, e := s.LDAPProvider(ctx, p.ID)
		if e != nil {
			return "", e
		}
		if p.SearchSizeLimit == nil {
			p.SearchSizeLimit = old.SearchSizeLimit
		}
		if p.PropertyMapping == nil {
			p.PropertyMapping = old.PropertyMapping
		}
	}
	if ValidateLDAPProvider(p) != nil || len(password) > 65536 {
		return "", fault.Invalid
	}
	mapping, err := json.Marshal(p.PropertyMapping)
	if err != nil {
		return "", err
	}
	if p.ID == "" {
		if password == "" {
			return "", fault.Invalid
		}
		if err := s.db.Row(ctx, `INSERT INTO ldap_identity_providers(name,url,base_dn,bind_dn,search_filter,login_attribute,identity_attribute,ca_pem,search_size_limit,property_mapping) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, p.Name, p.URL, p.BaseDN, p.BindDN, p.SearchFilter, p.LoginAttribute, p.IdentityAttribute, p.CAPEM, p.DirectoryLimit(), mapping).Scan(&p.ID); err != nil {
			return "", err
		}
	} else {
		// Identity boundary is immutable. Create a new provider for a different directory.
		tag, err := s.db.Exec(ctx, `UPDATE ldap_identity_providers SET name=$2,bind_dn=$3,search_filter=$4,login_attribute=$5,ca_pem=$6,search_size_limit=$11,property_mapping=$12,version=version+1 WHERE id=$1 AND version=$7 AND url=$8 AND base_dn=$9 AND identity_attribute=$10`, p.ID, p.Name, p.BindDN, p.SearchFilter, p.LoginAttribute, p.CAPEM, p.Version, p.URL, p.BaseDN, p.IdentityAttribute, p.DirectoryLimit(), mapping)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
	}
	if password != "" {
		sealed, err := secretbox.Seal(s.key, []byte(password), "ldap-provider:"+p.ID)
		if err != nil {
			return "", err
		}
		if _, err = s.db.Exec(ctx, `UPDATE ldap_identity_providers SET secret_sealed=$2 WHERE id=$1`, p.ID, sealed); err != nil {
			return "", err
		}
	}
	if _, err := s.db.Exec(ctx, `UPDATE ldap_identity_providers SET enabled=$2,binding_enabled=$3 WHERE id=$1`, p.ID, p.Enabled, p.BindingEnabled); err != nil {
		return "", err
	}
	return p.ID, nil
}
func (s *Service) LDAPPassword(ctx context.Context, id string, vaultKeys ...[]byte) (string, error) {
	var managed bool
	var ref string
	if e := s.db.Row(ctx, `SELECT settings_managed,coalesce(configuration_secret_id::text,'') FROM ldap_identity_providers WHERE id=$1`, id).Scan(&managed, &ref); e != nil {
		return "", e
	}
	if managed {
		if ref == "" || len(vaultKeys) != 1 {
			return "", fault.Invalid
		}
		var sealed []byte
		if e := s.db.Row(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1 AND key='ldap-password'`, ref).Scan(&sealed); e != nil {
			return "", e
		}
		plain, e := secretbox.Open(vaultKeys[0], sealed, "settings:ldap-password:"+ref)
		defer clear(plain)
		return string(plain), e
	}
	var sealed []byte
	if err := s.db.Row(ctx, `SELECT secret_sealed FROM ldap_identity_providers WHERE id=$1`, id).Scan(&sealed); err != nil {
		return "", err
	}
	plain, err := secretbox.Open(s.key, sealed, "ldap-provider:"+id)
	defer clear(plain)
	return string(plain), err
}
