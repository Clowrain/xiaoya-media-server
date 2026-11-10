# 小雅媒体服务器（Emby 兼容）低功耗 NAS 待改进技术方案

> 版本: v1.0 ｜ 日期: 2026-11-10 ｜ 状态: 待评审
> 代码基线: `0548df9`（审计基线 `af2706e` 之后 4 个提交）
> 交付说明: 本文档原计划经 `dws doc create` 生成为钉钉在线文档，但当前端 DingTalk 授权已过期，按技能错误处理降级为本地 Markdown 交付。重连钉钉后可一键转为在线文档。

---

## 方案摘要（TL;DR）

1. **架构选择**：保留「302 直链 + 客户端解码 + PostgreSQL 业务库 + strm.txt 权威清单」主链路，**不更换数据库、不引入服务端转码**。该方向已被社区验证为低功耗 NAS 的标准做法（来源：go-wdd 项目 README，"通过直链跳转与 STRM 文件降低播放/刮削成本"）。
2. **核心改进方向（按优先级）**：① 空闲功耗与调度轮询；② 同步/发布错误可见性与退避；③ PostgreSQL 低内存精调；④ 播放客户端兼容矩阵与直链生命周期；⑤ 可观测性与运维；⑥ 安全加固。
3. **性能目标**：空闲态整机额外磁盘写入趋近于 0、常驻内存 ≤ 768MB（现状容器限额）；大库浏览 P95 与深分页延迟需 NAS 实机压测后设定（**暂无可靠基准数据**）。
4. **关键风险**：客户端触发转码会使 302 直通失效、流量回落服务器（最高风险）；直链过期/上游限流导致播放失败；同步失败静默重试无退避。
5. **预计排期**：分 3 期，P0（正确性+空闲功耗）→ P1（DB 调优+兼容矩阵+可观测性）→ P2（功能增强+安全加固）。

---

## 一、背景与目标

### 1.1 业务背景

项目是部署在低功耗 NAS 上的 Emby 兼容媒体服务：以小雅/AList 的 `strm.txt` 为权威资源清单，生成 `.strm` 直链文件，业务元数据存于 PostgreSQL，播放时通过 302 重定向让客户端直连网盘真实下载地址，服务器不承担转码。

自审计基线 `af2706e` 以来，`5e54455`、`d4e2377`、`01b7bd6` 等提交已实质性修复审计中的多数 P1 问题（详见 §2.1）。本方案聚焦**剩余待改进项**，而非重复已完成的审计。

### 1.2 技术目标

- 空闲态（静态库、无播放）下不产生持续磁盘唤醒与无谓数据库事务，契合 NAS 休眠/低功耗目标。
- 同步、发布、刮削链路的失败**可见、可重试、有退避**，不静默吞错。
- 在 256MB～1GB RAM 的 NAS 上，PostgreSQL 与 Go 进程内存有界且可预测。
- 明确客户端兼容边界，避免「声明不支持转码但客户端无法解码」导致的播放失败无提示。
- 具备轻量可观测性，便于实机验收与长期运维。

### 1.3 成功标准

| 维度 | 指标 | 目标值 | 度量方式 |
|------|------|--------|---------|
| 空闲功耗 | 静态库 10 分钟内额外 DB 事务数 | 趋近 0（仅到期根触发） | PG 日志 / `pg_stat_database` xact 计数 |
| 空闲功耗 | 空闲轮询 SELECT 频率 | 由固定 10s 降为动态/事件驱动 | 调度器日志 |
| 内存 | 整机常驻（Go+PG 容器） | ≤ 768MB（现限额），目标压至 ≤ 640MB | `docker stats` / cgroup memory.current |
| 正确性 | 同步/发布失败被记录并进入退避 | 100% 失败可见 | 任务状态表 `LastError` + 日志 |
| 性能 | 大库浏览/深分页 P95 | **待 NAS 实机压测设定** | 实机基准（见 §6.3） |
| 兼容 | 主流客户端直链播放成功率 | 建立兼容矩阵并标注降级路径 | 实机客户端回归 |

---

## 二、现状评估与约束

### 2.1 审计问题修复状态（基线 `af2706e` → `0548df9`）

