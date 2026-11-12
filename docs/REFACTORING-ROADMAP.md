# Emby 兼容架构重构路线图

> 状态基线：2026-11-11。按阶段推进；每项单独验证后再开始下一项。目标是逐步贴近 V2.0 架构，不做大规模重写，不改变已确认的客户端行为和 PostgreSQL 数据。

## 已完成

### 阶段 0：现状基线
- 对照用户提供的 V2.0 规范、`BACKEND-ARCHITECTURE-SPEC.md`、`EMBY-XIAOYA-IMPROVEMENT-PLAN.md` 和现有实现完成首轮盘点。
- Go 基线：`go test ./...`、`go vet ./...`、`go build ./...` 通过。
- 前端基线：4 个测试文件、6 个测试及 `npm run build` 通过。构建生成文件已清理。
- `TEST_DATABASE_URL` 当前未配置；基线和后续本地验证不会自动覆盖 PostgreSQL 专项集成测试。

### 阶段 1：协议 DTO 与认证边界（首批切片）
- `UserDTO`、`UserPolicyDTO`、`UserConfigurationDTO` 已替代用户响应中的动态 map；现有 JSON 测试锁定主要字段，但未覆盖全部字段组合。
- `UserItemDataDTO`、`MediaSourceDTO`、`ItemsQueryResultDTO` 已用于响应边界；Items envelope 的 `Items` 仍为 `[]M`，MediaSource 测试覆盖关键字段是否存在及 typed/map 投影一致，尚非所有字段值的协议快照。
- Token/API Key 校验、登录密码校验、Token 签发、持久化和撤销由认证服务及 `authRepository` 承接；HTTP 登录处理位于 `auth_handler.go`，通用路由分发和鉴权入口仍在 `main.go`。
- 登录限流按解析后的 IP 归并（含 IPv6），清理过期 IP 计数；认证存储错误不再退化为 API Key 查找，Token 哈希/过期与设备关联策略保持不变；登录历史更新失败不阻断登录但会记录错误。
- `single` 与 `items` 浏览 handler 已移到 `internal/embyserver/items_handler.go`；`main.go` 保留路由分发。

## 当前与待办顺序

### 阶段 2：统一 PostgreSQL schema 与 migration 生命周期 — 实现完成，专项验收有缺口
- `migrations/002_resource_engine.sql` 是可移植 resource schema 的唯一权威来源；`migrations.ResourceEngine` 被 `SQLStore.EnsureSchema` 嵌入执行，移除了重复 Go 常量 `resourceEngineDDL`。Postgres 专属 `items.availability` 仍由原方言分支处理，`003_resource_lifecycle.sql` 的事务和 backfill 标记不变。
- `App.init` 的核心 schema 编排与有序 DDL 函数调用位于 `internal/embyserver/schema.go`；方言专属 DDL 仍在相应 schema 文件中，未声称所有 DDL 集中于单一文件。
- App schema 使用 `app_schema_migrations` v1 baseline、递增版本检查和 Postgres advisory lock；资源域利用 `media_pipeline_schema` v2 控制 resource DDL，生命周期 `003` 保留独立 marker。迁移实现不是统一事务包装：DDL 部分完成后失败时依赖 DDL/迁移步骤幂等后重试，失败不会登记版本。
- SQLite 测试覆盖 App ledger 重复应用、失败后不记账/重试（含部分幂等 DDL 后失败）、未来版本与版本间隙；PostgreSQL advisory lock 并发/失败释放由集成测试覆盖。旧 App scan snapshot 回填由需 `TEST_DATABASE_URL` 的 PostgreSQL 测试覆盖。resource ledger/backfill 现有测试以 SQLite 为主。
- **PostgreSQL 16 验收（2026-11-12）：**在独立 Docker PostgreSQL 16 实例配置 `TEST_DATABASE_URL` 后，`go test ./...` 全量通过；显式 `TestAppSchemaAdvisoryLockSerializesAndReleasesAfterFailure` 也通过。新增 discovery PostgreSQL 测试覆盖旧 resource schema 的 003 backfill/marker 一次性行为、未知未来版本拒绝，以及 lifecycle migration 事务失败回滚后可重试；SQLite 迁移重试测试另覆盖部分幂等 DDL 后失败恢复。

