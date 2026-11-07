# Xiaoya Media Server V1.0 部署与运维指南

本文档对应《Xiaoya Media Server 完整技术方案 V1.0》第 11 章，提供单栈统一部署、小雅 SQLite 只读 WAL 安全挂载、首次初始化、备份恢复与版本升级的完整操作说明。

---

## 1. 部署拓扑概览

```mermaid
flowchart LR
  subgraph Host["NAS / Linux 宿主机"]
    XY["现有小雅容器（保持不变）<br/>strm_internal.db + .db-wal + .db-shm"]
    VOL_STRM["/media/strm 本地 STRM 与附件"]
    VOL_DATA["/data 缓存与自动备份"]
  end

  subgraph Stack["Docker Compose 统一栈 (deployments/compose.yaml)"]
    APP["xiaoya-media-server :8096<br/>Emby API + Web 控制台 + 监控/STRM/TMDB/调度"]
    PG["postgres:16-alpine :5432<br/>媒体索引 + 观看进度 + 统一资源台账"]
  end

  Clients["Infuse / VidHub / SenPlayer / 浏览器"] -->|HTTP 8096| APP
  APP -->|只读目录挂载 :ro| XY
  APP -->|HTTP /d/... & /api/fs/*| XY
  APP <-->|SQL| PG
  APP <-->|原子读写| VOL_STRM
  APP <-->|读写| VOL_DATA
```

---

## 2. 关键前提：小雅 SQLite 只读挂载规范

1. **零侵入不改造小雅容器**：保持现有小雅容器原样运行，禁止修改小雅镜像、启动脚本或建库逻辑。
2. **必须挂载父目录而非单文件**：
   - SQLite 在 WAL 模式下会伴随生成 `strm_internal.db-wal` 和 `strm_internal.db-shm`。
   - 若仅挂载单个 `.db` 文件，容器内无法读取 `-wal` 中尚未 checkpoint 的最新节点，甚至可能因 inode 替换导致旧句柄失效。
   - 因此在 `deployments/compose.yaml` 中必须将宿主机目录挂载为 `/xiaoya-db:ro`：
     ```yaml
     volumes:
       - ${XIAOYA_DB_DIR}:/xiaoya-db:ro
     ```
3. **连接层双重只读锁定**：
   - 程序内部通过 `file:<path>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)` 打开连接，并在启动与每次扫描前执行 `VerifyReadOnly` 校验，确保绝不向小雅库写入任何字节或创建索引。

---

## 3. 快速部署步骤（Docker Compose）

### 3.1 准备环境变量

```bash
cd deployments
cp .env.example .env
```

编辑 `.env`，重点确认以下必填项：

| 变量名 | 说明 | 示例 |
|---|---|---|
| `POSTGRES_PASSWORD` | PostgreSQL 强密码 | `StrongPass_2026!` |
| `ADMIN_PASSWORD` | 初始 `admin` 管理员密码（**不少于 12 位**） | `AdminPass_2026!` |
| `XIAOYA_DB_DIR` | 宿主机上 `strm_internal.db` 所在**父目录** | `/vol1/1000/xiaoya/data` |
| `ALIST_BASE_URL` | 客户端与服务端可访问的小雅地址 | `http://192.168.1.10:5678` |
| `STRM_TARGET_BASE_URL` | 写入 `.strm` 文件的目标地址（默认保留 `http://xiaoya.host:5678`） | `http://192.168.1.10:5678` |
| `TMDB_API_KEY` | TMDB v3 API Key（无本地 NFO 时自动刮削） | `your_tmdb_v3_key` |
| `MONITOR_ROOTS` | 初始精确监控根目录（可留空，多个用逗号分隔） | `/每日更新/电视剧/国产剧（115）,/每日更新/电视剧/国产剧` |

### 3.2 启动服务栈

```bash
docker compose pull
docker compose up -d
```

检查健康状态：

```bash
curl -fsS http://127.0.0.1:8096/api/system/health
# 期望输出：{"service":"xiaoya-media-server","status":"ok"}
```

### 3.3 首次进入后台完成媒体库关联

1. 浏览器访问 `http://<NAS_IP>:8096`，用 `admin` 和首次启动时设置的密码登录，打开 **管理控制台**。
2. 在 **媒体库管理** 创建电影或电视剧媒体库，把需要入库的 STRM 输出目录加入媒体库。
3. 在 **小雅同步入库 & AList** 添加要监控的精确路径，设置各目录的增量同步间隔。点目录旁的 **全量** 建立基线。结果区会显示实际新增、缺失、恢复和失败数量。
4. 按需从 AList 浏览或搜索，选择 **单次入库** 或 **整剧追更**。可指定导入的媒体库和其中一个输出目录。「已生成，未收录」表示已有 STRM，但其路径尚未加入媒体库。
5. 在 **资源与任务** 查看资源状态、变更、扫描批次和定时任务。取消管理需先预览并确认；本地文件、媒体条目和观看记录会保留。
6. 观看时，在 VidHub、SenPlayer 或 Infuse 中添加 Emby 服务器，使用同一服务地址和账号登录。网页用于浏览和管理，不提供播放。

