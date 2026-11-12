package operations

// Bounds apply to rule thresholds, not observed values (e.g. a reduced storage
// quota can legitimately report over 100 percent). Windowed counters are gauges.
type MonitorMetric struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Unit        string  `json:"unit"`
	Scope       string  `json:"scope"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	Description string  `json:"description"`
}

func MonitorMetrics() []MonitorMetric {
	return []MonitorMetric{
		{"agent.cpu", "Agent CPU 使用率", "%", "agent", 0.1, 100, "使用服务端收到的采集数据；过期或缺失保持未知。"},
		{"agent.memory", "Agent 内存使用率", "%", "agent", 0.1, 100, "已用内存 / 总内存；过期或缺失保持未知。"},
		{"agent.disk", "Agent 最高磁盘使用率", "%", "agent", 0.1, 100, "取采集磁盘中最高使用率；过期或缺失保持未知。"},
		{"agent.silence", "Agent 未收到采集时间", "秒", "agent", 15, 86400, "首次采集前从 Agent 注册时间计算。"},
		{"configuration.delay", "配置未应用时间", "秒", "platform", 15, 86400, "当前配置没有在线节点或尚未由全部在线节点确认时，从该版本创建时间计算。"},
		{"queue.age", "最久任务排队时间", "秒", "platform", 1, 604800, "尚未取消的 queued 任务中最早创建时间；无排队任务为0。"},
		{"queue.failed", "近15分钟任务异常数", "个", "platform", 1, 1000000, "包含 failed、partial、unknown；正常取消、跳过及仍在执行的任务不计入。"},
		{"notification.failed", "近15分钟通知投递失败数", "个", "platform", 1, 1000000, "仅最终 failed，按最后一次尝试时间计算；主动跳过、升级前无尝试时间的历史记录不计入。"},
		{"notification.delay", "通知最久待派发时间", "秒", "platform", 1, 604800, "仅 pending 且已到派发时间；静默时段与未来重试不计入。"},
		{"storage.usage", "最高个人或共享空间使用率", "%", "platform", 0.1, 100, "按各空间用量/配额取最大值；零容量仍有数据视为100%，没有空间为0。"},
		{"sessions.active", "当前远程会话数", "个", "platform", 1, 1000000, "统计当前实例授权周期内有效主机与数据库会话，包含预约。"},
		{"recording.incomplete", "近15分钟异常录屏数", "个", "platform", 1, 1000000, "已结束且非complete状态；仍写入的录屏不计入。"},
		{"backup.failed", "近15分钟备份任务异常数", "个", "platform", 1, 1000000, "backup.create 的 failed、partial、unknown；取消不计入。"},
	}
}
func monitorMetric(key string) (MonitorMetric, bool) {
	for _, m := range MonitorMetrics() {
		if m.Key == key {
			return m, true
		}
	}
	return MonitorMetric{}, false
}

// Closed SQL definitions only: no user-supplied identifiers or expressions.
var platformSamples = map[string]string{
	"queue.age":            `SELECT coalesce(greatest(0,extract(epoch FROM ($1::timestamptz-min(created_at)))),0)::double precision FROM jobs WHERE state='queued' AND NOT cancel_requested`,
	"queue.failed":         `SELECT count(*)::double precision FROM jobs WHERE state IN ('failed','partial','unknown') AND ended_at >= $1::timestamptz-interval '15 minutes'`,
	"notification.failed":  `SELECT count(*)::double precision FROM notification_deliveries WHERE status='failed' AND last_attempt_at >= $1::timestamptz-interval '15 minutes'`,
	"notification.delay":   `SELECT coalesce(greatest(0,extract(epoch FROM ($1::timestamptz-min(next_attempt)))),0)::double precision FROM notification_deliveries WHERE status='pending' AND next_attempt <= $1`,
	"storage.usage":        `SELECT coalesce(max(CASE WHEN quota_bytes>0 THEN used_bytes::double precision*100/quota_bytes WHEN used_bytes>0 THEN 100 ELSE 0 END),0) FROM user_storage WHERE $1::timestamptz IS NOT NULL`,
	"sessions.active":      `SELECT ((SELECT count(*) FROM sessions WHERE closed_at IS NULL AND lease_expires_at>$1 AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1))+(SELECT count(*) FROM database_sessions WHERE ended_at IS NULL AND quota_lease_until>$1 AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1)))::double precision`,
	"recording.incomplete": `SELECT count(*)::double precision FROM recordings WHERE state NOT IN ('complete','writing') AND ended_at >= $1::timestamptz-interval '15 minutes'`,
	"backup.failed":        `SELECT count(*)::double precision FROM jobs WHERE kind='backup.create' AND state IN ('failed','partial','unknown') AND ended_at >= $1::timestamptz-interval '15 minutes'`,
}