### 阶段 3.1：媒体 Items 浏览查询 — 实现收敛完成，PostgreSQL 16 专项复验通过
- **已完成首轮切片：**新增 `MediaItemRepository`、`ItemPageRepository` 和 `buildItemBrowseQuery`，拆出单条读取、count/page SQL、genre-ID 查找及 Items 参数/query 计划；现有分页、排序与筛选回归通过。
- **已完成子切片 3.1.5：**Series ChildCount/RecursiveItemCount/EpisodeCount 与 Episode Season/Series ancestry 通过批量 `BrowseMetadata` 查询移入 `MediaItemRepository`；保留 flat episode ancestry 与列表字段。
- **已完成子切片 3.1.6：**`GenreNamesInLibrary` 将 Genres/Items/Filters 类端点的 `item_genres`/`items` 读取归入 media repository；既有 genre 过滤、排序及空结果协议保留。
- Items handler 相关的 library image candidate、collage candidate、Years、Item existence、path-scope 及 SimilarItems 查询均由 `MediaItemRepository` 承接；库路径清理通过 item repository 在原事务内删除对应资源。probe 文件清理通过 `MediaItemRepository.GetByID` 判断媒体及可播放 URL；probe 存储文件和 `media_probe` 表的清理仍归 media settings 工作流。metadata indexing / TMDB 更新、scanner inventory 和 direct publish 属 metadata/scanner/ingestion 工作流，不与本阶段的 Emby browse 查询混并。SQLite 与 PostgreSQL 16 全量 Go 测试通过，详见最终验证。

### 阶段 3.2：Library repository — 实现收敛完成
- 首轮已新增 `LibraryRepository` / `LibraryRecord`，libraries 列表 SQL、排序和父级库存在性查询进入 repository；`libraryDTOs()` 负责协议映射，SQLite/PG 测试通过。
- **已完成子切片 3.2.1a：**`libraryPaths()` 从 `LibraryRepository.Paths` 读取配置；缺失/坏 JSON fallback 与显式空路径列表保持兼容，数据库错误记日志。
- **已完成子切片 3.2.1b：**`libraryFolders`、management library create/update/delete 和 bootstrap 对 `library-paths:*` 的写入通过 `LibraryRepository` 完成；调用方现有事务边界保持不变。
- **已完成子切片 3.2.2a：**收拢 scanner、poster、browse、scoped import 和 publish fallback 中的 library 读取；保留缺失库回退、按 id/kind 排序和路径配置语义。Repository 对缺少可选 `library_order` / 状态列的轻量 legacy/test schema 提供兼容回退。
- **已完成子切片 3.2.2b：**管理库 create/update/delete、目录主路径与数量同步、`library_order` 写入/清理、scanner 状态与扫描统计写入，以及 bootstrap/重启扫描状态修复均经 `LibraryRepository`；保留既有事务边界、路径变更时清理 Items 的顺序和响应行为。扫描结束写失败会记录日志。
- 新增 repository 写入测试覆盖库创建、详情/主路径更新、排序写入/删除、库删除及扫描状态；管理路由现有测试继续覆盖事务与 HTTP 行为。

