# Xiaoya Media Server V1.0 验收清单与已知边界

本文档逐项对照《Xiaoya Media Server 完整技术方案 V1.0》的功能与验收标准，记录实现与本地回归范围。发布前还需在实际 NAS 和目标 Emby 客户端完成实机验收。

---

## 1. 七大核心模块验收对照表（对应方案第 15 章）

### 15.1 媒体服务基础能力（`go-emby` 完整演进）
- [x] 管理员初始化、密码哈希、令牌鉴权、API Key 管理与播放设备并发限制：`internal/embyserver/main.go`
- [x] 网页媒体库浏览、搜索和管理；Emby 兼容接口提供拼音首字母检索与多版本合并：`internal/embyserver/browsing.go`、`internal/embyserver/versions.go`、`internal/embyserver/initials.go`
- [x] 302 直链播放、本地 `file://` Range 流式播放、播放进度回传、继续观看（`Resume`）与真实下一集（`/Shows/NextUp`）：`internal/embyserver/direct_strm.go`、`internal/embyserver/subtitles.go`、`internal/embyserver/nextup.go`
- [x] 外挂字幕自动发现（同目录及 `Subs/` 子目录 `.srt/.ass/.ssa/.vtt/.sub/.idx`）、`MediaStreams` 注入与 `.srt -> WebVTT` 动态转换：`internal/embyserver/subtitles.go`
- [x] STRM 内含过期 `?sign=...` 签名时自动回退无签名 `/d/...` 并联动 AList `/api/fs/get` 刷新直链，且保留 SSRF 防护校验：`internal/embyserver/xiaoya.go`
- [x] 首次音视频直接出流、循环检测、10 次请求上限、内部回环转 AList 解析，以及刷新失败不返回失效地址；阿里和 115 实机样本均取得 CDN 地址并通过两处 Range 读取：[修复方案与验证](XIAOYA-PLAYBACK-FIX.md)。
- [x] 移除上游 `licensesdk` 联网授权校验与平台强耦合调用，跨平台纯 Go 编译：`internal/embyserver/rename_{linux,darwin,other}.go`

### 15.2 元数据与 NFO `prefer` 策略
- [x] 本地非空有效 NFO 优先读取（`prefer`），且拒绝空白页、HTML 错误页与 JSON 报错内容：`internal/embyserver/tmdb_metadata.go`、`internal/strm/generator.go`
- [x] 支持识别标准目录与独立 `{tmdb-270431}` 中间层目录并归集 `Series -> Season -> Episode`：`internal/metadata/tmdb/extract.go`、`internal/embyserver/snapshot.go`
- [x] 无本地 NFO 时自动调用 TMDB v3 API 刮削剧集/季/分集/电影中文标题、简介、海报、背景图与演职员，支持 `zh-CN -> en-US` 简介回退与 PostgreSQL 缓存：`internal/metadata/tmdb/client.go`、`internal/embyserver/tmdb_metadata.go`
- [x] TMDB 未启用、超时或报错绝不阻塞 STRM 生成、媒体入库与播放：`internal/embyserver/zheyueliang_e2e_test.go`
- [x] 管理后台支持配置 TMDB Key、连通性测试及手动搜索绑定修正：`internal/embyserver/tmdb_metadata.go`、`web/index.html`

### 15.3 指定目录监控（只读消费小雅 SQLite）
- [x] 双重只读锁（`mode=ro` + `_pragma=query_only(1)`）读取 `strm_internal.db`，兼容 WAL 模式，绝不修改小雅表或索引：`internal/resource/xiaoya/reader.go`
- [x] 可按需精确监控 `/每日更新/电视剧/国产剧（115）` 与 `/每日更新/电视剧/国产剧`，缺失或同名兄弟目录冲突显式报错，绝不退化为模糊匹配：`internal/resource/xiaoya/reader_test.go`
- [x] 支持后台动态新增、启停、移除监控目录并立即触发全量/增量同步：`internal/adminapi/handler.go`、`web/index.html`

### 15.4 增量发现、快照对比与 `keep` 删除保护策略
- [x] 首次全量扫描建立 `BASELINE` 快照，后续支持 `id > last_max_node_id` 快速增量扫描与每日全树差异对账：`internal/resource/discovery/service.go`
- [x] 准确识别 `BASELINE_ADDED`、`ADDED`、`MODIFIED`、`RENAMED`、`MOVED`、`MISSING`、`RESTORED`、`MANUAL_IMPORTED` 八类状态流转并落库审计：`internal/resource/discovery/service_test.go`
- [x] 扫描中途异常或源库不可达时批次标记为 `FAILED`，**绝不推进游标，绝不误标 `MISSING`**：`internal/resource/discovery/service.go`
- [x] 执行 `keep` 删除策略：仅完整成功的全树扫描才标记 `MISSING`，保留本地 `.strm`/NFO/字幕/图片、媒体条目与 `userdata` 观看进度；源端回归自动 `RESTORED`；管理员可预览并确认取消管理，文件、条目和观看记录保留：`internal/resource/discovery/store.go`、`internal/embyserver/snapshot.go`

