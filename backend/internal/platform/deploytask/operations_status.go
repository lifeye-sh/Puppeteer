package deploytask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type OperationStatus struct {
	Supported    bool         `json:"supported"`
	Desired      string       `json:"desired"`
	Applied      string       `json:"applied"`
	Dependencies string       `json:"dependencies"`
	Service      string       `json:"service"`
	Reason       string       `json:"reason"`
	CheckedAt    time.Time    `json:"checkedAt,omitempty"`
	Schedule     *walSchedule `json:"schedule,omitempty"`
}

func (d *HostDriver) Operations(ctx context.Context) map[string]OperationStatus {
	out := map[string]OperationStatus{}
	raw, e := os.ReadFile(d.Executor.Files[0])
	var doc map[string]json.RawMessage
	var enrollment operationsEnrollment
	var model composeModel
	good := e == nil && json.Unmarshal(raw, &doc) == nil && json.Unmarshal(raw, &model) == nil && json.Unmarshal(doc["x-puppeteer-operations"], &enrollment) == nil && enrollment.Version == 1
	digest := sha256.Sum256(raw)
	healthy := map[string]bool{}
	observed, observedErr := d.Executor.run(ctx, 8*time.Second, "observe operations services", "ps", "--all", "--format", "json")
	if observedErr == nil {
		var containers []containerStatus
		containers, observedErr = parseGraphicsContainers(observed)
		for _, c := range containers {
			healthy[c.Service] = c.State == "running" && c.Health == "healthy"
		}
	}
	for _, id := range []string{"agent-repository", "wal", "web-access"} {
		status := OperationStatus{Supported: good && (id != "wal" || enrollment.WAL), Desired: "disabled", Applied: "unknown", Dependencies: "unknown", Service: "unknown", Reason: "尚无当前部署的专项验证证据"}
		if good {
			status.Applied = "applied"
			enabled := false
			if id == "wal" {
				enabled = walEnabled(raw)
			} else {
				var mounts []composeMount
				json.Unmarshal(model.Services["web"]["volumes"], &mounts)
				for _, m := range mounts {
					if id == "agent-repository" && m.Target == "/srv/agent-updates" {
						enabled = true
					}
					if id == "web-access" && m.Target == "/etc/nginx/access-server/managed.conf" {
						if b, e := os.ReadFile(m.Source); e == nil {
							enabled = strings.Contains(string(b), "deny all;")
						}
					}
				}
			}
			if enabled {
				status.Desired = "enabled"
			}
			var proof operationsState
			if b, e := os.ReadFile(filepath.Join(d.StateDirectory, id+"-status.json")); e == nil && json.Unmarshal(b, &proof) == nil {
				status.CheckedAt = proof.CheckedAt
				status.Reason = proof.Reason
				if proof.State == "rolled_back" {
					status.Dependencies = "blocked"
				} else if proof.Digest != hex.EncodeToString(digest[:]) {
					status.Dependencies = "stale"
					status.Reason = "部署配置在上次验证后发生变化，请重新预检并执行以更新证据"
				} else if proof.State == "verified" || proof.State == "recoverable" || proof.State == "stopped" {
					status.Dependencies = "ready"
					status.Service = "verified"
					if !enabled {
						status.Service = "stopped"
					}
				}
			}
			if id == "wal" && enabled {
				status.Service = "stale"
				var schedule walSchedule
				if b, e := os.ReadFile(filepath.Join(d.StateDirectory, "wal-schedule.json")); e == nil && json.Unmarshal(b, &schedule) == nil {
					status.Schedule = &schedule
					if schedule.Error != "" {
						status.Service = "failed"
						status.Reason = schedule.Error
					} else if status.Dependencies == "ready" && time.Since(schedule.Check) < 6*time.Minute && time.Since(schedule.Backup) < 25*time.Hour {
						status.Service = "recoverable"
					} else if status.Dependencies == "ready" {
						status.Reason = "WAL 备份或归档检查未按时完成，请检查后台任务及执行器状态"
					}
				}
			}
			if id == "agent-repository" && enabled {
				var mounts []composeMount
				json.Unmarshal(model.Services["web"]["volumes"], &mounts)
				for _, m := range mounts {
					if m.Target == "/srv/agent-updates" {
						latestRoot := "1.root.json"
						for n := 2; n <= 16; n++ {
							name := strconv.Itoa(n) + ".root.json"
							if _, err := os.Stat(filepath.Join(m.Source, name)); err != nil {
								break
							}
							latestRoot = name
						}
						for _, name := range []string{latestRoot, "targets.json", "snapshot.json", "timestamp.json"} {
							var meta struct {
								Signed struct {
									Expires time.Time `json:"expires"`
								} `json:"signed"`
							}
							b, err := os.ReadFile(filepath.Join(m.Source, name))
							if err != nil || json.Unmarshal(b, &meta) != nil || !meta.Signed.Expires.After(time.Now()) {
								status.Dependencies, status.Service, status.Reason = "stale", "failed", "签名元数据缺失或已过期，请重新发布有效的签名仓库"
							}
						}
					}
				}
			}
			service := "web"
			if id == "wal" {
				service = "postgres"
			}
			if observedErr != nil {
				status.Service, status.Reason = "unknown", "无法读取实际容器状态"
			} else if !healthy[service] {
				status.Service, status.Reason = "failed", "相关服务容器未运行或未通过健康检查"
			}
		}
		if !status.Supported {
			status.Reason = "当前安装未接管此专项能力，请使用匹配的安装/升级脚本"
		}
		out[id] = status
	}
	return out
}