### 阶段 3.3：用户数据 repository
- **已完成子切片 3.3.1：**新增 `UserRepository`，抽离单条播放状态与收藏读取；UserData 组装仍使用 `UserItemDataDTO`，并保持按用户隔离。查询错误记录日志但不改变对外响应。
- **已完成子切片 3.3.2a：**收藏添加/删除写操作从 `setFavorite` 和 `updateUserData` handler 移入 `UserRepository.SetFavorite`；保留 `ON CONFLICT DO NOTHING`、`created_at` 和权限校验。
- **已完成子切片 3.3.2b：**单项已看/未看、Series/Season -> Episode 级联及未看时 resume/play 清理移入 `UserRepository`；保留当前非事务写入顺序，聚合错误并记录日志，不改变 HTTP 响应。
- **已完成子切片 3.3.2c：**`PlaybackPositionTicks` 写入移入 `UserRepository.SetPlaybackPosition`；测试锁定 ticks 原样存储、已有 `Played` 状态不变、新记录默认未看。
- 收藏、已看/未看和播放进度 repository 用例，以及 PostgreSQL 16 全量 Go 测试均通过。
- **阶段 3.3.4 用户态 SQL 审计更新：**批量用户状态/收藏读取、person favorite 与 Items/Resume/NextUp 用户过滤、播放会话释放、文件保护和内存回收均已归属 User/PlaybackSession repository。用户详情读取、删除、基本设置更新、创建账号（含可选播放权限的原子写入）、用户列表、初始管理员创建、播放权限读写、密码更新/Token 撤销及 user identity 名称/密码联动更新均已迁入 `UserRepository`；密码和身份更新保留原事务及错误响应分类，删除后的会话清理调用 `PlaybackSessionRepository.ReleaseUser`。当前会话的 logout Token 撤销属于认证生命周期，由 `Authenticator.Revoke` / `authRepository` 承接，数据库失败返回 500，不再在路由直接执行 SQL。
- **3.3.4 用户列表 last-login 查询已完成：**`UserRepository.ListAccounts` 以 LEFT JOIN 批量读取 `user_logins.last_login`，消除逐用户查询；缺少可选登录历史表的轻量 legacy schema 回退为原账号列表字段。ISO 时间格式及无登录记录不输出 `LastLoginDate` 的协议测试保持通过。
- **阶段 3.3.3 子切片 1 已完成：**新增 `PlaybackCheckInRequest` 与解析器，将 JSON/query 参数优先级、null ticks 回退和 malformed body 兼容从 route 分离；路由继续负责 API-key 拒绝、活动会话回退、许可校验和 HTTP 204/400/403 状态。
- **已完成子切片 3.3.3.2：**新增 `PlaybackSessionRepository` 管理 plays 当前会话读取/释放、过期租约清理、同用户并发限制和 reserve 事务；进程内 `updatePlaybackActivity` 仍作为观测状态留在 activity 模块。
- **已完成子切片 3.3.3.3：**check-in 中 `userdata.position` 与 `resume_activity` 写入改经 User/PlaybackSession repository；保留 `max(position,0)`、ticks 单位、停止清理和 204 响应。
- 阶段 3.3 用户数据及 session repository 用例、PostgreSQL 16 全量测试、Go vet/build 均通过。不把播放进度并入全局媒体实体。
- PlaybackInfo DTO/handler 独立；`MediaItemRepository.VersionsByGrouping` 管版本 SQL；`groupedMediaSourceSelector` 处理 API 用户单源与普通用户多版本选择。MediaSource DTO 保持 DirectPlay/DirectStream true、Transcoding false。
- **阶段 3 实施结论：**3.1–3.4 的路线图范围已完成 repository / application boundary 收敛；跨 domain 封面资源使用独立 `CoverRepository`，跨 library/item 的合法性检查由 API 编排两个领域 repository，未把 union query 塞进 `LibraryRepository`。直接 SQL 保留在 metadata indexing、TMDB 更新、scanner inventory、direct publish 等既有独立工作流中，不属于浏览/库/用户/播放这几条待办切片。
- 不得将播放进度与全局媒体实体混存。
- 不改变响应字段、用户隔离规则及 302/播放行为。

### 阶段 3.4：播放 repository/application service
- **3.4.1 已完成：**PlaybackInfo 外层 DTO/handler 独立；`MediaItemRepository.VersionsByGrouping` 接管多版本读取 SQL，`groupedMediaSourceSelector` 保护 API-key 单源与普通用户多版本选择，能力字段保持与现有 302 直连实现一致。
- **阶段 3.4.2 首个切片完成：**`stream()` 保留 GET/HEAD、鉴权、版本 fallback、reserve 和本地 Range；远端 redirect/STRM/NanShare/public URL 分发移入 `resolvePlaybackRedirect()`，分支顺序与状态码不变。
- **阶段 3.4.2 子切片完成：**新增 `playbackURLResolver` seam，集中 recognized STRM resolver endpoint 选择、上游 redirect 解析和 5 秒链接缓存；缓存 key 继续区分源 URL、用户 token、UA 和 X-Real-IP，cache hit 不重复成功日志，HTTP redirect adapter 保持 no-store/302。
- **阶段 3.4.3 子切片 1 已完成：**将 `logPlayback`、`updatePlaybackActivity` 与 `playbackProgress` 集中到 `playback_activity.go`；进度仍是进程内观测，时长回退与 100% 计算保持不变。
- **阶段 3.4.3 子切片 2 已完成：**新增 `redactPlaybackURL`，播放 redirect 日志与 STRM 失败详情去除 userinfo/query/fragment；上游错误经 `safeProxyError` 输出，HTTP status 和无源/失败响应结构不变。
- **已完成 3.4.3 fallback 测试切片：**覆盖无源、无效 URL、STRM 解析失败、NanShare 上游非重定向、HEAD 直连及日志/错误体 secret redaction；状态码与响应结构锁定。真实客户端 fallback 验证仍属于阶段 5。
- 复用已有 PlaybackInfo、302、字幕、direct STRM 和客户端回归测试。

