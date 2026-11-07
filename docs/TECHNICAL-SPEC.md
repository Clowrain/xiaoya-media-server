# Xiaoya Media Server 完整技术方案

**版本：V1.0 方案基线**  
**日期：2026-11-07**

> 本方案依据目前讨论、用户确认的业务规则及已执行的 Xiaoya SQLite 诊断结果整理。go-emby 源码尚未完成逐文件审计，因此本文不会假设其内部模块、接口覆盖或数据库实现已经得到验证。

## 1. 项目定位与目标

以用户已确认可用于二次开发的 go-emby 当前可获得源码为基础，建设统一的 Xiaoya Media Server。将媒体服务器、Emby 兼容 API、媒体库扫描、TMDB/NFO、播放能力，与 Xiaoya/AList 资源发现、增量同步、STRM 生成和人工入库融合在同一服务中。

目标能力：

- Emby 兼容客户端接入，重点覆盖 VidHub、SenPlayer 及现有实际使用的客户端。
- 用户、设备、媒体库、电影、电视剧、季、集、媒体详情管理。
- TMDB 元数据识别、刮削、海报、背景图和演员图片管理。
- NFO、字幕及图片附件管理。
- 播放地址解析、直连/重定向播放、播放进度和继续观看。
- Xiaoya SQLite 指定目录监控、资源快照和变更历史。
- 按原始 `strm.py`、`strm2.py` 规则生成自有 STRM。
- AList 浏览、搜索、单文件或整目录主动入库。
- 统一后台、任务调度、日志和运行状态。

V1.0 优先复用 go-emby 已有且可验证的能力；对源码缺失或闭源部分，先列明边界，再决定自主实现。

## 2. 已确认决策

| 事项 | 决策 |
|---|---|
| 基础代码 | go-emby 当前可获得源码，用户已确认可二次开发 |
| 产品形态 | 媒体服务器与资源管理融合为一个服务 |
| Xiaoya DB | 只读访问，不修改其表结构 |
| 自动监控 | 只扫描用户指定目录 |
| 初始目录 | `/每日更新/电视剧/国产剧（115）`、`/每日更新/电视剧/国产剧` |
| 电视剧入库 | 整部目录入库，自动跟踪后续剧集 |
| NFO | `prefer`：有 NFO 优先同步使用，无 NFO 由媒体服务器刮削 |
| AList 资源删除 | `keep`：标记失效，保留 STRM、NFO、字幕、图片 |
| STRM | 完整参考 `strm.py`、`strm2.py`，不得简化为直接写入 DB content |
| 媒体扩展名 | 两份脚本媒体扩展名并集 |
| 附件 | 字幕完整处理；按原脚本规则处理图片和 NFO |
| 扫描 | 递归 |
| 输出 | 自定义输出根目录，按配置保留源相对路径 |
| 元数据 | 由融合后的媒体服务器引擎负责 TMDB/NFO/图片 |
| 资源入口 | 自动监控和人工入库共用同一资源模型 |

## 3. 已验证的 Xiaoya SQLite 信息

数据库路径：

```text
/vol1/docker/volumes/2ddce7161a56adcc62c175c9a565b3a4e54cb99047741c35dd9866e7ff44ef63/_data/strm_internal.db
```

已验证表结构：

```sql
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    parent_id INTEGER NOT NULL DEFAULT 0,
    is_dir BOOLEAN NOT NULL DEFAULT 0,
    content TEXT NOT NULL DEFAULT ''
);
```

已知事实：

- 当前诊断时节点约 879,799 条，最大 ID 879,798。
- `id` 为自增主键；`parent_id` 表示父子关系；`is_dir` 区分目录和文件。
- `content` 在已检查的 `.strm` 节点中保存播放 URL。
- `nodes` 没有 `created_at`、`updated_at`。
- 节点 ID 可辅助发现新增，不能证明真实新增日期，也不能检测已有节点内容变化。
- 服务应只读访问 SQLite，不改变 Xiaoya 数据。

### 3.1 折月亮验证样本

用户输入路径 `/每日更新/电视剧/国产剧/折月亮`。数据库实际目录名为 `折月亮 (2026)`：

