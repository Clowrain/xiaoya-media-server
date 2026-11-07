# Xiaoya Media Server

统一的媒体服务器与 Xiaoya/AList 资源管理平台。基于 `go-emby` 可二次开发源码构建，将 Emby 兼容 API、TMDB/NFO 刮削、播放链路与 Xiaoya SQLite 只读资源发现、增量同步、原版规则 STRM 生成及主动入库融合为单一服务。

详细架构与里程碑请查阅：[完整技术方案 V1.0 基线](docs/TECHNICAL-SPEC.md)。

---

## 里程碑路线图

| 阶段 | 里程碑 | 核心交付物 |
|---|---|---|
| **M0** | `go-emby` 源码与 STRM 脚本审计 | `docs/GO-EMBY-SOURCE-AUDIT.md`、STRM 规则映射表 |
| **M1** | 迁移媒体服务器基础能力 | 可启动服务、认证/设备/媒体库 Emby API 基线 |
| **M2** | 媒体库、扫描和元数据 | 电影/电视剧层级、TMDB 与 NFO `prefer` 策略 |
| **M3** | 播放链路 | STRM 解析、重定向播放、进度上报与继续观看 |
| **M4** | SQLite Reader 和资源快照 | 只读连接 `strm_internal.db`、目录定位与递归快照 |
| **M5** | 增量变化检测 | 新增/修改/移动/删除识别、`keep` 失效保留策略 |
| **M6** | STRM 兼容迁移 | 对齐 `strm.py` / `strm2.py` 规则、Python/Go 对照测试 |
| **M7** | 附件和媒体库融合 | NFO/字幕/图片同步、《折月亮》端到端集成验证 |
| **M8** | AList 浏览和主动入库 | AList 搜索/浏览、单文件与整剧目录入库 |
| **M9** | 管理界面和部署完善 | 后台管理、Docker 部署与 V1.0 验收报告 |

---

## Git 版本管理规范

本项目按里程碑和模块化任务严格进行 Git 版本控制：

1. **分支策略**
   - `main`：稳定主干分支，仅合并通过编译与测试验证的阶段性成果。
   - `docs/*`：源码审计报告、架构设计、规则映射文档（如 `docs/m0-source-audit`）。
   - `feat/*`：按里程碑模块拆分功能分支（如 `feat/m1-server-base`、`feat/m4-xiaoya-reader`、`feat/m6-strm-engine`）。
   - `fix/*` / `test/*` / `chore/*`：缺陷修复、回归测试与构建脚本维护。
2. **提交规范（Conventional Commits）**
   - 格式：`<type>(<scope>): <subject>`
   - 示例：
     - `docs(audit): add go-emby source audit report and module dependency graph`
     - `feat(xiaoya): implement read-only sqlite node tree scanner`
     - `feat(strm): port url encoding and 235-char path truncation from strm.py`
     - `test(strm): add python vs go golden compatibility test suite`
3. **里程碑 Tag**
   - 每个里程碑验收完成后打附注标签（如 `v0.1.0-m0`、`v0.2.0-m1` ... 直至 `v1.0.0`）。
4. **安全与仓库卫生**
   - 严禁提交任何 API Key（如 TMDB Key）、签名 Token、真实生产配置、数据库文件（`.db`/`-wal`/`-shm`）及本地生成的媒体/STRM 产物。