### 阶段 3 最终收敛记录
- **3.2.2 已完成：**library path settings、management CRUD、扫描状态/统计、排序写入及 bootstrap/重启修复均经 LibraryRepository；后续审计遇到新的 library 持久化调用再单独切片。
- **3.2.2 审计补充已完成：**`direct_publish.go` 使用 `FirstByKindOrFirst` 选发布目标库，`filesAvailable` 使用 `CountByStatus` 检查扫描中库，管理扫描接口使用 `Exists` 校验库 ID；启动 bootstrap 使用 `Count`，Lenna 兼容判断和封面生成均使用 `GetByID`；管理库排序在原事务内用 `CountWith(tx)` 检查库数量。发布目标无库时仍保留 `default` 回退。
- **3.2.3 封面资源 persistence 已完成：**新增 `CoverRepository` 统一 `covers` 读、存在性判断、保存和删除；`image_upload.go` 与 `/admin/cover` 只做权限/协议校验和跨域 ID existence 编排。`compat.go`、`browsing.go`、`poster_gen.go` 从同一 repository 读取封面；行为、ETag 和图片响应保持兼容。
- **3.3.4 子切片 1 已完成：**`pagination.listDTOs` 的批量 `userdata`/favorites 查询迁入 `UserRepository.ListPlaybackStates` / `ListFavorites`；按 user_id 和当前页 item IDs 限定数据，保留 EnableUserData/API 用户跳过及 UserData 合并行为。新增 user scope / item scope repository 测试，`go test ./...` 通过。
- **3.3.4 子切片 2 已完成：**person detail 的单项 favorite 读取改用 `UserRepository.IsFavorite`；person 列表的 favorite 数据改为按页批量读取，消除逐人查询；favorite inclusion/exclusion predicate 已在子切片 5 移入 `UserRepository`。
- **3.3.4 子切片 3 已完成：**按用户清理播放会话纳入 `PlaybackSessionRepository.ReleaseUser`；禁用播放权限及成功删除用户后调用该方法，失败记录日志且不改变既有响应。测试确认只清理目标用户会话。
- **3.3.4 子切片 4 已完成：**Resume 列表筛选 predicate 通过 `UserRepository.ResumableItemPredicate` 生成；电影推荐的最近播放 seed 查询移入 `UserRepository.ResumeSeed`，保留用户、媒体库范围和 resume_activity 排序语义。新增 predicate 参数及 seed 最新活动/媒体库范围测试。
- Resume 列表的 `resume_activity` 排序 SQL 由 `ItemPageRepository.LoadPage` 执行，属于分页持久化排序路径；user ID 使用 SQL 绑定参数，游标 timestamp 显式转换为整数。
- **3.3.4 子切片 5 已完成：**person favorite inclusion/exclusion、Items IsPlayed/IsFavorite 和 NextUp 播放历史 predicate 移入 `UserRepository` 的用户域过滤构造方法；user ID 继续作为绑定参数。`ItemPageRepository` 的 Resume 排序 user ID 改为绑定参数；补充恶意格式 user ID 的多页排序回归，修正 Resume cursor 比较时 timestamp 作为文本值导致重复页的问题（对游标 timestamp 显式转整数）。
- 浏览层仍负责组装查询条件；user-specific SQL predicate 定义由 UserRepository 持有。
- **3.3.4 子切片 6 已完成：**`memory.go` 的活动播放计数及 `files.go` 的播放中资源路径查询迁入 `PlaybackSessionRepository`；保留 180 秒内存回收门槛、120 秒文件保护窗口和原错误语义。repository 测试覆盖活动/过期会话与媒体路径。
- **3.1 人物浏览子切片已完成：**`MediaItemRepository` 接管人物名称/作品数、分页 count 和 keyset page SQL；路由继续负责参数、cursor、DTO 与错误响应。repository 测试覆盖去重计数、作品数、排序及 keyset 分页。
- **3.1 detail count 子切片已完成：**`Series`/`Season` 的直接子项数及 `Series` 的递归 Episode 数统一由 `BrowseMetadata` 查询；detail DTO 保持原有字段和值，避免 `decorateImages` 直接访问 `items` 表。测试覆盖 season 子项数与 series 递归计数。
- **3.1 人物图片子切片已完成：**人物主图 thumb 查询、失败记录读取/写入，以及“仅展示有可用图片人物”的过滤条件均归入 `MediaItemRepository`；过滤条件通过 `PersonPageQuery` 传入，Count/List 共用，测试覆盖失败过期语义。自定义 `covers` 表读写另由 `CoverRepository` 统一承接。
- **3.1 `ItemPageQuery` 边界审计完成，暂不引入 SQL query DSL：**当前生产调用只有 `items_handler`，`Where`/参数来自 `buildItemBrowseQuery`，所有请求值走绑定参数；`Order` 来自 `browseOrderWithContext` 的列名白名单（latest/resume 为固定分支）。将其改成类型化 criteria 需要重写递归 CTE、过滤器与排序表达式，并在应用层/Repository 维护两套表达，当前收益不足。若未来新增独立调用方或 SQL 片段不再由受控 builder 生成，再评估拆分类型化 criteria；Repository 不应接收外部请求直接拼入的 SQL。
- **3.1.7 Items 浏览/媒体操作残余读取已完成：**库封面候选图、collage 候选项目、Years、图片目标存在性、媒体树与路径范围读取、下一个 Episode 及 counts 查询移入 `MediaItemRepository`；媒体名称覆盖、路径范围删除及 library media 清理也通过该 repository，事务由原调用方持有。
- **阶段 1–3 一致性整改与 PostgreSQL 补充验收（2026-11-12）：**认证只在用户 Token 查无记录时回退 API Key；登录限流以标准 IPv4/IPv6 host key 计数并逐请求清理过期 key；logout 撤销归入 `Authenticator`/`authRepository` 并对数据库失败返回 500。SimilarItems 查询进入 `MediaItemRepository`，probe 清理通过该 repository 判断媒体可播放性；DTO 能力字段测试扩充。Stage 2 文档明确区分实现、SQLite 覆盖、PostgreSQL 验收以及 DDL 非统一事务包装；新增部分幂等 DDL 失败重试、PG advisory lock 并发/释放以及 resource lifecycle backfill、未来版本拒绝、事务失败回滚后重试用例，PostgreSQL 16 下均通过。
- **本轮最终验证（2026-11-12）：**Docker PostgreSQL 16 下 `TEST_DATABASE_URL=… go test ./...`、单独的 advisory lock 集成测试、`go vet ./...`、`go build ./...`、`git diff --check` 均通过。测试容器在验收后清理。
- 3.4 代码 fallback 与 secret redaction 已有回归；真实客户端对 decoder、Range/字幕/fallback 的验证留在阶段 5，不以模拟测试冒充实机结果。
- 保留已有 `resource/discovery`、`strm`、`integration`、`scheduler`、`metadata/tmdb` 边界；不创建万能 Store/Manager，不重写扫描、同步或播放主链路。
- **阶段 3 实施完成：**阶段 3.1–3.4 本路线图范围内已完成 domain repository 收敛，受影响 SQLite/Go 用例通过；未迁移的 metadata indexing、TMDB 更新、scanner inventory、direct publish SQL 保持在各自独立业务工作流，不属于本阶段 Emby browse/library/user/playback 切片。
- **阶段 3 验收完成（2026-11-12）：**Docker PostgreSQL 16 下全量 Go 测试及 advisory lock 专项测试通过；PostgreSQL 结果与 SQLite 覆盖分别记录。进入阶段 4 前，仍需按路线图实施前端 feature 化切片；真实客户端矩阵验证继续留在阶段 5。

