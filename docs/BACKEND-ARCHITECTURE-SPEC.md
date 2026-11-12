# 后端架构规范与重构规划说明 (Backend Architecture Specification)

本文档定义了 Xiaoya Media Server 在前端升级至 React 19 后的**后端整体架构规划、模块边界职责、数据流向模型与无用目录清理成果**。

---

## 1. 架构目标与设计原则

1. **协议双重兼容**：
   - **对外**：100% 兼容 Emby / Jellyfin 协议，使各类第三方客户端（Infuse, VidHub, Fileball, SenPlayer, 官方 Emby APP 等）即插即用。
   - **对内**：提供高内聚、轻量化的 RESTful 管理接口（`/admin/*`, `/api/*`），驱动 React 现代化管理后台。
2. **零转码直链穿透 (Zero-Transcoding Direct Play)**：
   - 彻底解除 NAS 的 CPU/GPU 解码瓶颈，所有小雅/网盘媒体资源通过 302 重定向穿透至 CDN 直链高速播放。
3. **单二进制极简部署 (Single Binary Distribution)**：
   - 前端通过 Go `//go:embed` 打包嵌入二进制；无须配置复杂的 Nginx 反向代理或外置 Node 运行时。
4. **分层清晰与高内聚 (Modular Architecture)**：
   - 明确 API 路由层、业务服务层、数据存储层与周期调度层的职责边界，杜绝隐式全局耦合。

---

## 2. 整体架构分层设计

```
┌────────────────────────────────────────────────────────────────────────┐
│                   Web & Client Layer (客户端接入层)                      │
│   React 19 SPA (/web, /assets)  │  Emby/Infuse 客户端 (/emby, /Items)   │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ HTTP（当前实现）；WebSocket 尚未实现，需经目标客户端验收确认需求
┌───────────────────────────────────▼────────────────────────────────────┐
│                  Routing & Gateway Layer (统一路由网关)                 │
│  - embyserver.App (Emby 协议分发、会话校验、静态资源路由)                   │
│  - adminapi.Handler (资源、监控、AList 分页、TMDB 与日志接口)             │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
┌───────────────────────────────────▼────────────────────────────────────┐
│                    Business Services (核心服务层)                      │
│                                                                        │
│  ┌───────────────────────┐  ┌──────────────────────┐  ┌─────────────┐  │
│  │ Pipeline & Scheduler  │  │   STRM Engine        │  │  TMDB Svc   │  │
│  │ 监控目录周期同步/追更   │  │   生成规范文件/映射   │  │  刮削与纠偏  │  │
│  └───────────────────────┘  └──────────────────────┘  └─────────────┘  │
│  ┌───────────────────────┐  ┌──────────────────────┐  ┌─────────────┐  │
│  │ DirectSTRM Resolver   │  │   Scanner & Probe    │  │  Log System │  │
│  │ 302 重定向/Token 刷新 │  │   元数据提取/并发控制 │  │  全链路排错 │  │
│  └───────────────────────┘  └──────────────────────┘  └─────────────┘  │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
┌───────────────────────────────────▼────────────────────────────────────┐
│                  Data & Persistence Layer (存储与持久化)                │
│  - PostgreSQL: 媒体库元数据、用户体系、扫描计划、发现快照台账               │
│  - Xiaoya SQLite: 脱机只读高速数据字典                                  │
│  - In-Memory LRU Cache: 302 直链短效缓存 (TTL 5s)                       │
│  - File System: 本地 STRM 输出目录 (/media/strm)                        │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 核心子系统模块职责划分

### 3.1 网关与 API 层 (`internal/embyserver` & `internal/adminapi`)
- **Emby API**：提供 `/emby/Users/*`、`/emby/Items`、`/emby/Users/{id}/Views` 等标准协议接口；
- **Direct Play**：提供 `/Videos/{id}/stream`，根据资源类型自动执行本地直读或 STRM 302 跨跳穿透；
- **Admin API**：接管 `/api/system/*`、`/api/monitor/*`、`/api/sync/*`、`/api/alist/*`、`/admin/logs` 等核心控制台接口。

### 3.2 资源发现与同步流水线 (`internal/integration` & `internal/resource`)
- **`resource/discovery`**：维护小雅网盘资源快照基线与增量游标（Cursor），具备 `keep` 机制防止上游网盘失效时误删本地 STRM。
- **`resource/alist`**：封装 AList v3 API，提供分页列表 (`/api/alist/browse`) 与目录关键词检索 (`/api/alist/search`)，支持整剧追踪与单次转储。
- **`integration.Pipeline`**：高内聚串联「小雅网盘/AList 扫描 ➔ 数据快照更新 ➔ 本地 STRM 文件生成 ➔ 局部媒体库通知刷新」，提供事务性执行保障。

### 3.3 STRM 生成引擎 (`internal/strm`)
- 按照 Python 黄金标准生成兼容多端播放器的 STRM 文本、同名外挂字幕与元数据占位。
- 提供路径映射器（PathMapper），将上游网盘虚拟路径转换为标准的本地存放路径。

### 3.4 TMDB 刮削与纠偏系统 (`internal/metadata/tmdb`)
- 集成 TMDB API v3，支持自定义局域网 ProxyURL、速率限制（RateLimitMs）、自动重试。
- 内置正则清洗器提取片名、季集标识（`SxxExx`）、年份与分辨率，并为管理控制台提供一键搜索与强制关联（Rematch）能力。

### 3.5 全链路排错与诊断系统 (`RecordGlobalLog`)
- 统一日志链路标识：`emby`、`strm`、`sync`、`tmdb`、`alist`、`scanner`。
- 采用内存循环队列（Ring Buffer）+ 文件落盘双通道机制，支持实时按级别、链路模块与关键字搜索，可在前端 LogDrawer 毫秒级展示。

---

## 4. 目录结构清理与成果

通过本次重构，移除了过去在演进过程中遗留的冗余文件与废弃工程：

| 移除项目 | 类型 | 移除原因 |
|:---|:---|:---|
| `file-ui/` | 废弃前端目录 | 旧 Vue 3 独立工程，已被全新 React 19 单页应用 (`frontend/`) 完全替代。 |
| `web/files/` | 旧构建产物 | `file-ui` 构建输出，无继续保留价值。 |
| `bin/` (大文件) | 临时构建产物 | 包含了历史遗留的 22MB 二进制文件，由构建脚本自动生成即可。 |
| `scripts/ref/` | 历史参考脚本 | 包含早期原型 `strm.py`，现已完全用 Go 原生单元测试替代。 |
| `.DS_Store` 等 | 系统隐匿文件 | 彻底清理，并在 `.gitignore` 中完善规则。 |

本次重构后仓库根目录干净精练：
- `cmd/server/`：主服务装配入口
- `configs/`：配置模板
- `deployments/`：Docker 多阶段构建配置
- `frontend/`：React 19 + TypeScript + Tailwind 现代化前端
- `internal/`：分层清晰的 Go 后端核心
- `migrations/`：数据库迁移脚本
- `web/`：Go 内嵌静态产物