| # | 审计问题 | 当前状态 | 证据 |
|---|---------|---------|------|
| P1-1 | 完整对账不识别源端删除 | **已修复** | `manual_stream.go` 新增 `reconcileManualMissing`，按 `last_seen_batch_id` 分批标记缺失 |
| P1-2 | 同步失败被吞、指纹提前推进 | **部分修复** | `SyncRootsFromManifest` 用 `errors.Join` 汇总；调度器仅在 `BatchSuccess` 推进指纹（`scheduler.go:313-317`）。**但** ticker 仍 `_ = s.RunTaskNow(...)` 丢弃错误（`scheduler.go:196`），无结构化日志、无退避 |
| P1-3 | 局部更新有整库快照开销 | **已修复** | 旧 JSON blob 改为 `scan_file_stamps` 行存储 + `scan_file_stage` UNLOGGED 暂存表，`commit()` 仅操作本次范围 |
| P1-4 | 流式读取后积累全量对象 | **已修复** | `manifest_sync.go` 先落 JSONL 临时文件，再按 200 条分页回放 |
| P2-5 | 空闲监控退化为每秒写库 | **部分修复** | 改为 10s ticker + `dueMonitorRoots` 门控，指纹未变时推进 `LastScannedAt`（`scheduler.go:297-300`）。**但** 每 10s 仍执行一次 `ListMonitorRoots` SELECT（`scheduler.go:180,191,261`） |
| P2-6 | 平铺剧集 Series 被 Season 覆盖 | **已修复** | `direct_publish.go` 为平铺集生成虚拟 `Season N` 路径；`libraries.type` 列名错误亦已修正为 `kind`（`direct_publish.go:77`） |

### 2.2 仍待改进（按域归类，详见 §四）

- **A 调度与空闲功耗**：固定 10s 轮询、ticker 丢错、多任务空转仍写状态。
- **B 错误可见性**：`direct_publish.go:77,79,120,136,178,229` 静默忽略错误；无指数退避。
- **C 数据库与容量**：PG 低内存参数缺口（`random_page_cost`/并行 worker/`synchronous_commit`/`autovacuum_work_mem`）；连接池文档缺失；深分页/`DISTINCT ON` 实机延迟无数据。
- **D 播放与客户端兼容**：缺兼容矩阵；直链 TTL/限流处理待验证；`/Shows/NextUp` 固定返回空需与 README 对齐。
- **E 可观测性与运维**：无 metrics/健康面板；备份恢复演练未文档化；`.env` 与持久化路径需复核。
- **F 安全**：公网暴露需反代+HTTPS+访问控制；302 目标宜签名/限时。

### 2.3 约束条件

- **硬件**：低功耗 NAS，ARM64 为主，RAM 256MB～2GB；磁盘可能为机械盘/网络挂载，随机 I/O 慢、需避免持续唤醒。
- **源端**：媒体来自远程小雅/AList（网盘），**非本地文件系统**。关键约束：fsnotify/inotify 在 NFS/SMB 等网络文件系统上**不工作**，且 fsnotify 无内置轮询降级（来源：fsnotify README FAQ + issue #9）。因此「清单驱动 + 定时轮询」是正确路线，不能指望文件通知。
- **客户端**：解码能力由客户端决定；服务器声明 `SupportsTranscoding: false`。
- **技术栈**：Go 单二进制 + go:embed 前端 + PostgreSQL 16（独立 alpine 容器）。

---

## 三、总体架构

### 3.1 架构图（ASCII，含改进点标注）