### 4. 前端渐进 feature 化 — 已完成（2026-11-12）
- **4.1 已完成：**新增 `resources.ts`、`resourceSync.ts` 与 `sync.ts`，承接资源列表、追更审核、资源变更/批次/任务、资源详情与操作、扫描/同步、AList 浏览/搜索、识别确认、manifest 检索和后台任务请求。`AdminResources`、`AdminXiaoyaSync` 与相关同步管理页通过业务 API 模块访问接口，不再直接构造 endpoint。
- **4.2 已完成：**按业务边界新增 `media.ts`、`libraries.ts`、`users.ts`、`settings.ts`、`auth.ts`、`tmdb.ts`、`logs.ts` 与 `jobs.ts`；更新首页、媒体详情/播放、目录选择、媒体库管理、系统用户、认证、TMDB 和日志 UI 调用点。Emby 图片 URL helper 及原始 File 上传/删除也归入媒体 API。相关页面/组件不直接调用 `request()` 或拼接 `/api/...` endpoint。
- `client.ts` 收敛为共享 JSON/File transport、认证状态/headers、401 处理与 toast 通知；业务 API 模块继续复用共享 transport。请求路径、方法、参数、响应投影、错误处理及认证行为保持不变；未涉及数据库或后端接口。
- **4.3 已评估：**`App.tsx` 与 `MediaWall.tsx` 的改动限于 API import/callsite 迁移。MediaWall 请求/取消状态与首页展示状态耦合，单独抽离会增加间接层且没有解决实际职责冲突；本阶段未作 UI 结构性拆分，符合“仅在必要时拆解”的约束。
- **最终验证（2026-11-12）：**`frontend` 下 `npm test` 通过（4 个测试文件、6 个测试）；`npm run build` 通过（TypeScript 与 Vite production build）；`git diff --check` 通过。Build 更新 `web/` 静态产物与 TypeScript build info，生成产物保留在当前工作树供发布流程决定是否纳入。
- **完成标准：**前端测试、TypeScript/build 均通过；已改业务页面不再直接构造低层 API endpoint；共享 transport/auth 与业务 API 边界清晰。

