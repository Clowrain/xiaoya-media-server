# 《折月亮 (2026)》端到端全链路测试报告（V1.0 规范 11.5 节交付物）

- **测试套件**：[zheyueliang_e2e_test.go](../internal/embyserver/zheyueliang_e2e_test.go)（`TestZheYueLiang14StepEndToEnd`）
- **协调流水线**：[pipeline.go](../internal/integration/pipeline.go)
- **执行日期**：2026-11-07
- **验证状态**：**14 / 14 步骤全部通过（PASS）**

---

## 1. 真实样本拓扑参数

严格对齐技术方案第 3.1 节与第 11.5 节样本定义：

| 属性 | 验证值 |
|---|---|
| 监控根目录 | `/每日更新/电视剧/国产剧`（节点 ID `639780`，父链路 `568922 -> 617762 -> 639780`） |
| 剧集目录实际名 | `折月亮 (2026)`（节点 ID `641716`） |
| 元数据标签子目录 | `{tmdb-270431}`（节点 ID `641717`，提取 TMDB ID = `270431`） |
| 季目录 | `Season 1`（节点 ID `641718`） |
| 剧集文件节点 | `E01` – `E27`（节点 ID `641719` – `641745`，`is_dir = 0`，无点号文件名） |
| 子树节点总数 | **30**（3 个目录节点 + 27 个剧集文件节点） |

---

## 2. 14 步端到端逐项验证结果

| 步骤 | 规范要求 | 执行动作与断言细节 | 结果 |
|---|---|---|---|
| **01** | 配置监控目录并定位实际名称 | 验证未带年份后缀的 `/每日更新/电视剧/国产剧/折月亮` 精确返回 `ErrPathNotFound`（拒绝模糊误匹配）；注册 `/每日更新/电视剧/国产剧` 并精确定位 `639780` -> `折月亮 (2026)` (`641716`) | PASS |
| **02** | 递归读取 30 个节点 | 通过只读 WAL 连接（`mode=ro`, `query_only=1`）分批 BFS 读取全部 30 个节点，生成基线批次（`is_baseline=true`, `TotalSeen=30`, `AddedCount=30`, `FailedCount=0`） | PASS |
| **03** | 识别 TMDB ID `270431` | 从 `{tmdb-270431}` 路径段及剧集子目录自动提取 `270431` | PASS |
| **04** | 识别 `Season 1` 和 `E01–E27` | 全部 27 个无扩展名节点精确解析为 `S01E01` 至 `S01E27` | PASS |
| **05** | 按原 Python 规则生成 STRM | 在 `<output_root>/每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/` 下原子生成 `E01.strm` – `E27.strm` | PASS |
| **06** | 检查输出文件名和播放 URL | 本地保留 `{tmdb-270431}` 与中文目录名，无 `.strm.strm`；STRM 内容保留中文可读路径与 `?sign=INIT_SIGN_XX:0` 参数完整性 | PASS |
| **07** | 检查 NFO、字幕、图片 | 成功同步 `poster.jpg` 与 `E01.chs.srt` 并写入 `media_attachments`；源端无 NFO 时**不生成空 `tvshow.nfo`**；存在合法本地 NFO 时按 `prefer` 策略优先采用本地元数据 | PASS |
| **08** | 媒体服务器建立电视剧、季、集关系 | `seriesDirectory()` 自动跨越 `{tmdb-270431}` 纯标签目录归并至 `折月亮 (2026)`，建立 `Series(折月亮, 2026) -> Season 1 -> Episode(1..27)` 三级结构 | PASS |
| **09** | TMDB 元数据正确关联 | 调用 TMDB v3 接口刮削 `tv/270431` 及 `tv/270431/season/1`，正确填充中文剧名、简介、评分、海报及分集标题/时长 | PASS |
| **10** | 客户端浏览和播放 | 验证 Xiaoya `:5678` 播放地址 302 CDN 重定向、`E01.chs.srt` 动态转换 WebVTT 字幕流，以及 `E01` 完播后 `Shows/NextUp` 准确返回 `E02` | PASS |
| **11** | 再次扫描无重复实体和无意义重写 | 二次完整扫描 `AddedCount=0`, `ModifiedCount=0`，`E01.strm`–`E27.strm` 文件 `mtime` 保持完全不变 | PASS |
| **12** | 模拟新增一集，自动发现并生成 | 向 SQLite 插入 `E28` (`ID 641746`)，执行主键游标增量扫描 `ScanRootIncremental`，毫秒级识别 `AddedCount=1` 并落盘 `E28.strm` | PASS |
| **13** | 模拟 `content` 变化，正确更新 STRM | 更新 `E01` (`ID 641719`) 的 `content` 为 4K 新签名地址，完整扫描识别 `ModifiedCount=1` 并通过临时文件 + `os.Rename` 原子替换 `E01.strm` | PASS |
| **14** | 模拟资源消失，标记 `MISSING` 并保留本地文件 | 从 SQLite 删除 `E28` (`ID 641746`)，完整扫描识别 `MissingCount=1`，资源状态置为 `MISSING`，本地 `E28.strm` 与字幕文件完整保留（`keep` 策略） | PASS |
