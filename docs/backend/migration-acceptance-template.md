# 单轮前端验收记录模板

复制到团队验收记录目录，填写实际结果。此模板没有预先通过任何功能。

| 项目 | 填写值 |
|---|---|
| 轮次 / 日期 / 验收人 | |
| 前端commit / 后端commit / 镜像digest | |
| runtime.backendId / instanceId / release | |
| schemaVersion / capability revision | |
| 前端URL / 后端部署环境 | |
| 测试账号角色 / 测试资产fixture ID | 不填密码 |
| 开发自测 / 用户验收 | not_run / passed / failed 分别填写 |
| 本轮状态 | planned / developing / ready_for_user / accepted / failed |

## 操作与证据

| 用例ID | 用户操作 | 预期 | 实际 | 结果 | requestId / sessionId / 日志或截图 | 问题ID |
|---|---|---|---|---|---|---|
| 本轮正向 | | | | not_run | | |
| 本轮反向/越权 | | | | not_run | | |
| 刷新/重登/重启持久化 | | | | not_run | | |
| 中断/重试/重复提交 | | | | not_run | | |
| 前轮回归 | | | | not_run | | |

## 放行与回退

- 仍未开放的能力及原因：
- 已接受的兼容差异（必须明确到功能，不能笼统“暂不支持”）：
- 阻断缺陷与修复版本：
- 升级前备份位置及恢复演练证据（不填私钥）：
- 失败回退命令/适用数据版本/数据丢失风险：
- 用户验收结论与日期：
- 下一轮可开始的条件：

请求日志和HAR需脱敏：Cookie、Authorization、票据、SQL敏感参数、目标密码及个人信息不得直接附到公开记录中。UI角标只证明代理配置，实际后端身份需用runtime响应和服务日志核对。