```text
每日更新                   ID 568922
└── 电视剧                 ID 617762
    └── 国产剧              ID 639780
        └── 折月亮 (2026)   ID 641716
            └── {tmdb-270431} ID 641717
                └── Season 1 ID 641718
                    ├── E01 ID 641719
                    ├── E02 ID 641720
                    ├── ...
                    └── E27 ID 641745
```

递归查询共 30 个节点：3 个目录、27 个文件。目录标识提供 TMDB ID `270431`。已观察到的 `content` 示例：

```text
http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/01.mp4?sign=...
```

此结果证明样本节点的 `content` 包含播放 URL。签名有效期、刷新方式和实际播放兼容性尚待验证。不要将脱敏示例当作真实 URL。

## 4. 总体架构

```text
Clients (VidHub / SenPlayer / Emby clients)
                  |
          Emby-compatible API
                  |
        Xiaoya Media Server
  ┌───────────────┼─────────────────┐
  |               |                 |
Media Server   Resource Engine   Admin/Scheduler
  |               |
  |          Xiaoya SQLite Reader (read-only)
  |          AList Browser/Search Adapter
  |          Change Detection
  |          STRM Generator
  |          NFO/Subtitle/Image Sync
  |
TMDB / NFO / Images / Playback / Library
                  |
          Unified Business DB
             PostgreSQL
```

逻辑模块：

| 模块 | 职责 |
|---|---|
| go-emby 基础模块 | Emby API、媒体服务器现有能力，待源码审计 |
| Xiaoya Reader | 只读查询节点和目录树 |
| Discovery | 监控目录扫描、快照、差异检测 |
| Resource Manager | 自动监控、人工入库、资源状态 |
| STRM Engine | 兼容 Python 原脚本的生成逻辑 |
| Attachment Engine | NFO、字幕、图片同步 |
| Media Engine | 媒体识别、媒体库索引 |
| Metadata Provider | TMDB、NFO、图片 |
| Playback | 播放地址解析、直连/重定向、进度 |
| AList Adapter | 浏览、搜索和必要的文件访问 |
| Scheduler | 定时扫描、重试和任务状态 |
| Admin | 配置、资源、批次、任务和日志 |

目标是一个统一服务，不再额外部署独立 go-emby 服务和资源管理服务。数据库最终是否采用独立 PostgreSQL、是否迁移原 go-emby 数据库，须先审计原项目再定。

## 5. 功能范围

### 5.1 Emby 兼容媒体服务器（P0）

基于 go-emby 当前源码迁移、保留并增强：

| 功能 | 需求 | 当前状态 |
|---|---|---|
| Emby REST API | 客户端所需接口兼容 | 待源码审计 |
| 用户认证 | 登录、Token、用户信息 | 待源码审计 |
| 设备管理 | 客户端设备识别和会话 | 待源码审计 |
| 媒体库 | 媒体库列表及访问权限 | 待源码审计 |
| Items API | 电影、电视剧、季、集详情 | 待源码审计 |
| 播放 API | 播放信息、媒体源、重定向 | 待源码审计 |
| 播放进度 | 开始、进度、停止事件 | 待源码审计 |
| 继续观看 | Resume、Next Up 等体验 | 待源码审计 |
| 字幕 | 外挂字幕识别和客户端接口 | 待源码审计 |
| 用户权限 | 用户及媒体库权限 | 待源码审计 |
| 管理能力 | 用户、媒体库、任务和配置 | 待源码审计 |

验收以真实客户端回归为准，至少覆盖登录、媒体库浏览、详情、播放、进度上报和继续观看。具体 API 范围不能在未审计代码前假定完整。

### 5.2 媒体库和扫描（P0）

- 支持电影、电视剧、季、集层级。
- 支持本地媒体目录及 STRM 目录。
- 支持手动全库扫描和单项目刷新。
- 支持任务状态、错误日志和重试。
- 扫描结果进入统一媒体模型。
- 来源资源发现与媒体元数据识别分离。
- 同一媒体可以关联多个来源、多个版本和多个文件。

### 5.3 TMDB 与元数据（P0）

