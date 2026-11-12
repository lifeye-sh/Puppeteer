package system

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/mail"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

func validateMailConfiguration(v ConfigurationValues) error {
	// SMTP always verifies TLS. A private CA may be supplied explicitly.
	if v.Bool("mail-insecure-skip-verify") {
		return fault.Invalid
	}
	if raw := v.String("mail-ca-pem"); raw != "" {
		rest := []byte(raw)
		count := 0
		for len(strings.TrimSpace(string(rest))) > 0 {
			rest = []byte(strings.TrimSpace(string(rest)))
			if !strings.HasPrefix(string(rest), "-----BEGIN CERTIFICATE-----") {
				return fault.Invalid
			}
			block, tail := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
				return fault.Invalid
			}
			cert, e := x509.ParseCertificate(block.Bytes)
			if e != nil || !cert.IsCA {
				return fault.Invalid
			}
			count++
			rest = tail
		}
		if count == 0 {
			return fault.Invalid
		}
	}
	if !v.Bool("mail-enabled") && v.String("mail-host") == "" && v.String("mail-from") == "" && v.String("mail-username") == "" && v.String("mail-password") == "" {
		return nil
	}
	host := v.String("mail-host")
	if host == "" {
		return fault.Invalid
	}
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fault.Invalid
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return fault.Invalid
				}
			}
		}
	}
	if _, err := mail.ParseAddress(v.String("mail-from")); err != nil {
		return fault.Invalid
	}
	if v.Bool("mail-enabled") && v.String("mail-username") != "" && v.String("mail-password") == "" {
		return fault.Invalid
	}
	return nil
}

// Runs inside SaveConfiguration's locked transaction, including CLI writes.
// Existing independently managed channels are not adopted or overwritten.
func (s *Service) applyMailConfiguration(ctx context.Context, v ConfigurationValues) error {
	if v.String("mail-host") == "" {
		_, err := s.db.Exec(ctx, `UPDATE notification_channels SET enabled=false,version=version+1 WHERE type='smtp' AND settings_managed`)
		return err
	}
	config, err := json.Marshal(map[string]any{"host": v.String("mail-host"), "port": v.Int("mail-port"), "implicitTls": v.Bool("mail-use-ssl"), "username": v.String("mail-username"), "from": v.String("mail-from"), "ca": v.String("mail-ca-pem")})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO notification_channels(name,type,enabled,config,settings_managed,configuration_secret_id) VALUES('平台邮件（统一配置）','smtp',$1,$2,true,nullif($3,'')::uuid) ON CONFLICT(type) WHERE settings_managed DO UPDATE SET enabled=EXCLUDED.enabled,config=EXCLUDED.config,configuration_secret_id=EXCLUDED.configuration_secret_id,version=notification_channels.version+1`, v.Bool("mail-enabled"), config, v.String("mail-password"))
	return err
}