### 3.4 通过 API 提交后台任务

同步和入库接口仍接受原来的同步请求。需要后台运行时，为 `POST /api/sync/full`、`POST /api/sync/incremental` 或 `POST /api/resources/import` 加上 `?async=true`。接口返回 `202` 与任务 `id`，用 `GET /api/jobs/{id}` 查询结果。任务状态为 `RUNNING`、`SUCCESS`、`PARTIAL`、`FAILED` 或 `INTERRUPTED`。同一范围已有任务时返回 `409`；服务重启后可重新提交已中断的任务。

---

## 4. 飞牛 OS (fnOS) NAS 专属部署指南

飞牛 OS 内置 Docker 与 Compose 应用管理器，无需在 NAS 上安装 Go 开发环境即可一键拉取镜像部署：

### 4.1 在飞牛 OS 中创建项目
1. 打开飞牛 OS 网页控制台，进入 **Docker** -> **Compose**（项目）。
2. 点击 **创建项目**，输入项目名称（如 `xiaoya-media-server`）。
3. 项目路径选择你希望存放数据的存储卷（例如 `/vol1/1000/docker/xiaoya-media-server`）。

### 4.2 配置 Compose 与环境变量
直接粘贴项目 [deployments/compose.yaml](file:///Users/oneal/data/dev/projects/nas/xiaoya-media-server/deployments/compose.yaml) 内容。

根据飞牛 OS 上的小雅容器路径填写环境变量（或在同级创建 `.env`）：
- `XIAOYA_DB_DIR`: 飞牛上小雅容器挂载的宿主机配置目录（必须包含 `strm_internal.db` 的父目录，例如 `/vol1/1000/docker/xiaoya/data`）。
- `MEDIA_DIR`: 飞牛上存放 STRM 文件的存储空间（例如 `/vol1/1000/media`）。
- `APP_DATA_DIR`: 持久化缓存与自动备份目录（例如 `/vol1/1000/docker/xiaoya-media-server/appdata`）。
- `POSTGRES_DATA_DIR`: 数据库存储目录（例如 `/vol1/1000/docker/xiaoya-media-server/pgdata`）。
- `POSTGRES_PASSWORD`: 设置 PostgreSQL 数据库强密码。
- `ADMIN_PASSWORD`: 初始管理员登录密码（至少 12 位）。
- `ALIST_BASE_URL`: 小雅 AList 访问地址（例如 `http://192.168.x.x:5678` 或 `http://xiaoya:5678` 如果在同一 Docker 网络）。

### 4.3 启动与访问
点击 **立即构建/启动**，飞牛 OS 将自动从 `ghcr.io/clowrain/xiaoya-media-server:latest` 拉取对应平台（AMD64 / ARM64）的多架构镜像并运行。
服务启动后通过 `http://<飞牛NAS_IP>:8096` 即可进入 Web 控制台或在 Infuse / VidHub 客户端中添加 Emby 连接。

---

## 5. 本地源码编译与运行

```bash
# 1. 纯静态编译（无 CGo 依赖）
CGO_ENABLED=0 go build -o bin/xiaoya-media-server ./cmd/server

# 2. 仅初始化/迁移 PostgreSQL 表结构并退出（可选）
./bin/xiaoya-media-server -config configs/config.example.yaml -prepare-schema

# 3. 启动统一服务
./bin/xiaoya-media-server -config configs/config.example.yaml
```

---

## 6. 备份、恢复与升级说明

1. **备份**：服务每 24 小时使用当前 PostgreSQL 连接配置执行 `pg_dump`，也可由管理员调用 `POST /admin/backup`。标准部署写入容器的 `/data/backups/`，对应宿主机 `${APP_DATA_DIR}/backups/`。仅发布完整备份，保留最近 14 份。STRM、字幕和图片位于媒体挂载目录，应另行备份。
2. **恢复检查**：先创建空的独立恢复库，用 `pg_restore --exit-on-error --no-owner --dbname=<恢复库>` 恢复选中的 `.dump`，核对账号、媒体库、观看记录和监控配置后再安排切换。生产库仍在使用时，不要直接把备份导入同名库。
3. **升级**：先备份 PostgreSQL 和媒体目录，再拉取新镜像并执行 `docker compose up -d`。启动时自动应用资源生命周期迁移，保留原有账号、媒体 ID、观看记录、监控设置及 STRM 映射。升级后到 **资源与任务** 检查「需要确认的旧追更目录」，仅为确实需要自动更新的目录重新启用追更。已中断的同步或入库可在原入口重新提交。
4. **发布验收**：用实际 NAS 与小雅/AList 数据验证首次全量、增量、缺失保护和备份恢复；在目标客户端验收登录、季/集、直链、外挂字幕、进度和继续观看。