- TMDB API 配置和连通性测试。
- 通过 TMDB ID 获取电影、电视剧元数据。
- 无 ID 时按名称、年份搜索并允许人工确认。
- 支持季、集元数据。
- 支持海报、背景图、演员图片。
- 支持本地 NFO 优先。
- 支持重新匹配、刷新元数据和图片。
- 刮削失败不应阻断 STRM 资源入库。

`{tmdb-270431}` 可直接提供折月亮的 TMDB ID。

### 5.4 NFO、图片和字幕（P0）

采用 `prefer`：

1. AList 有 NFO：同步原始 NFO，媒体服务器优先读取。
2. NFO 内容变化：通过 Hash 检测并更新本地文件。
3. 没有 NFO：媒体服务器执行 TMDB 匹配和刮削。
4. 不创建空 NFO。
5. 支持完整字幕附件规则。
6. 支持原脚本规定的图片格式。
7. 附件需关联到媒体、季、集或对应目录。
8. 附件同步失败可独立重试。

附件匹配与目录布局以 `strm.py`、`strm2.py` 实际逻辑为准。

### 5.5 Xiaoya SQLite 资源发现（P0）

- 只读连接 `strm_internal.db`。
- 支持多个监控根目录。
- 按 `parent_id` 递归获取节点。
- 批量读取，避免全表无约束加载。
- 保留源节点 ID、父节点 ID、名称、is_dir、content。
- 生成快照 Hash。
- 识别新增、修改、改名、移动和删除。
- 保存首次发现时间、最近发现时间和变更时间。
- 支持扫描批次、断点恢复和失败重试。
- 不修改 Xiaoya 数据库结构。

初始配置：

```yaml
monitor:
  enabled: true
  interval: 10m
  roots:
    - /每日更新/电视剧/国产剧（115）
    - /每日更新/电视剧/国产剧
```

路径需要精确匹配和规范化。目录不存在或出现重名歧义时，必须报告错误，不得静默匹配错误节点。

### 5.6 变更检测与时间记录（P0）

由于 SQLite 无时间字段，由本服务记录：

- `first_seen_at`：本服务首次发现时间。
- `last_seen_at`：最近一次完整扫描发现时间。
- `last_changed_at`：本服务检测到变化的时间。
- `source_node_id`、`source_parent_id`。
- `snapshot_hash`、`content_hash`、`name_hash`、`path_hash`。

规则：

- 节点 ID 是新增检测的辅助信息，不足以识别修改。
- 必须比较 name、parent_id、path、is_dir、content 等字段。
- 目录改名或移动需要更新子节点路径映射。
- 只有完整成功扫描才能判定删除。
- 首次扫描建立基线，不推断历史创建日期。
- 通过本服务批次历史才能准确回答“昨天新增了哪些资源”。

### 5.7 STRM 生成（P0）

必须完整迁移 `strm.py` 和 `strm2.py` 规则。不能将 SQLite `content` 直接写入 STRM 视为完成。

需逐项对照并兼容：

- 两份脚本媒体扩展名并集。
- 递归扫描。
- 自定义输出根目录和源相对路径映射。
- URL 替换规则。
- URL 编码规则。
- 文件命名规则。
- 235 字符限制及冲突处理。
- NFO、字幕、图片附件处理。
- 中文、空格、`#`、`&`、`+` 等特殊字符。
- 已编码 URL，避免错误二次编码。
- DB 中 `.strm` 节点的处理，防止 `.strm.strm`。
- Hash 增量判断、原子写入、失败恢复。

配置方向：

```yaml
strm:
  output_root: /media/strm
  preserve_source_structure: true
  max_path_length: 235
  media_extensions: all
  attachments: full
  recursive: true
```

最终行为以原脚本为兼容基准。必须用相同输入对比 Python 和 Go 输出，规则对照表和测试样本作为交付物。

### 5.8 AList 浏览和主动入库（P1）

- 浏览 AList 目录。
- 按名称搜索资源。
- 查看路径、类型、大小等可用信息。
- 单个媒体文件入库。
- 整部电视剧目录入库。
- 取消管理、重新处理和批量操作。
- 手动入库和自动监控共用同一资源模型。