```
   ┌──────────────────────────────────────────────────────────────┐
   │  客户端 (Emby App / Infuse / VidHub / Kodi / TV)               │
   │  —— 承担视频解码；无法解码时会触发转码 → 302 失效 [改进点 D1]    │
   └───────────────┬──────────────────────────────▲───────────────┘
                   │ ① 浏览/播放请求 (Emby API)     │ ⑤ 直连拉流(Range/seek)
                   ▼                                │
   ┌──────────────────────────────────────────────┴───────────────┐
   │  xiaoya-media-server (Go 单二进制, GOMEMLIMIT=384MiB)          │
   │                                                                │
   │  Emby 兼容 API ── 302 直链解析器 ── STRM 引擎 ── 调度器[改进A] │
   │        │              │                  │            │        │
   │        │              │ ④ 302 redirect   │            │        │
   │        ▼              ▼                  ▼            ▼        │
   │  ┌───────────┐  ┌───────────┐   ┌────────────┐ ┌───────────┐  │
   │  │PostgreSQL │  │ AList/小雅 │   │ strm.txt   │ │  TMDB     │  │
   │  │ 业务库    │  │ (远程源)   │   │ 权威清单   │ │ 刮削      │  │
   │  │[改进点 C] │  └─────┬─────┘   └────────────┘ └───────────┘  │
   │  └───────────┘        │ ③ 解析真实下载直链(有TTL/限流)[改进D2] │
   │                       ▼                                        │
   │                 网盘真实下载地址 ──────────────────────────────┘
   └────────────────────────────────────────────────────────────────┘
                   │
                   ▼ ② 清单同步/对账 (errors.Join 汇总, 仅成功推进指纹)
              [改进点 B: 失败需结构化日志 + 指数退避]
```

### 3.2 核心组件与改进映射

| 组件 | 职责 | 现状 | 改进点 |
|------|------|------|--------|
| 调度器 `scheduler.go` | 9~10 个定时任务驱动同步/对账/刮削 | 10s 固定轮询 + ticker 丢错 | A1/A2/B1 |
| 直链解析 302 | 播放时重定向到网盘直链 | 声明不支持转码 | D1/D2 |
| PostgreSQL 容器 | 业务元数据/观看记录 | 已限 256M、shared_buffers=64MB | C1/C2 |
| 直接发布 `direct_publish.go` | 元数据入库 | 平铺集已修复，但多处静默忽略错误 | B2 |
| 清单同步 `manifest_sync.go` | strm.txt → 业务库 | 有界批处理已落地 | B1/B3 |

---

## 四、关键改进模块设计

### 模块 A：调度与空闲功耗优化

#### A1 现状问题
- `scheduler.go:179-181` 将 `monitor_root_scan` 的 ticker 硬编码为 **10 秒**；`dueMonitorRoots`（`:260-273`）每 10s 执行一次 `ListMonitorRoots` SELECT。虽然无到期根时 `continue`（`:192-194`）避免了任务执行与状态写入，但**空闲态仍每 10s 唤醒一次数据库**，与低功耗/休眠目标冲突。
- `scheduler.go:196` `_ = s.RunTaskNow(ctx, taskKey)` 丢弃返回错误：失败虽已持久化到任务状态表（`:239-256` 写 `FAILED` + `LastError`），但**无结构化日志、无连续失败退避**，下一轮仍按固定 `Interval` 重试。

#### A2 空闲检测选型矩阵

| 评估维度 | 方案①固定 10s 轮询（现状） | 方案②动态间隔轮询 | 方案③事件驱动(fsnotify) | 权重 |
|---------|--------------------------|------------------|------------------------|------|
| 空闲磁盘唤醒 | 差（每 10s 一次 SELECT） | 优（按最近到期时间休眠） | 优（无事件不唤醒） | 30% |
| 网络文件系统可用 | 优 | 优 | **不可用**（NFS/SMB 无通知，fsnotify 无轮询降级） | 25% |
| 实现复杂度 | 低（已有） | 中（计算 next-due，重置 ticker） | 高（递归 watch + inotify 上限 + 原子重命名丢 watch） | 20% |
| 追更及时性 | 优（≤10s） | 良（≤动态间隔，可设上限 60s） | 优 | 15% |
| 内存/FD 成本 | 低 | 低 | 高（每目录一 watch，inotify 上限随 RAM 缩放） | 10% |
| **综合得分** | **62** | **88** | **48** | — |

**结论**：选择**方案②动态间隔轮询**。理由：源端为远程网络文件系统，方案③事件驱动在 NFS/SMB 上根本不工作（fsnotify README FAQ 明确："NFS and SMB protocols does not provide network level support for file notifications"，且轮询 watcher "not yet implemented"），不可行；方案①空闲仍持续唤醒 DB。
**落地**：将 10s 固定 ticker 改为 `min(最近到期根的剩余时间, 60s)` 的动态 ticker；无到期根时拉长间隔（如 60s），有到期根临近时收紧。保留 `dueMonitorRoots` 门控。