### 5. 按兼容性差距补齐功能并做发布验收 — 代码审计与本地门禁已完成，实机验收未完成
- **5.1 静态兼容性审计已完成（2026-11-12）：**P0 浏览/分页、PlaybackInfo、302、本地 Range/HEAD 与外挂字幕均有 handler 实现和自动化覆盖；真实客户端行为另列待验。P1 用户收藏、已看、Resume 和进度有用户隔离实现与测试；修复收藏/播放/进度持久化失败仍返回成功的问题，当前写入失败返回 500，畸形 JSON 返回 400，空请求体兼容保留。另确认 `/Sessions` 仍返回空数组，缺少活动会话协议响应；在没有已验证 DTO 和实际客户端请求前，不猜测响应结构。状态与证据见 `docs/EMBY-COMPATIBILITY-MATRIX.md`。
- **5.2 矩阵与证据分类已建立，实机执行待外部环境：**`docs/EMBY-COMPATIBILITY-MATRIX.md` 列出 Infuse、VidHub、Emby Theater、SenPlayer 的登录、浏览、分页、PlaybackInfo、302、Range/拖动、字幕、进度/续播场景，并区分自动化测试与真实客户端样本。王总本轮确认暂不提供实机环境，因此这些矩阵格均保留“未执行”；CDN Range 检查与模拟 E2E 不作为播放器实机证据。
- **5.3 WebSocket 评估已完成：**服务当前无 WebSocket upgrade、事件流或相应测试；现有 HTTP 进度接口不等价于 WebSocket。没有目标客户端证据证明需要该功能，故本阶段不臆造协议或新增实现，保留待客户端验收确认。转码/HLS 是明确非目标；`/Shows/NextUp` 恒空是既有产品决策，未改变。后端架构图已注明 WebSocket 尚未实现；折月亮 E2E 报告也已澄清其 NextUp 断言只验证内部计算 helper。
- **5.4 代码级验收已完成，实际恢复与 NAS 测量待目标环境：**迁移版本拒绝、失败重试、旧数据 backfill 与 PostgreSQL advisory lock 已有覆盖；先前已记录 Docker PostgreSQL 16 集成验收。新增 `internal/embyserver/backup_test.go` 使用临时假 `pg_dump` 覆盖备份发布、失败清理和 14 份保留策略。王总本轮确认暂不提供实机环境，因此真实 `pg_dump` 文件的 `pg_restore --list`/独立库 round-trip 以及目标 NAS CPU/RSS/磁盘、导入峰值、P50/P95 和并发播放预算测量均保留待办；不把配置上限当作实测结论。
- **本轮验证（2026-11-12）：**`go test ./...`、`go vet ./...`、`go build ./...`、`frontend` 下 `npm test`（4 个文件、6 个测试）、`npm run build` 与 `git diff --check` 通过。默认 Go 测试未配置 `TEST_DATABASE_URL`；PostgreSQL 16 结果引用前述阶段 2 独立 Docker 验收记录。
- **阶段 5 尚未达到发布完成标准：**必须在目标客户端和实际 NAS 完成矩阵、备份恢复与资源测量，并记录客户端版本/请求响应样本及 NAS 指标后，才能关闭阶段 5。尚未有证据的项目明确保留为待办，不以合成测试代替。
- **最终完成标准：**完整 Go/前端门禁通过、旧库升级/恢复可验证、目标客户端矩阵有真实结果、无未解释的双实现；go-emby 仅为历史源码渊源而非运行时依赖。

## 每个切片的交付记录

每次更新本路线图的“已完成”与“当前项”，记录改动路径、行为/数据库影响、执行的测试和未覆盖的验证。测试结果不能替代真实客户端或 Postgres 环境验证；无环境时明确保留为待办。