职责建议：SQLite 用于自动监控索引；AList API 用于浏览、搜索和需要额外信息的场景；WebDAV 只在确实需要文件内容访问时作为传输适配器。最终边界待接口验证。

### 5.9 删除和恢复（P0）

采用 `keep`：

- 源资源消失时标记 `MISSING`。
- 保留 STRM、NFO、字幕、图片。
- 保留媒体实体和观看历史。
- 不自动删除媒体库记录。
- 资源恢复后允许恢复状态。
- 人工清理需展示影响范围并要求确认。

### 5.10 播放链路（P0）

```text
Client
 -> Emby-compatible Playback API
 -> 查询媒体和文件
 -> 读取 STRM
 -> 解析播放 URL
 -> 校验或获取可用地址
 -> 直连或 HTTP 重定向
 -> AList/网盘资源
 -> Client 播放
```

必须验证签名 URL 有效期、过期后的重新获取方式、Range/HEAD 请求、302 行为、字幕访问、播放进度和客户端兼容。不能假设 SQLite 中的签名 URL 永久有效。

---

## 6. 统一数据模型建议

以下是逻辑实体，不是未经审计即可直接执行的建表脚本。优先评估 go-emby 现有数据模型和迁移机制。

| 实体 | 职责 |
|---|---|
| `monitor_roots` | 自动监控目录 |
| `sync_batches` | 扫描批次 |
| `sync_cursors` | 目录扫描游标 |
| `media_resources` | AList/Xiaoya 来源资源 |
| `managed_resources` | 自动监控或人工入库关系 |
| `resource_change_history` | 资源变更历史 |
| `media_items` | 电影、电视剧、季、集 |
| `media_files` | STRM、本地文件和版本 |
| `media_attachments` | NFO、字幕、图片 |
| `metadata` | TMDB 等元数据 |
| `media_images` | 海报、背景、人物图片 |
| `users` | 用户 |
| `devices` | 客户端设备 |
| `libraries` | 媒体库 |
| `playback_sessions` | 播放会话 |
| `playback_progress` | 播放进度 |
| `scheduled_tasks` | 后台任务 |

关键设计：`media_resources` 表示来源资源；`media_items` 表示媒体库实体；`media_files` 表示媒体实体关联的文件。一个媒体实体可以关联多个来源和版本，避免同一电视剧重复建档。

`media_resources` 建议包含 `source_node_id`、`source_parent_id`、`source_path`、`name`、`is_dir`、`source_content`、Hash、首次发现时间、最后发现时间、状态等字段。

---

## 7. 项目结构建议

下列是目标模块，不代表原仓库当前已经采用这些目录。实际重构以源码审计结果为准。

```text
xiaoya-media-server/
├── cmd/server/
├── internal/
│   ├── embyapi/
│   ├── auth/
│   ├── device/
│   ├── library/
│   ├── media/
│   ├── metadata/
│   │   ├── tmdb/
│   │   ├── nfo/
│   │   └── images/
│   ├── playback/
│   ├── subtitle/
│   ├── resource/
│   │   ├── xiaoya/
│   │   ├── alist/
│   │   ├── discovery/
│   │   ├── sync/
│   │   └── managed/
│   ├── strm/
│   │   ├── scanner/
│   │   ├── generator/
│   │   ├── encoder/
│   │   ├── pathmapper/
│   │   └── attachment/
│   ├── scheduler/
│   ├── admin/
│   ├── repository/
│   └── database/
├── migrations/
├── web/
├── configs/
├── deployments/
└── docs/
```

---

## 8. 配置与部署

配置方向：

```yaml
server:
  listen: "0.0.0.0:8096"

database:
  driver: postgres
  dsn: ""

xiaoya:
  sqlite_path: /xiaoya-db/strm_internal.db
  read_only: true

monitor:
  enabled: true
  interval: 10m
  roots:
    - /每日更新/电视剧/国产剧（115）
    - /每日更新/电视剧/国产剧

strm:
  output_root: /media/strm
  recursive: true
  media_extensions: all
  attachments: full
  preserve_source_structure: true

alist:
  base_url: http://xiaoya.host:5678

metadata:
  tmdb:
    enabled: true
    api_key: ""

playback:
  mode: redirect
```