#### A3 关键逻辑（伪代码）
```
loop:
  roots = ListMonitorRoots()            // 仅在 ticker 触发时查询
  nextDue = min over enabled roots of (LastScannedAt + IntervalSec - now)
  if nextDue <= 0:  RunTaskNow(monitor_root_scan)   // 记录并处理错误(见 B1)
  ticker.Reset(clamp(nextDue, 1s, 60s)) // 动态休眠，空闲时拉长到 60s
```

---

### 模块 B：同步/发布错误可见性与退避

#### B1 调度器错误处理
- 将 `scheduler.go:196` 改为记录错误并驱动退避：
```
if err := s.RunTaskNow(ctx, taskKey); err != nil {
    log.Warn("scheduled task failed", "task", taskKey, "err", err)
    s.backoff[taskKey] = min(s.backoff[taskKey]*2, maxBackoff)  // 指数退避
} else { s.backoff[taskKey] = baseInterval }
```
- 连续失败时按指数退避拉长 `NextRunAt`，避免对已故障的上游/DB 持续打满；恢复后重置。

#### B2 直接发布静默错误
- `direct_publish.go:77,79` 回退媒体库查询 `_ = ...Scan(&fallbackID)`：未命中时可能回退到错误媒体库且无日志。改为记录 warn 并在 fallbackID 为空时返回明确错误。
- `direct_publish.go:120,136,178,229` 的 `_, _ = tx.Exec(...)`（rating/metadata 写入）：虽在事务内，但失败会导致**部分发布**（条目入库但评分/元数据缺失）且无提示。改为检查错误，非关键写入失败时记录 warn 并标记该条目 `partial`，关键写入失败时回滚事务。

#### B3 退避与告警
- 为 `failed_resource_retry`、`tmdb_scrape_retry` 增加单资源连续失败计数与退避，避免对 429/超时的上游无限重试（呼应 go-wdd FAQ："访问太频繁/访问被阻断 → 增大请求间隔"）。

---

### 模块 C：PostgreSQL 低内存精调

#### C1 现状与缺口
当前 `compose.yaml` 已设：`shared_buffers=64MB`、`work_mem=4MB`、`maintenance_work_mem=32MB`、`effective_cache_size=128MB`、`max_connections=50`，容器限 256M。**未设**：`random_page_cost`、`max_parallel_workers_per_gather`、`synchronous_commit`、`autovacuum_work_mem`、`huge_pages`。

#### C2 参数选型矩阵（关键决策）

| 参数 | 现状/默认 | 建议值 | 依据（来源 + 条件） |
|------|----------|--------|--------------------|
| `shared_buffers` | 64MB | 保持 64MB（≤1GB RAM 时应低于 25%） | PG16 文档 runtime-config-resource："On systems with less than 1GB of RAM, a smaller percentage of RAM is appropriate"；25% 规则仅适用于 ≥1GB 专用库 |
| `max_connections` | 50 | **降至 12～16** | Go 侧连接池上限仅 6/空闲 2（审计记录）；`work_mem` 按「每连接每操作」叠加，50 连接是内存放大风险（PG16：total memory could be many times work_mem） |
| `work_mem` | 4MB | 保持 4MB（配合降 max_connections） | `hash_mem_multiplier` 默认 2.0，哈希操作可达 2×work_mem；小内存不宜上调 |
| `random_page_cost` | 默认 4.0 | **1.1～2.0** | PG16 runtime-config-query：NAS 网络延迟降低随机访问相对开销；库小于内存/高网络延迟时应调低，利于索引扫描 |
| `max_parallel_workers_per_gather` | 默认 2 | **0（禁用并行）** | PG16：每个 worker 独立套用 work_mem，4 worker 并行可达 5× CPU/内存/IO；低 RAM 硬件应禁用 |
| `autovacuum_work_mem` | 继承 32MB | **单独上限 16～32MB** | PG16：autovacuum 运行时最多 `autovacuum_max_workers`×该值，需单独封顶 |
| `synchronous_commit` | on | **off（可选，见矩阵）** | 业务库可由 strm.txt 清单重建，崩溃丢失最近数秒事务可接受，换取写入吞吐 |
| `effective_cache_size` | 128MB | 保持（纯规划器提示，无内存成本） | PG16："no effect on the size of shared memory allocated... used only for estimation" |
| `huge_pages` | try | 确保禁用 THP | PG16：Linux 透明大页"has been known to cause performance degradation with PostgreSQL" |

