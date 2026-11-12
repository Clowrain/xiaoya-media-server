# Emby 客户端兼容性矩阵（阶段 5.2）

- 状态：**等待实际客户端验收；当前没有完成的 Emby 客户端实机记录**。
- 盘点日期：2026-11-12。
- 目的：把仓库自动化测试与真实客户端观察分开记录。单元测试、`httptest`、模拟上游或直接访问 CDN 均不能记作 Emby 客户端实机通过。

## 客户端实机记录

每次执行时填写客户端版本、设备/操作系统、服务器构建、测试日期、实际 HTTP 请求/响应或脱敏抓包位置和结论。当前目标客户端均未在本轮运行，版本与观察数据留空，不推定通过。

| 客户端 | 版本 / OS / 设备 | 登录 | 浏览 | 分页 | PlaybackInfo | 302 跟随 | Range / 拖动 | 字幕 | 进度 / 续播 | 记录 |
|---|---|---|---|---|---|---|---|---|---|---|
| Infuse | 未提供 / 未运行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 待实机记录 |
| VidHub | 未提供 / 未运行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 待实机记录 |
| Emby Theater | 未提供 / 未运行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 待实机记录 |
| SenPlayer | 未提供 / 未运行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 待实机记录 |
| 其他目标客户端 | 未提供 / 未运行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 未执行 | 请补充客户端与版本 |

## 自动化覆盖（非实机证据）

| 场景 | 仓库自动化证据 | 限制 |
|---|---|---|
| 登录 / 认证 | `internal/embyserver/login_compat_test.go` | 合成请求和响应；没有客户端生成的握手样本 |
| 浏览 / 分页 | `internal/embyserver/emby_routing_spec_test.go`、`pagination_test.go` | handler/查询测试；没有客户端 continuation 交互样本 |
| PlaybackInfo | `internal/embyserver/emby_routing_spec_test.go`、`emby_playback_spec_test.go`、`playback_info_dto_test.go` | 合成 API 请求；客户端对字段选择的接受情况未验证 |
| 302 / 播放 fallback | `internal/embyserver/playback_fallback_test.go`、`emby_playback_spec_test.go` | 服务端 redirect 测试；没有真实播放器观察与跟随记录 |
| Range / HEAD | `internal/embyserver/playback_subtitles_nextup_test.go` | 覆盖本地 handler；远端 CDN Range 记录见 `XIAOYA-PLAYBACK-FIX.md`，该记录不是播放器 seek 验收 |
| 字幕 | `internal/embyserver/playback_subtitles_nextup_test.go`、`emby_playback_spec_test.go` | 转换和 handler 合成测试；未验证客户端选择及渲染 |
| 用户进度 / 续播 | `internal/embyserver/emby_playback_spec_test.go`、`user_actions_test.go` | 合成请求及数据状态测试；没有实际客户端生命周期记录 |

`docs/ZHEYUELIANG-E2E-REPORT.md` 的 14/14 流程使用临时 SQLite 和模拟 Xiaoya/TMDB/附件响应；它是自动化 E2E 证据，不是 Emby 客户端实机验收。`docs/XIAOYA-PLAYBACK-FIX.md` 的 CDN 与 Range 检查验证了资源端链路，不等价于客户端 302 跟随、seek 或字幕验证。

## 已知边界与待验项

- P0 浏览、分页、PlaybackInfo、直链 redirect、本地 Range/HEAD 和外挂字幕均有服务端实现及自动化测试；目标客户端行为仍待验证。远端 Range/seek 还依赖实际资源源站和播放器。
- `/Sessions` 目前返回空集合；仓库保存播放会话并提供活动会话读取能力，但没有已验证的 Emby Session DTO/目标客户端需求。保持列为协议/客户端待确认项，不将内部会话记录直接映射成猜测的协议响应。
- WebSocket 当前未实现。HTTP 播放进度接口不代表 WebSocket 兼容；应先通过目标客户端观察确认是否依赖，再决定是否开发。
- 服务端转码/HLS 是明确非目标，不应宣传为支持。`/Shows/NextUp` 恒空是既有产品取舍，阶段 5 不擅自改变。

## 实机验收记录模板

对每个客户端和场景，至少保存：客户端名称、精确版本、设备/OS、服务器构建/配置、执行时间、操作步骤、脱敏请求与响应（含状态码和必要 headers）、预期与实测结果、相关服务器日志/错误、通过或失败结论。访问令牌、密码、API Key、签名 URL 和个人媒体路径须在记录前脱敏。