配置字段、端口、数据库和播放模式须在源码审计和环境验证后定稿。密钥通过环境变量或 Secret 管理，不写入仓库。

容器挂载至少包括：

- Xiaoya SQLite 数据及必要的 WAL/SHM 可见性，挂载为只读。
- STRM 输出目录。
- 媒体元数据和图片目录。
- 配置和密钥目录。
- PostgreSQL 数据目录（若采用独立数据库容器）。

SQLite 采用 WAL 时，必须验证读取一致性；不能在不确认 WAL 状态的情况下只复制主 DB 文件作为有效快照。

---

## 9. 定时任务

初始建议频率，全部可配置：

| 任务 | 初始频率 | 作用 |
|---|---|---|
| 指定目录扫描 | 10 分钟 | 发现资源变化 |
| 增量节点检查 | 10 分钟 | 辅助发现新增 |
| 附件差异同步 | 30 分钟 | 同步变化附件 |
| 完整差异校验 | 每天 | 识别移动和删除 |
| STRM 一致性检查 | 每天 | 检查缺失和内容变化 |
| 媒体索引/任务检查 | 每 30 分钟 | 检查后台处理状态 |
| 失败任务重试 | 可配置 | 重试失败任务 |

运行约束：

- 同一根目录禁止冲突的并发完整扫描。
- 每次扫描创建批次。
- 只有完整成功的扫描才能推进成功游标。
- 扫描异常不能触发删除。
- 文件写入采用临时文件和原子替换。
- 任务幂等且可恢复。
- 签名 URL 失效与源资源删除必须区分。

---

## 10. 管理 API 建议

具体路径应先检查 go-emby 现有路由，避免冲突。以下为自研资源管理接口建议：

| API | 用途 | 优先级 |
|---|---|---|
| `GET /api/monitor/roots` | 监控目录列表 | P0 |
| `POST /api/monitor/roots` | 添加监控目录 | P0 |
| `PUT /api/monitor/roots/{id}` | 修改监控目录 | P0 |
| `DELETE /api/monitor/roots/{id}` | 停止监控 | P0 |
| `GET /api/resources` | 资源列表 | P0 |
| `GET /api/resources/{id}` | 资源详情 | P0 |
| `GET /api/resources/changes` | 变更历史 | P0 |
| `GET /api/batches` | 扫描批次 | P0 |
| `POST /api/sync/full` | 全量扫描 | P0 |
| `POST /api/sync/incremental` | 增量扫描 | P0 |
| `POST /api/resources/{id}/retry` | 重新处理 | P0 |
| `GET /api/alist/browse` | AList 浏览 | P1 |
| `GET /api/alist/search` | AList 搜索 | P1 |
| `POST /api/resources/import` | 人工入库 | P1 |
| `POST /api/resources/{id}/reprocess` | 重新生成 STRM/附件 | P1 |
| `GET /api/tasks` | 任务列表 | P0 |
| `GET /api/system/health` | 健康检查 | P0 |

Emby 兼容接口优先沿用 go-emby 当前实现，不随意改变协议和响应结构。

---

## 11. 验收标准

### 11.1 P0：媒体服务器

- 完成源码功能清单和依赖图。
- 新服务可以启动。
- 目标客户端能够登录。
- 能够获取媒体库和媒体详情。
- 能够浏览电影、电视剧、季、集。
- 能够播放 STRM 资源。
- 能够上报播放进度。
- 继续观看和观看历史通过客户端回归。
- 用户、设备和权限通过测试。

### 11.2 P0：资源发现

- 只读连接 Xiaoya 数据库。
- 按配置路径逐级定位。
- 递归获取所有层级。
- 正确构建父子关系。
- 读取并保存 content。
- 生成资源快照和批次。
- 识别新增、修改、移动和删除。
- 失败不推进成功游标。
- 首次扫描不伪造历史时间。

### 11.3 P0：STRM