#### C3 `synchronous_commit` 决策矩阵

| 维度 | on（现状） | off |
|------|-----------|-----|
| 写入吞吐/延迟 | 较慢（每事务 fsync） | 快（批量回写） |
| 崩溃数据安全性 | 不丢已确认事务 | 可能丢最近 ~3×wal_writer_delay 的事务 |
| 本场景可接受性 | — | **可接受**：业务库为 strm.txt 清单的派生缓存，崩溃后可重新对账重建 |
| 空闲磁盘写入 | 每事务 fsync 唤醒磁盘 | 批量回写，减少唤醒 |

**结论**：低功耗 NAS 建议 `synchronous_commit=off`，以「可重建的派生数据」换取更低磁盘唤醒与更高写入吞吐；观看进度等不可重建数据如担心丢失，可对该表单独用 `SET LOCAL synchronous_commit=on`。

#### C4 连接池文档
- 在 `docs/DEPLOYMENT.md` 补充：Go 侧池 max 6/idle 2 与 PG `max_connections` 的关系；多实例部署时 `max_connections` 需 ≥ 实例数×6 + 运维预留，避免连接耗尽。

---

### 模块 D：播放客户端兼容与直链生命周期

#### D1 客户端兼容矩阵（需实机回归填充）
302 直通仅在客户端**能原生解码**编码/容器时成立；一旦触发转码，流量回落服务器、低功耗优势失效（来源：go-wdd README："转码不会 302……如果客户端触发了转码，流量仍会走 Emby"）。

| 客户端 | 直链 302 | 常见可解码 | 需转码/降级场景 | 实测状态 |
|--------|---------|-----------|----------------|---------|
| Infuse (iOS/TV) | 待验证 | H.264/H.265, MKV/MP4 | 部分字幕内封/特殊编码 | **需实机** |
| VidHub | 待验证 | H.264/H.265 | — | **需实机** |
| Emby 官方 App | 待验证 | 视平台 | 平台不支持的编码 | **需实机** |
| Kodi | 待验证 | 广 | — | **需实机** |
| 电视/盒子 | 待验证 | 窄 | 高码率/HEVC 10bit | **需实机** |

**落地**：建立兼容矩阵文档；对无法解码的客户端给出明确提示（而非静默失败）；评估是否对特定客户端提供「软链/本地代理」降级开关。

#### D2 直链 TTL 与上游限流
- 302 目标为网盘真实下载地址，存在**过期**与**访问频率限制**风险（来源：go-wdd："缓存时间（秒）控制直链缓存"、"接口请求间隔过小更易触发访问频率限制"、FAQ"访问太频繁/访问被阻断"）。
- **改进**：验证小雅 driver 对直链 TTL 的处理；播放返回 403/过期时触发**即时重解析**而非缓存旧链；对上游解析请求做**间隔限制 + 429 退避**（与 TMDB 250ms 限速、429 退避一致的机制复用）。

#### D3 字幕与 seek
- 外挂字幕 WebVTT 转换已具备；需实机验证 302 重定向后 **Range/seek** 在各客户端正常（拖动进度条不重新缓冲整段）。

#### D4 `/Shows/NextUp`
- 当前固定返回空（Git `a57f986` 有意移除）。**改进**：README/文档与行为对齐（审计已指出）；如为产品取舍，明确标注「暂不支持下一集推荐」；如计划恢复，列入 P2 功能增强。

---

### 模块 E：可观测性与运维

- **E1 轻量 metrics**：暴露 `/metrics`（Prometheus 文本格式）或管理台面板，关键指标：空闲轮询频率、各任务上次状态/耗时/连续失败数、同步滞后（清单指纹 vs 已入库）、302 解析成功率/延迟、PG 连接数。低功耗 NAS 上指标采集本身需轻量（拉取式，避免常驻推送）。
- **E2 备份恢复**：`backup.go` 已具备；补充**定时备份 + 恢复演练文档**，明确 PostgreSQL 数据目录与 `MEDIA_INFO_ROOT` 持久化路径（审计指出 `/app/data` 默认 vs Compose 持久化 `/data` 的漂移，需统一）。
- **E3 配置漂移复核**：`compose.yaml` 与 `.env.example` 已对齐 TMDB 全量变量；需复核**生产 `.env`** 是否同步，以及 `POSTGRES_DATA_DIR` 路径一致性。

