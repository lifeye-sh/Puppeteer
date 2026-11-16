package systemupdate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// Authorization is stored on the host, outside application backup/rollback.
// It never contains passwords or MFA proof.
type AutomationPolicy struct {
	RetainDays        int       `json:"retainDays"`
	Revision          uint64    `json:"revision"`
	Mode              string    `json:"mode"`
	Channel           string    `json:"channel"`
	MaxVersion        string    `json:"maxVersion"`
	Timezone          string    `json:"timezone"`
	Weekdays          []int     `json:"weekdays"`
	Start             string    `json:"start"`
	DurationMinutes   int       `json:"durationMinutes"`
	ExpiresAt         time.Time `json:"expiresAt"`
	AllowSchemaChange bool      `json:"allowSchemaChange"`
}

func DefaultAutomation() AutomationPolicy {
	return AutomationPolicy{Revision: 1, Mode: "off", Channel: "beta", Timezone: "Asia/Shanghai", Weekdays: []int{0}, Start: "02:00", DurationMinutes: 120}
}

func (p AutomationPolicy) Validate(now time.Time) error {
	if p.RetainDays != 0 && (p.RetainDays < 30 || p.RetainDays > 365) {
		return errors.New("升级产物保留期须为 0（不清理）或 30–365 天")
	}
	if p.Revision == 0 || (p.Mode != "off" && p.Mode != "prepare" && p.Mode != "window") {
		return errors.New("自动升级模式或配置版本无效")
	}
	if p.Mode == "off" {
		return nil
	}
	if p.Channel != "beta" && p.Channel != "stable" {
		return errors.New("自动升级渠道无效")
	}
	if _, e := Compare(p.MaxVersion, p.MaxVersion); e != nil {
		return errors.New("请指定授权版本上限")
	}
	if !p.ExpiresAt.After(now) || p.ExpiresAt.After(now.Add(30*24*time.Hour)) {
		return errors.New("自动升级授权必须在未来 30 天内到期")
	}
	if _, e := time.LoadLocation(p.Timezone); e != nil {
		return errors.New("维护窗口时区无效")
	}
	if len(p.Weekdays) < 1 || len(p.Weekdays) > 7 || p.DurationMinutes < 15 || p.DurationMinutes > 240 {
		return errors.New("请选择星期，维护窗口须为 15–240 分钟")
	}
	seen := map[int]bool{}
	for _, d := range p.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			return errors.New("维护星期无效或重复")
		}
		seen[d] = true
	}
	if len(p.Start) != 5 || p.Start[2] != ':' {
		return errors.New("维护开始时间须为 HH:MM")
	}
	hour, e := strconv.Atoi(p.Start[:2])
	minute, e2 := strconv.Atoi(p.Start[3:])
	if e != nil || e2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return errors.New("维护开始时间无效")
	}
	return nil
}

// Window returns one stable local-calendar slot across DST folds and midnight.
// The deadline is a latest downtime START, not a deadline that aborts migration.
func (p AutomationPolicy) Window(now time.Time) (string, time.Time, bool) {
	if p.Mode == "off" || !p.ExpiresAt.After(now) {
		return "", time.Time{}, false
	}
	loc, e := time.LoadLocation(p.Timezone)
	if e != nil {
		return "", time.Time{}, false
	}
	local := now.In(loc)
	if p.Mode == "prepare" {
		return local.Format("2006-01-02"), p.ExpiresAt, true
	}
	clock := strings.Split(p.Start, ":")
	if len(clock) != 2 {
		return "", time.Time{}, false
	}
	hour, e := strconv.Atoi(clock[0])
	minute, e2 := strconv.Atoi(clock[1])
	if e != nil || e2 != nil {
		return "", time.Time{}, false
	}
	for _, offset := range []int{0, -1} {
		date := local.AddDate(0, 0, offset)
		start := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
		if start.Format("15:04") != p.Start {
			continue
		} // nonexistent local time at DST change
		allowed := false
		for _, day := range p.Weekdays {
			allowed = allowed || int(start.Weekday()) == day
		}
		end := start.Add(time.Duration(p.DurationMinutes) * time.Minute)
		if end.After(p.ExpiresAt) {
			end = p.ExpiresAt
		}
		if allowed && !now.Before(start) && now.Before(end) {
			return fmt.Sprintf("%s/%s", start.Format("2006-01-02"), p.Start), end, true
		}
	}
	return "", time.Time{}, false
}

func (p AutomationPolicy) Select(c Catalog, current string) (Release, bool) {
	var best Release
	for _, r := range c.Releases {
		newer, e := Compare(r.Version, current)
		bound, e2 := Compare(r.Version, p.MaxVersion)
		source, e3 := Compare(current, r.MinSource)
		if r.Channel != p.Channel || e != nil || e2 != nil || e3 != nil || newer <= 0 || bound > 0 || source < 0 {
			continue
		}
		if best.Version == "" {
			best = r
		} else if n, _ := Compare(r.Version, best.Version); n > 0 {
			best = r
		}
	}
	return best, best.Version != ""
}