- 完成两份 Python 脚本规则映射。
- 同一输入下 Python/Go 输出符合兼容要求。
- 中文、空格、特殊字符处理一致。
- URL 编码、替换规则一致。
- 文件名限制和冲突处理正确。
- NFO、字幕、图片处理一致。
- 不生成 `.strm.strm`。
- 重复扫描不无意义重写。
- 资源变化时正确更新。

### 11.4 P0：元数据和删除策略

- 有 NFO 优先读取。
- 无 NFO 能执行 TMDB 匹配。
- 能提取 TMDB ID 270431。
- 海报、背景、字幕关联正确。
- 源资源消失后标记 MISSING。
- STRM、NFO、字幕、图片保留。
- 媒体实体和观看历史保留。
- 资源恢复后可以恢复状态。
- 不完整扫描不触发删除。

### 11.5 折月亮端到端测试

以真实样本验证：

```text
实际目录名：折月亮 (2026)
根节点 ID：641716
TMDB ID：270431
目录节点总数：30
文件节点：27
剧集：E01–E27
```

步骤：

1. 配置监控目录并定位实际名称。
2. 递归读取 30 个节点。
3. 识别 TMDB ID 270431。
4. 识别 Season 1 和 E01–E27。
5. 按原 Python 规则生成 STRM。
6. 检查输出文件名和播放 URL。
7. 检查 NFO、字幕、图片。
8. 媒体服务器建立电视剧、季、集关系。
9. TMDB 元数据正确关联。
10. 客户端浏览和播放。
11. 再次扫描无重复媒体实体和无意义重写。
12. 模拟新增一集，自动发现并生成。
13. 模拟 content 变化，正确更新 STRM。
14. 模拟资源消失，标记 MISSING 并保留本地文件。

### 11.6 P1：主动入库

- AList 目录浏览。
- AList 搜索。
- 单文件入库。
- 整部电视剧目录入库。
- 已入库电视剧后续新增剧集自动跟踪。
- 资源、批次、任务和错误可检索。

---

## 12. 里程碑

| 阶段 | 内容 | 交付 |
|---|---|---|
| M0 | go-emby 源码审计 | 源码审计报告、依赖图、功能清单 |
| M1 | 迁移媒体服务器基础能力 | 可启动服务、Emby API 回归结果 |
| M2 | 媒体库、扫描和元数据 | 电影/电视剧、TMDB/NFO 测试 |
| M3 | 播放链路 | 客户端播放和进度测试 |
| M4 | SQLite Reader 和资源快照 | 指定目录扫描结果 |
| M5 | 增量变化检测 | 新增、修改、移动、删除测试 |
| M6 | STRM 兼容迁移 | Python/Go 对照报告 |
| M7 | 附件和媒体库融合 | NFO、字幕、图片及端到端测试 |
| M8 | AList 浏览和主动入库 | 搜索、单文件/目录入库 |
| M9 | 管理界面和部署完善 | Docker、运维文档、V1.0 验收 |

M0 完成后应根据源码真实情况调整 M1–M3 的实施顺序。资源引擎和媒体服务器迁移可以并行开发，但必须通过集成测试。

---

## 13. 待确认事项

| 编号 | 事项 | 原因 | 确认时点 |
|---|---|---|---|
| C01 | go-emby 仓库、分支和 commit | 固定源码基线 | M0 |
| C02 | 源码实际功能和闭源依赖 | 确定可迁移范围 | M0 |
| C03 | 第三方依赖许可 | 明确使用和分发边界 | M0 |
| C04 | 原项目数据库、schema、迁移机制 | 决定数据库融合方案 | M0 |
| C05 | 是否迁移现有用户、媒体库和播放数据 | 决定迁移范围 | M0 |
| C06 | Emby API 兼容范围 | 以真实客户端请求确定 | M1 |
| C07 | 是否要求兼容 Emby 官方客户端全部核心功能 | 影响工作量 | M1 |
| C08 | 播放采用原重定向还是增加代理播放 | 影响播放架构 | M3 |
| C09 | SQLite content 签名有效期和续期机制 | 影响长期播放 | M3/M5 |
| C10 | AList API 与 WebDAV 的职责边界 | 避免重复实现 | M4/M8 |
| C11 | `strm.py`、`strm2.py` 最终版本 | STRM 兼容基准 | M6 前 |
| C12 | 两份脚本规则差异和覆盖优先级 | 决定兼容细节 | M6 前 |
| C13 | STRM 输出路径和容器内挂载路径 | 保证媒体服务器可见 | M6 |
| C14 | NFO、字幕、图片实际目录布局 | 确定附件关联 | M7 |
| C15 | TMDB Key、语言和区域偏好 | 元数据配置 | M2 |
| C16 | 用户、设备、播放历史迁移范围 | 数据迁移方案 | M1/M3 |
| C17 | 是否复用 go-emby 现有前端 | 决定 UI 工作量 | M0 |
| C18 | 是否需要多用户和媒体库隔离 | 权限模型 | M1 |
| C19 | 资源移动后旧 STRM 的处理方式 | 避免重复和断链 | M5 |
| C20 | 性能目标、扫描批量大小和频率 | 决定优化策略 | M4 |