---

### 模块 F：安全加固

- **F1 暴露面**：服务为 Emby 兼容入口，若对公网开放需「反向代理 + HTTPS + 访问控制（白名单/VPN）」（来源：go-wdd 安全建议："等同于 Emby 的外网入口，建议配合反代、HTTPS、访问控制再对公网开放"）。现状 `ADMIN_PASSWORD` 强制 ≥12 位、`BOOTSTRAP_API_KEY` 机制良好。
- **F2 302 目标**：直链宜**签名 + 限时**，防止重定向目标被滥用/枚举；`STRM_TARGET_BASE_URL` 占位地址需确保不被外部直接利用。

---

## 五、技术选型汇总

| 决策点 | 候选 | 结论 | 理由 |
|--------|------|------|------|
| 业务数据库 | PostgreSQL 16 / SQLite | **保留 PostgreSQL** | 审计已结论：清单解析器/热目录 SQLite 任务与旧文档并存，但主链路为 PG；更换数据库不如消除全量操作直接。PG 独立 alpine 容器 + 低内存参数已可控 |
| 空闲检测 | 固定轮询 / 动态轮询 / 事件驱动 | **动态间隔轮询** | 事件驱动在 NFS/SMB 不可用（fsnotify 限制）；固定轮询空闲仍唤醒 DB（见 §4.A2 矩阵） |
| `synchronous_commit` | on / off | **off（派生数据）** | 业务库可由 strm.txt 重建，换取低磁盘唤醒与高吞吐（见 §4.C3） |
| 并行查询 | 启用 / 禁用 | **禁用（per_gather=0）** | 低 RAM 下每 worker 独立套 work_mem，并行放大内存/IO（PG16 文档） |
| 播放路径 | 302 直通 / 服务端转码 | **302 直通** | 低功耗 NAS 核心优势；转码会使直通失效、流量回落（go-wdd） |

---

## 六、性能与容量

### 6.1 内存预算（现状，来源：compose.yaml + 审计）

| 组件 | 限制/配置 | 评估 |
|------|----------|------|
| Go 进程 | `GOMEMLIMIT=384MiB`, `GOGC=80`, 容器限 512M | 合理；`debug.FreeOSMemory()` 周期归还 |
| PostgreSQL | `shared_buffers=64MB`, 容器限 256M, `max_connections=50` | 低内存调优到位，但 max_connections 偏大（建议降至 12～16） |
| **整机容器限额** | **768MB** | 对 ≥2GB NAS 可接受；1GB 机器建议按 §4.C 压缩 |

### 6.2 待实测项（**暂无可靠基准数据，建议 NAS 实机压测**）
- 审计中 40 万条目测试在 ARM64 受限环境 4 分钟超时（停在构造目录 INSERT 阶段），**深分页与 `DISTINCT ON` 合并查询的实际延迟仍无数据**。
- 真实普通用户列表默认进入 `mergeWhere` 的 `DISTINCT ON` 去重（`versions.go:19-34`、`main.go:1531-1533`），需测默认合并、总数统计、StartIndex 随机跳页、连续游标分页。

### 6.3 压测方案（NAS 实机）
| 场景 | 指标 | 工具/方法 |
|------|------|----------|
| 空闲态 | CPU%、RSS、10min 内 DB xact 数、磁盘写入 | `docker stats` + `pg_stat_database` + iostat |
| 大库浏览 | 首页/搜索/深分页 P50/P95 | 实机客户端 + 服务端计时日志 |
| 导入/追更 | 峰值内存、WAL 量、耗时 | 受控清单变更 + cgroup memory.peak |
| 播放 | 302 解析延迟、直连拉流稳定性、seek | 多客户端实机回归 |
| 故障收敛 | 源端删除/恢复/同步失败/重启后状态 | 注入故障 + 状态表核对 |

