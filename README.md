# Xiaoya Media Server

基于 [`go-emby`](https://github.com/sd87671067/go-emby) 演进的**一体化小雅资源监控、Python 兼容 STRM 引擎、AList 浏览入库、TMDB/NFO 元数据刮削与 Emby 兼容媒体服务器**（V1.0）。

---

## 核心特性

1. **Emby 兼容播放与网页管理**
   - 网页用于浏览影库、管理媒体库和资源。使用 Infuse、VidHub、SenPlayer 等 Emby 兼容客户端播放，支持 302 直链重定向播放、过期 `?sign=...` 自动刷新回退、外挂字幕发现与 `.srt -> WebVTT` 动态转换、播放进度续播与真实 `/Shows/NextUp` 下一集推荐。
2. **小雅 SQLite 零侵入只读目录监控**
   - 通过 `mode=ro` + `PRAGMA query_only=1` 只读消费小雅 `strm_internal.db`（WAL 安全），支持精确路径注册与按需监控，建立基线快照、分钟级 `id > last_max_node_id` 增量发现与全树差异对账。
3. **按 `strm.py` 和 `strm2.py` 规则生成 STRM 与附件**
   - 覆盖两份脚本的媒体扩展名并集、字幕和图片等附件，支持无后缀分集节点（如 `E01..E27`）、原子写入和重启后复用 STRM 路径。规则片段用 Python 对照，特殊路径、签名和附件另有回归测试。
4. **`prefer` NFO 优先策略 + TMDB v3 自动刮削**
   - 本地有效非空 NFO 优先采用（自动过滤空白页、HTML 错误页与 JSON 报错）；缺失 NFO 时识别 `{tmdb-*}` 或片名年份自动调用 TMDB v3 刮削中文剧集/季/集/电影海报与演职员，刮削异常绝不阻塞入库与播放。
5. **`keep` 源端缺失保护策略**
   - 仅完整无错扫描可标记资源 `MISSING`（扫描异常绝不推进游标或误标缺失），完整保留本地 STRM/NFO/字幕/图片与用户观看进度；源端恢复自动 `RESTORED`；管理员可预览并确认取消管理；本地文件、媒体条目和观看记录继续保留。
6. **AList 目录浏览、关键词搜索与一键人工入库**
   - 后台直接浏览与搜索小雅 AList 全量目录，支持单文件入库及整部剧集目录入库（可选开启自动追更），纳入同一统一资源台账与周期调度体系。

---

## 仓库目录结构

```text
xiaoya-media-server/
├── cmd/server/                 # 统一服务入口（装配 Emby + Pipeline + AList + Scheduler + AdminAPI）
├── configs/                    # YAML 配置示例 (config.example.yaml)
├── deployments/                # 生产多阶段 Dockerfile、compose.yaml 与 .env.example
├── docs/                       # 技术方案、架构规划、部署指南与验收清单
├── frontend/                   # 现代化 React 19 + TypeScript + Vite + TailwindCSS 前端工程
├── internal/
│   ├── adminapi/               # /api/* 统一资源、监控、AList、批次与调度管理接口
│   ├── config/                 # 配置加载与环境变量覆盖
│   ├── embyserver/             # Emby 兼容核心、扫描器、字幕、NextUp、直链解析与 TMDB 协同
│   ├── integration/            # 监控发现 -> STRM 落地 -> 局部媒体库刷新一体化流水线
│   ├── metadata/tmdb/          # TMDB v3 客户端与 {tmdb-*} / SxxExx 命名解析器
│   ├── resource/
│   │   ├── alist/              # AList v3 客户端、分页浏览/搜索与单文件/整剧追更入库器
│   │   ├── discovery/          # 基线快照、增量游标、状态机、keep 保护与统一存储层
│   │   └── xiaoya/             # 小雅 SQLite 纯 Go 只读驱动与子树遍历器
│   ├── scheduler/              # 后台周期调度任务与防重入执行器
│   └── strm/                   # Go 原生 STRM 与附件生成引擎
├── migrations/                 # 统一资源发现与管理表结构 SQL
└── web/                        # Go //go:embed 静态资源打包目标（Vite 生产构建产物）
```

---

## 快速开始

### 方式一：Docker Compose 一键部署（推荐）

```bash
cd deployments
cp .env.example .env
# 编辑 .env 填写 POSTGRES_PASSWORD、ADMIN_PASSWORD(>=12位)、XIAOYA_DB_DIR、ALIST_BASE_URL、TMDB_API_KEY
docker compose pull
docker compose up -d
```

启动后访问 `http://<宿主机IP>:8096` 登录管理端。播放时，在 Emby 兼容客户端中添加同一个服务器地址。完整说明见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)。

### 方式二：本地开发与测试

```bash
# 纯 Go 静态编译
CGO_ENABLED=0 go build -o bin/xiaoya-media-server ./cmd/server

# 运行单元测试与《折月亮》样本测试；设置 TEST_DATABASE_URL 指向专用测试库以运行 PostgreSQL 集成测试
go test ./...
```

---

## 版本里程碑（Git Tags）

| Tag | 里程碑说明 |
|---|---|
| `v0.1.0-m0` | M0：`go-emby` 源码全量审计与 `strm.py`/`strm2.py` 兼容规则基线冻结 |
| `v0.2.0-m1` | M1：`go-emby` 核心迁移、去授权联网耦合、跨平台适配与统一配置骨架 |
| `v0.3.0-m2` | M2：TMDB v3 刮削器、`{tmdb-*}` 目录识别与 NFO `prefer` 协同策略 |
| `v0.4.0-m3` | M3：外挂字幕链路、过期 `sign` 直链恢复、本地流式播放与 `/Shows/NextUp` |
| `v0.5.0-m5` | M4 & M5：小雅 SQLite 只读访问层、快照增量差异引擎与 `keep` 缺失保护 |
| `v0.6.0-m6` | M6：Go 原生 STRM 与附件引擎及 `python3` 逐字节黄金对比验证 |
| `v0.7.0-m7` | M7：一体化流水线闭环与《折月亮》构造样本 14 步流程测试通过 |
| `v1.0.0` | M8 & M9：AList 浏览搜索入库、周期调度、统一后台控制台与生产容器化交付 |

---

## 文档索引

- [完整技术方案 V1.0](docs/TECHNICAL-SPEC.md)
- [Python STRM 兼容规则审计](docs/STRM-RULES-AUDIT.md)
- [`go-emby` 基线源码审计报告](docs/GO-EMBY-SOURCE-AUDIT.md)
- [《折月亮》14 步端到端测试报告](docs/ZHEYUELIANG-E2E-REPORT.md)
- [生产部署与运维指南](docs/DEPLOYMENT.md)
- [V1.0 验收清单与已知边界](docs/ACCEPTANCE-CHECKLIST.md)
- [Xiaoya 播放链路修复方案与阿里 / 115 实机验证](docs/XIAOYA-PLAYBACK-FIX.md)