建议默认：保留可迁移的现有用户和媒体数据；先保证 VidHub/SenPlayer 核心体验；先复用可用的重定向播放；PostgreSQL 作为统一业务数据库候选，待审计后定案；资源移动先保留映射，不自动清理旧 STRM。

---

## 14. 待办清单

### T0：启动前资料

- [ ] 获取 go-emby 最新源码。
- [ ] 记录仓库、分支、commit 和获取日期。
- [ ] 获取 `strm.py`、`strm2.py` 原文件。
- [ ] 获取当前 go-emby 部署配置和环境变量。
- [ ] 获取原数据库结构或备份。
- [ ] 归档依赖许可。
- [ ] 建立 go-emby 原版可运行基线。
- [ ] 使用 VidHub/SenPlayer 记录客户端回归流程。

### T1：源码审计

- [ ] 绘制模块依赖图。
- [ ] 列出 Emby API 路由。
- [ ] 梳理用户、设备和认证流程。
- [ ] 分析媒体库扫描。
- [ ] 分析 TMDB、NFO、图片和字幕。
- [ ] 分析 STRM 解析和播放重定向。
- [ ] 分析播放进度和继续观看。
- [ ] 分析数据库模型、索引和迁移。
- [ ] 标记缺失、闭源依赖和不可复用部分。
- [ ] 输出 `GO-EMBY-SOURCE-AUDIT.md`。

### T2：媒体服务器迁移

- [ ] 建立新项目仓库和分支策略。
- [ ] 迁移基础服务启动和配置。
- [ ] 迁移 Emby API。
- [ ] 迁移认证、用户和设备。
- [ ] 迁移媒体库和媒体模型。
- [ ] 迁移播放和进度逻辑。
- [ ] 建立 API 和客户端回归测试。

### T3：资源发现引擎

- [ ] 实现 SQLite 只读 Reader。
- [ ] 实现监控目录逐级定位。
- [ ] 实现递归目录树。
- [ ] 实现节点快照和 Hash。
- [ ] 实现批次和游标。
- [ ] 实现新增、修改、移动、删除检测。
- [ ] 实现首次发现时间和变更历史。
- [ ] 实现失败恢复和完整扫描保护。

### T4：STRM 和附件

- [ ] 对两份 Python 脚本逐规则分析。
- [ ] 输出 Python/Go 规则映射表。
- [ ] 实现路径映射。
- [ ] 实现 URL 替换和编码兼容。
- [ ] 实现扩展名筛选。
- [ ] 实现文件名长度限制和冲突保护。
- [ ] 实现 STRM 原子写入和幂等更新。
- [ ] 实现 NFO 同步。
- [ ] 实现字幕、图片同步。
- [ ] 建立兼容性测试集。

### T5：媒体引擎集成

- [ ] 融合 SQLite 发现结果和媒体扫描器。
- [ ] 实现 TMDB ID 提取和元数据匹配。
- [ ] 验证 NFO 优先读取。
- [ ] 验证无 NFO 时 TMDB 刮削。
- [ ] 验证电影、电视剧、季、集关系。
- [ ] 验证播放 URL 和签名有效性。
- [ ] 验证客户端播放和进度上报。
- [ ] 使用折月亮完成端到端测试。

