package systemupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Settings struct {
	Revision      int64  `json:"revision"`
	Channel       string `json:"channel"`
	AutoCheck     bool   `json:"autoCheck"`
	IntervalHours int    `json:"intervalHours"`
}
type History struct {
	At       time.Time `json:"at"`
	Success  bool      `json:"success"`
	Reason   string    `json:"reason,omitempty"`
	Sequence uint64    `json:"sequence,omitempty"`
}
type State struct {
	Settings    Settings        `json:"settings"`
	LastAttempt time.Time       `json:"lastAttempt,omitempty"`
	LastSuccess time.Time       `json:"lastSuccess,omitempty"`
	Error       string          `json:"error,omitempty"`
	Catalog     *Catalog        `json:"-"`
	Envelope    json.RawMessage `json:"envelope,omitempty"`
	Digest      string          `json:"digest,omitempty"`
	Sequence    uint64          `json:"sequence"`
	History     []History       `json:"history"`
}
type View struct {
	Settings    Settings   `json:"settings"`
	State       string     `json:"state"`
	Reason      string     `json:"reason,omitempty"`
	LastAttempt time.Time  `json:"lastAttempt"`
	LastSuccess time.Time  `json:"lastSuccess"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	Sequence    uint64     `json:"sequence"`
	Releases    []Release  `json:"releases"`
	History     []History  `json:"history"`
}
type Manager struct {
	mu       sync.Mutex
	checking bool
	path     string
	state    State
	keys     map[string]ed25519.PublicKey
	fetch    func(context.Context) ([]byte, error)
	now      func() time.Time
}

func New(path string, keys map[string]ed25519.PublicKey, fetch func(context.Context) ([]byte, error)) (*Manager, error) {
	m := &Manager{path: path, keys: keys, fetch: fetch, now: time.Now, state: State{Settings: Settings{Revision: 1, Channel: "beta", AutoCheck: true, IntervalHours: 24}, History: []History{}}}
	if fetch == nil {
		m.fetch = Fetch
	}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil && !os.IsNotExist(e) {
			return nil, errors.New("无法读取更新检查记录")
		}
		if e == nil {
			if len(b) > 2*MaxIndexBytes || strict(b, &m.state) != nil || !validSettings(m.state.Settings) || len(m.state.History) > 30 {
				return nil, errors.New("更新检查记录损坏，请核验持久化存储")
			}
			if len(m.state.Envelope) > 0 {
				c, digest, err := Verify(m.state.Envelope, keys, m.now(), m.state.Sequence, m.state.Digest)
				if err != nil {
					m.state.Error = err.Error()
				} else {
					m.state.Catalog = &c
					m.state.Digest = digest
					m.state.Sequence = c.Sequence
				}
			} else if m.state.Sequence > 0 {
				m.state.Error = "缓存缺少已签名索引，请重新检查"
			}
		}
	}
	return m, nil
}
func Fetch(ctx context.Context) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", IndexURL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/json")
	c := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := c.Do(req)
	if e != nil {
		return nil, errors.New("无法连接更新源，请检查出站网络或 HTTPS 代理")
	}
	defer r.Body.Close()
	if r.StatusCode == 404 {
		return nil, errors.New("更新源尚未发布签名索引")
	}
	if r.StatusCode != 200 {
		return nil, errors.New("更新源响应异常，请稍后重试")
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, MaxIndexBytes+1))
	if e != nil || len(b) > MaxIndexBytes {
		return nil, errors.New("发布索引读取失败或超过大小限制")
	}
	return b, nil
}
func validSettings(s Settings) bool {
	return s.Revision > 0 && (s.Channel == "beta" || s.Channel == "stable") && s.IntervalHours >= 1 && s.IntervalHours <= 168
}
func (m *Manager) save(s State) error {
	if m.path != "" {
		if e := os.MkdirAll(filepath.Dir(m.path), 0700); e != nil {
			return errors.New("无法创建更新记录目录")
		}
		b, e := json.Marshal(s)
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(filepath.Dir(m.path), ".update-*")
		if e != nil {
			return errors.New("无法写入更新记录")
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if e = f.Chmod(0600); e == nil {
			_, e = f.Write(b)
		}
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e == nil {
			e = os.Rename(tmp, m.path)
		}
		if e != nil {
			return errors.New("更新记录持久化失败")
		}
	}
	m.state = s
	return nil
}
func (m *Manager) Configure(s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validSettings(s) {
		return errors.New("更新渠道或检查间隔无效")
	}
	if s.Revision != m.state.Settings.Revision {
		return errors.New("更新设置已变化，请刷新后重试")
	}
	next := m.state
	s.Revision++
	next.Settings = s
	return m.save(next)
}
func (m *Manager) Due() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Settings.AutoCheck && !m.checking && m.now().Sub(m.state.LastAttempt) >= time.Duration(m.state.Settings.IntervalHours)*time.Hour
}
func (m *Manager) Check(ctx context.Context) error {
	m.mu.Lock()
	if m.checking {
		m.mu.Unlock()
		return errors.New("正在检查更新")
	}
	if m.now().Sub(m.state.LastAttempt) < 30*time.Second {
		m.mu.Unlock()
		return errors.New("检查过于频繁，请稍后重试")
	}
	m.checking = true
	sequence, digest := m.state.Sequence, m.state.Digest
	m.mu.Unlock()
	b, e := m.fetch(ctx)
	var c Catalog
	var nextDigest string
	if e == nil {
		c, nextDigest, e = Verify(b, m.keys, m.now(), sequence, digest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() { m.checking = false }()
	next := m.state
	next.LastAttempt = m.now()
	h := History{At: next.LastAttempt, Success: e == nil}
	if e != nil {
		next.Error = e.Error()
		h.Reason = next.Error
	} else {
		next.LastSuccess = next.LastAttempt
		next.Catalog = &c
		next.Envelope = append(json.RawMessage{}, b...)
		next.Sequence = c.Sequence
		next.Digest = nextDigest
		next.Error = ""
		h.Sequence = c.Sequence
	}
	next.History = append([]History{h}, next.History...)
	if len(next.History) > 30 {
		next.History = next.History[:30]
	}
	if err := m.save(next); err != nil {
		return err
	}
	return e
}
func (m *Manager) View(current string) View {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	v := View{Settings: s.Settings, State: "not_checked", LastAttempt: s.LastAttempt, LastSuccess: s.LastSuccess, Sequence: s.Sequence, Releases: []Release{}, History: append([]History{}, s.History...)}
	if s.Catalog != nil {
		expiry := s.Catalog.ExpiresAt
		v.ExpiresAt = &expiry
		for _, r := range s.Catalog.Releases {
			if r.Channel == s.Settings.Channel {
				r.Images = append([]Image{}, r.Images...)
				v.Releases = append(v.Releases, r)
			}
		}
		sort.Slice(v.Releases, func(i, j int) bool { c, _ := Compare(v.Releases[i].Version, v.Releases[j].Version); return c > 0 })
		v.State = "no_channel_release"
		if len(v.Releases) > 0 {
			cmp, e := Compare(v.Releases[0].Version, current)
			if e != nil {
				v.State = "custom_build"
				v.Reason = e.Error()
			} else if cmp > 0 {
				v.State = "update_available"
			} else if cmp == 0 {
				v.State = "up_to_date"
			} else {
				v.State = "ahead_of_channel"
			}
		}
		if !expiry.After(m.now()) {
			v.State = "expired"
			v.Reason = "发布索引已过期，请重新检查更新"
		}
	}
	if s.Error != "" {
		v.State = "check_failed"
		v.Reason = s.Error
	}
	if m.checking {
		v.State = "checking"
	}
	return v
}
func (m *Manager) Target(v string) (Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.target(v)
}

func (m *Manager) SignedTarget(v string) (Release, json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, e := m.target(v)
	if e != nil {
		return Release{}, nil, e
	}
	return r, append(json.RawMessage(nil), m.state.Envelope...), nil
}

func (m *Manager) target(v string) (Release, error) {
	if m.state.Catalog == nil || m.state.Error != "" || !m.state.Catalog.ExpiresAt.After(m.now()) {
		return Release{}, errors.New("请先成功获取有效的签名发布索引")
	}
	for _, r := range m.state.Catalog.Releases {
		if r.Version == v && r.Channel == m.state.Settings.Channel {
			r.Images = append([]Image{}, r.Images...)
			return r, nil
		}
	}
	return Release{}, errors.New("目标版本不在当前渠道的可信发布索引中")
}
