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
| `STRM_TARGET_BASE_URL` | 写入 `.strm` 文件的目标地址（默认同 `ALIST_BASE_URL`） | `http://192.168.1.10:5678` |
| `TMDB_API_KEY` | TMDB v3 API Key（无本地 NFO 时自动刮削） | `your_tmdb_v3_key` |
| `MONITOR_ROOTS` | 初始精确监控根目录（逗号分隔） | `/每日更新/电视剧/国产剧（115）,/每日更新/电视剧/国产剧` |

### 3.2 启动服务栈

```bash
docker compose -f deployments/compose.yaml --env-file deployments/.env up -d --build
```

检查健康状态：

```bash
curl -fsS http://127.0.0.1:8096/api/system/health
# 期望输出：{"service":"xiaoya-media-server","status":"ok"}
```

### 3.3 首次进入后台完成媒体库关联

1. 浏览器访问 `http://<NAS_IP>:8096`，使用用户名 `admin` 与 `.env` 中的 `ADMIN_PASSWORD` 登录。
2. 点击右上角用户图标 → **后台管理**。
3. 在 **媒体库** 面板新建电视剧媒体库（例如名称 `国产剧`，目录选择 `/media/strm/每日更新/电视剧` 或 `/media/strm`）。
4. 切换到 **资源与监控** 面板：
   - 确认“Xiaoya SQLite 只读数据源”状态为 `🟢 已连接 (RO)`；
   - 点击 **全量同步全部** 完成首次基线快照与 `.strm` / 附件落地，系统会自动触发对应目录的局部媒体库扫描；
   - 后续每 `10m`（可配）由后台调度器自动执行增量同步，每日自动执行全量树差异校验。
5. 如需在指定监控目录之外按需追剧或入库单部电影，切换到 **AList 入库** 面板浏览或搜索小雅目录，点击 **整剧入库+追更** 或 **单文件入库** 即可。

---

## 4. 本地源码编译与运行

```bash
# 1. 纯静态编译（无 CGo 依赖）
CGO_ENABLED=0 go build -mod=vendor -o bin/xiaoya-media-server ./cmd/server

# 2. 仅初始化/迁移 PostgreSQL 表结构并退出（可选）
./bin/xiaoya-media-server -config configs/config.example.yaml -prepare-schema

# 3. 启动统一服务
./bin/xiaoya-media-server -config configs/config.example.yaml
```

---

## 5. 备份、恢复与升级说明

1. **自动备份**：
   - 服务内置每 24 小时执行一次 `pg_dump`，备份文件保存在 `/data/backups/`（对应宿主机 `${APP_DATA_DIR}/backups/`），自动保留最近 14 份快照。
2. **手动备份与恢复**：
   ```bash
   # 手动导出
   docker exec xiaoya-media-postgres pg_dump -U emby emby > backup_$(date +%F).sql

   # 恢复到新库
   cat backup_2026-11-07.sql | docker exec -i xiaoya-media-postgres psql -U emby -d emby
   ```
3. **平滑升级**：
   - 所有数据库表结构迁移均具备幂等性（`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`）。
   - 拉取新版本源码或镜像后执行 `docker compose up -d --build`，服务启动时会自动完成增量迁移，保留全部用户、媒体库、`userdata` 观看记录、监控游标与人工入库台账。