### T6：主动入库和管理界面

- [ ] 实现 AList 目录浏览。
- [ ] 实现 AList 搜索。
- [ ] 实现资源详情。
- [ ] 实现单文件入库。
- [ ] 实现整目录入库。
- [ ] 实现取消管理和重新处理。
- [ ] 实现资源、批次、任务和错误列表。
- [ ] 实现系统配置和运行状态页面。

---

## 15. 风险和关注点

| 事项 | 影响 | 应对 |
|---|---|---|
| go-emby 内部能力未审计 | 可能高估可复用范围 | M0 先做源码审计 |
| 部分能力闭源 | 某些功能无法迁移 | 标记边界，必要时自主实现 |
| Emby API 差异 | 客户端登录、展示或播放异常 | 真实客户端回归测试 |
| 签名 URL 过期 | STRM 播放失效 | 验证签名策略，必要时播放时刷新 |
| SQLite 大表 | 影响 Xiaoya 性能 | 只读、限定目录、批量查询、限制并发 |
| WAL 一致性 | 读取快照不一致 | 确保 WAL/SHM 可见并验证快照机制 |
| 资源移动 | 新旧 STRM 重复 | 保存路径历史和资源映射 |
| 附件关联 | NFO/字幕错误归属 | 以原脚本规则和样本测试 |
| 多来源重复媒体 | 媒体库重复项目 | 分离来源资源和媒体实体 |
| 数据迁移 | 用户、设备、播放记录丢失 | 备份、迁移验证和回滚演练 |

---

## 16. V1.0 交付物

1. 可独立部署的 Xiaoya Media Server。
2. go-emby 源码审计报告及迁移记录。
3. Emby API 和客户端回归测试报告。
4. 统一数据库模型和迁移脚本。
5. Xiaoya SQLite 只读资源发现模块。
6. 指定目录监控、增量检测和变更历史。
7. 与 `strm.py`、`strm2.py` 兼容的 Go STRM 生成器。
8. NFO、字幕、图片同步模块。
9. TMDB 元数据和媒体库扫描能力。
10. 播放、进度和继续观看能力。
11. AList 浏览、搜索和主动入库功能。
12. 管理界面、任务调度、日志和部署文档。
13. 折月亮端到端测试报告。
14. 完整验收清单和已知问题清单。

## 17. 当前状态总表

| 项目 | 状态 |
|---|---|
| 产品方向 | 已确认 |
| go-emby 作为二开基础 | 用户已确认 |
| go-emby 源码逐模块审计 | 待办 |
| Xiaoya SQLite 结构 | 已初步验证 |
| 折月亮 27 集目录树 | 已验证 |
| `content` 保存播放 URL | 已验证样本 |
| SQLite 无时间字段 | 已确认 |
| 初始监控目录 | 已确认 |
| 电视剧整目录入库 | 已确认 |
| NFO prefer | 已确认 |
| 删除 keep | 已确认 |
| STRM 原脚本兼容原则 | 已确认 |
| STRM 具体规则映射 | 待取得脚本并审计 |
| Emby API 兼容范围 | 待源码和客户端测试 |
| 播放签名有效期 | 待验证 |
| 统一数据库最终结构 | 待原项目数据库审计 |
| 前端复用方案 | 待确认 |
| AList API/WebDAV 分工 | 待验证 |

## 18. 推荐执行顺序

1. M0：go-emby 源码审计，确认可复用功能、数据库、依赖和许可。
2. STRM 规则审计：取得 `strm.py`、`strm2.py`，形成兼容基准。
3. 媒体服务器基础迁移：保留客户端兼容能力。
4. 资源发现和增量引擎：接入 Xiaoya SQLite。
5. STRM 和附件融合：按原脚本规则生成本地媒体文件。
6. 折月亮端到端验证。
7. AList 主动入库和管理界面。

**最终目标：以 go-emby 当前可用源码为媒体服务器基础，将 Xiaoya/AList 资源发现、增量管理和原规则 STRM 生成整合为媒体服务器原生能力，形成一个可独立部署、可持续二次开发的统一媒体平台。**