---

## 七、安全与合规

- 管理员密码强度（≥12 位）与 API Key 引导已具备。
- 公网暴露需反代 + HTTPS + 访问控制（§4.F1）。
- 302 直链签名/限时（§4.F2）。
- 业务库含观看记录等用户数据，备份需加密存储、权限受控。

---

## 八、风险与降级

| 风险 | 概率 | 影响 | 降级方案 | 负责人 |
|------|------|------|---------|--------|
| 客户端触发转码，302 直通失效、流量回落服务器 | 中 | 严重 | 建立兼容矩阵；对不可解码客户端明确提示；评估软链/本地代理降级开关 | 后端 |
| 直链过期/上游限流导致播放失败 | 高 | 中 | 403/过期即时重解析；上游请求间隔限制 + 429 退避 | 后端 |
| 同步/发布失败静默、无退避，持续打满故障上游 | 中 | 中 | B1 结构化日志 + 指数退避；B2 检查并标记 partial | 后端 |
| 空闲轮询持续唤醒磁盘，违背低功耗目标 | 高 | 中 | A2 动态间隔轮询（空闲拉长至 60s） | 后端 |
| PG `max_connections=50` × `work_mem` 内存放大，小内存 OOM | 中 | 严重 | C2 降 max_connections 至 12～16、禁用并行、封顶 autovacuum_work_mem | DBA/后端 |
| 大库深分页延迟未知，实机可能卡顿 | 中 | 中 | §6.3 实机压测后设定目标并优化索引/查询 | 后端 |
| 公网暴露无防护被滥用 | 低 | 严重 | F1 反代+HTTPS+白名单/VPN；F2 直链签名限时 | 运维 |
| 配置/持久化路径漂移导致重建丢数据 | 中 | 中 | E2/E3 统一持久化路径、复核生产 .env、备份恢复演练 | 运维 |

---

## 九、实施计划

| 阶段 | 范围 | 交付物 | 负责人 |
|------|------|--------|--------|
| **P0 正确性 + 空闲功耗** | A2 动态轮询、B1 调度器错误日志+退避、B2 直接发布错误检查 | 代码 + 单测；空闲态 DB xact 趋近 0 验证 | 后端 |
| **P1 DB 调优 + 兼容 + 可观测** | C2/C3 PG 参数、C4 连接池文档、D1 兼容矩阵、D2 直链 TTL/退避、E1 metrics | compose 调优 + 兼容矩阵文档 + metrics 端点 | 后端/DBA |
| **P2 功能增强 + 安全 + 实机验收** | D4 NextUp 取舍、F1/F2 安全加固、§6.3 NAS 实机压测 | 实机基准报告 + 安全加固 + 文档对齐 | 后端/运维 |

**完成标准**：实际客户端能顺畅浏览与持续播放，后台更新正确且资源有界，空闲态不持续唤醒磁盘，失败可见可退避。保留现有 302 播放与功能回归，不为降低表面内存而牺牲观看记录、缺失保护或错误可见性。

---

## 附录：引用来源

- fsnotify 网络文件系统限制与无轮询降级：fsnotify README FAQ + issue #9（github.com/fsnotify/fsnotify）。
- 302 直链/STRM 低功耗模式、转码使直通失效、直链缓存与限流、安全建议：go-wdd 项目 README（github.com/fengxinzi-mulan/go-wdd，社区项目，非 Emby 官方文档）。
- PostgreSQL 16 低内存调优（shared_buffers <1GB 规则、work_mem 叠加、effective_cache_size 仅提示、max_connections、random_page_cost NAS 延迟、并行 worker 内存放大、autovacuum_work_mem、THP、synchronous_commit）：postgresql.org/docs/16 runtime-config-resource / runtime-config-query / runtime-config-connection。
- 代码现状与审计修复状态：本仓库 `docs/NAS-PERFORMANCE-FUNCTION-AUDIT.md`、`internal/scheduler/scheduler.go`、`internal/embyserver/direct_publish.go`、`deployments/compose.yaml`（基线 `0548df9`）。
- 标注「需实机/暂无可靠基准数据」处：审计 40 万条目测试在 ARM64 受限环境超时，深分页/合并查询延迟无实测结论。
