package identity

import (
	"context"
	"net/netip"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

func normalizedIP(address string) (string, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return "", fault.Invalid
	}
	return ip.Unmap().String(), nil
}
func (s *Service) lockIP(ctx context.Context, address string) error {
	ip, err := normalizedIP(address)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,6401))`, ip)
	return err
}
func (s *Service) CheckIPLock(ctx context.Context, address string) error {
	ip, err := normalizedIP(address)
	if err != nil {
		return err
	}
	configuration, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return err
	}
	enabled, ok := configuration.Values["login-lock-enabled"].(bool)
	if !ok {
		return fault.Invalid
	}
	if !enabled {
		return nil
	}
	var locked bool
	if err = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM login_ip_locks WHERE address=$1::inet AND locked_until>now())`, ip).Scan(&locked); err != nil {
		return err
	}
	if locked {
		return fault.Locked
	}
	return nil
}
func (s *Service) UnlockIP(ctx context.Context, address string) error {
	ip, err := normalizedIP(address)
	if err != nil {
		return err
	}
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.UnlockIP(c, ip) })
	}
	if err = s.lockIP(ctx, ip); err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `DELETE FROM login_ip_locks WHERE address=$1::inet`, ip)
	return err
}
func (s *Service) failedIP(ctx context.Context, address string) error {
	ip, err := normalizedIP(address)
	if err != nil {
		return err
	}
	if err = s.lockIP(ctx, ip); err != nil {
		return err
	}
	v, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return err
	}
	if err = system.ValidateConfiguration(v.Values); err != nil {
		return err
	}
	if !v.Values.Bool("login-lock-enabled") {
		return nil
	}
	duration := v.Values.Int("login-lock-ip-duration")
	if duration == 0 {
		return nil
	}
	window, limit := v.Values.Int("login-lock-failed-duration"), v.Values.Int("login-lock-failed-times")
	_, err = s.db.Exec(ctx, `INSERT INTO login_ip_locks(address,failures,locked_until,last_failed_at,configuration_revision)
 VALUES($1::inet,1,CASE WHEN $3::integer=1 THEN now()+make_interval(mins=>$4) END,now(),$5)
 ON CONFLICT(address) DO UPDATE SET failures=CASE WHEN login_ip_locks.locked_until<=now() OR login_ip_locks.last_failed_at<=now()-make_interval(mins=>$2) THEN 1 ELSE least(login_ip_locks.failures::bigint+1,2147483647)::integer END,
 locked_until=NULL,last_failed_at=now(),configuration_revision=$5
 WHERE login_ip_locks.locked_until IS NULL OR login_ip_locks.locked_until<=now()`, ip, window, limit, duration, v.ID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE login_ip_locks SET locked_until=now()+make_interval(mins=>$2) WHERE address=$1::inet AND locked_until IS NULL AND failures>=$3`, ip, duration, limit)
	return err
}