### 15.5 Go 原生 STRM 与附件引擎
- [x] 完整覆盖 原脚本的 21 类媒体扩展名、字幕与图片/NFO 附件及 SQLite 无扩展名分集节点（`E01..E27`）：`internal/strm/extensions.go`
- [x] 深度不限递归、`%20` 转空格、防 `.strm.strm` 重复后缀、235 字符截断 + 冲突消歧 + UTF-8 255 字节安全上限：`internal/strm/pathmapper.go`
- [x] 11 字符精确编码、保留中文与已有 `%XX` 不二次编码、保留 `?sign=...` 查询参数、备用字符翻译表：`internal/strm/encoder.go`
- [x] 临时文件 + 原子替换写入、比较内容并跳过未变化文件、附件失败隔离不阻塞主流程：`internal/strm/generator.go`
- [x] 使用 `python3` 执行原规则片段，另以 Go 测试覆盖路径、编码、签名和附件边界：`internal/strm/compat_golden_test.go`

### 15.6 AList 浏览、搜索与一键人工入库
- [x] 支持 AList v3 Token / 用户名密码自动登录续期、目录浏览（`/api/fs/list`）、关键词搜索（`/api/fs/search`）与详情查询（`/api/fs/get`）：`internal/resource/alist/client.go`
- [x] 支持单文件一键入库与整部剧集目录递归入库，纳入同一 `media_resources` + `managed_resources` 统一模型：`internal/resource/alist/importer.go`
- [x] 整剧入库支持开启自动追更（`MANUAL_SERIES_DIR`），定时发现新增剧集并自动生成 STRM 与局部刷新媒体库：`internal/resource/alist/client_test.go`、`internal/scheduler/scheduler.go`

### 15.7 统一后台控制台与单栈部署交付
- [x] 后台异步同步/入库提供可查询的任务结果，重启中断的任务可以重跑。
- [x] 资源与任务页支持查看变更、扫描批次、定时任务及取消管理预览。
- [ ] 在实际 NAS 上验收部署与挂载，在目标 Emby 客户端验收选集、播放、字幕和续播。
- [x] 单一服务进程与端口同时提供 Emby 兼容 API、影库前端与后台“资源与监控 / AList 入库 / 批次与任务 / TMDB 刮削”四大管理中心：`cmd/server/main.go`、`internal/adminapi/handler.go`、`web/index.html`
- [x] 完整交付多阶段 `deployments/Dockerfile`、`deployments/compose.yaml`、`deployments/.env.example` 与部署文档：`deployments/`、`docs/DEPLOYMENT.md`

---

## 2. `折月亮` 14 步端到端样本验收结果（对应方案第 14 章）

详见专项报告 [ZHEYUELIANG-E2E-REPORT.md](ZHEYUELIANG-E2E-REPORT.md) 及自动化测试 [`TestZheYueLiang14StepEndToEnd`](../internal/embyserver/zheyueliang_e2e_test.go)。该用例使用临时 SQLite 样本树及模拟 Xiaoya、TMDB、附件响应；真实网盘检查见 [播放链路修复记录](XIAOYA-PLAYBACK-FIX.md)：
- 样本链路 `568922 -> 617762 -> 639780 -> 641716 (折月亮 (2026)) -> 641717 ({tmdb-270431}) -> 641718 (Season 1) -> 641719..641745 (E01..E27)` 全部 **14/14 步通过**，涵盖基线入库、幂等二次扫描、`E28` 增量追更、TMDB `270431` 刮削、本地 NFO `prefer` 覆盖、302 播放与 `NextUp` 推进、外挂字幕 WebVTT 转换、`E28 -> MISSING (keep)` 保护；恢复由资源生命周期用例单独覆盖。

---

## 3. V1.0 已知边界与后续演进方向（对应方案第 16 章）

1. **实时服务端转码（HLS / FFmpeg 硬解重编码）不在 V1.0 范围**：
   - V1.0 聚焦 STRM 302 直链播放与本地 Range 直出，依赖客户端原生解码能力（Infuse / VidHub / SenPlayer）；服务端仅使用 `ffprobe` 异步提取媒体流信息。
2. **小雅源端“仅删除节点”的感知时机**：
   - 小雅 `nodes` 表无 `updated_at` 或软删除墓碑字段，分钟级 `id > last_max_node_id` 增量扫描专用于极速捕获新增节点；源端节点改名、移动或消失由定时/手动触发的完整目录树差异扫描（`daily_full_diff` 或控制台“全量同步”）精确对账。
3. **多刮削源扩展预留**：
   - V1.0 内置本地 NFO (`prefer`) + TMDB v3 双源协同；后续版本可在此元数据接口之上按需扩展豆瓣 / Bangumi / Fanart.tv 等补充图片与评分源。
