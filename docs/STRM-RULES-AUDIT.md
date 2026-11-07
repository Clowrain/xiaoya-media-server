# STRM 与附件生成规则审计报告（`strm.py` / `strm2.py` 对照基线）

**基线文件**：
- [strm.py](../scripts/ref/strm.py)（242 行）
- [strm2.py](../scripts/ref/strm2.py)（238 行）
- **审计日期**：2026-11-07

---

## 1. 两份原始脚本逐行差异对比

通过 `diff -u scripts/ref/strm.py scripts/ref/strm2.py` 验证，两份脚本除**扩展名白名单**外其余代码逐字节完全一致：

| 维度 | `strm.py` | `strm2.py` | V1.0 合并基准（并集 + `full` 附件） |
|---|---|---|---|
| 视频扩展名 | `MP4, M4V, MKV, ISO, FLV, AVI, TS, WMV, MOV, RM, RMVB, WEBM`（12 种） | 缺少 `M4V`（11 种） | 采用并集（共 **12 种**，含 `M4V`），大小写不敏感 |
| 音频扩展名 | `WAV, MP3, FLAC, APE, WV, ALAC, M4A, AAC, WMA`（9 种） | 完全相同（9 种） | 保留全部 **9 种**，大小写不敏感 |
| 字幕扩展名 | `ASS, SRT, SSA, VTT, IDX, SUB`（6 种） | 完全相同（6 种） | 保留全部 **6 种**，并兼容复合语言后缀（如 `.zh.srt`、`.chs.ass`） |
| 图片与元数据扩展名 | `JPG, PNG, NFO, WEBP`（4 种） | **无**（仅同步字幕） | 默认 `attachments: full` 启用 `JPG, JPEG, PNG, WEBP, NFO`；支持配置切换为仅字幕模式 |

---

## 2. 原脚本核心处理逻辑逐项拆解

### 2.1 目录扫描与递归规则（`strm.py` L55–L120）
- **原脚本行为**：通过 WebDAV `client.list()` 硬编码嵌套循环 `url_1` 至 `url_5`，最多只扫描 **5 层子目录**；每次列目录 `time.sleep(1)`，最多重试 2 次（`while q < 3`）。
- **缺陷识别**：在深层目录（例如 `/每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/E01`，从根算起达 7 层）下，如果扫描根较浅，5 层硬编码会漏扫剧集。
- **Go 迁移规范**：
  - 改为**真正的递归遍历**（支持无限制深度，可配置最大深度防环保护，如 `max_depth: 32`）。
  - 支持双数据源输入并统一抽象为相对路径项：
    1. **Xiaoya SQLite (`strm_internal.db`) 节点树**（自动监控主通道，零网络开销）。
    2. **AList API / WebDAV 目录树**（主动入库及附件内容拉取通道）。

### 2.2 输出路径映射与 `%20` 还原（`strm.py` L51, L123–L125）
- **原脚本行为**：
  ```python
  save_mulu = r'/media/strm' + SCAN_PATH.replace("%20", " ") + "/"
  prefix = ".".join(b.replace(webdav_url, '').split(".")[0:-1])
  ext = b.replace(webdav_url, '').split(".")[-1]
  strm_file_temp = save_mulu + prefix.replace('%20', " ")
  ```
- **关键细节与边界修正**：
  1. **保留源相对目录结构**：当 `preserve_source_structure: true` 时，`output_root + "/" + clean(source_rel_path_without_ext) + ".strm"`。
  2. **`%20` 空格还原**：源路径若来自 WebDAV 编码路径中的 `%20`，本地文件名还原为空格 `" "`。
  3. **防止 `.strm.strm`（适配 `strm_internal.db` 特殊节点）**：
     - 在 Xiaoya `strm_internal.db` 中，文件节点（`is_dir = 0`）可能出现三种命名形态：
       - 形态 A：带原始媒体后缀，如 `01.mp4` -> 输出 `01.strm`。
       - 形态 B：节点名本身已带 `.strm`，如 `E01.strm` 或 `01.mp4.strm` -> 必须先剥离末尾 `.strm`（若剥离后末尾仍为已知媒体扩展名如 `.mp4` 则一并剥离），输出 `E01.strm` / `01.strm`，**严禁生成 `.strm.strm`**。
       - 形态 C：无扩展名剧集节点（如《折月亮》样本中的 `E01`，`is_dir = 0` 且 `content` 为有效播放 URL） -> 原 Python `".".join("E01".split(".")[0:-1])` 会退化为空字符串 `""`；Go 实现须判断无点号文件节点直接以 `name` 作为 `prefix`，输出 `E01.strm`。
  4. **路径穿越防护**：所有源节点名称必须清理 `..` 与非法根跳转，确保最终落盘路径严格位于 `output_root` 之下。

### 2.3 235 字符长度截断与冲突消解（`strm.py` L126–L129）
- **原脚本行为**：
  ```python
  if len(strm_file_temp) > 235:
      strm_file = strm_file_temp[:235] + "_xxx" + "." + 'strm'
  else:
      strm_file = strm_file_temp + "." + 'strm'
  ```
- **关键细节与边界修正**：
  1. **字符计数单位**：Python 3 的 `len(str)` 按 **Unicode 码点（Go `[]rune` 长度）** 计算，而非 UTF-8 字节数。为保持与 Python 输出一致，主判断采用 `utf8.RuneCountInString(strmFileTemp) > 235`。
  2. **Linux 文件系统单文件名 255 字节上限保护**：在 `ext4`/`xfs` 上单个文件名（`filepath.Base`）上限为 255 UTF-8 字节（85 个汉字即达 255 字节）。若 `len(baseBytes) > 242`（预留后缀字节），即使总 rune 数未超 235，也必须按 UTF-8 rune 边界安全截断文件名部分。
  3. **多集截断重名冲突消解**：
     - 若同一目录下两集超长文件名在前 235 个字符完全一致（集号在末尾被截掉），原脚本都会生成 `[:235]_xxx.strm` 导致后一集静默跳过或覆盖。
     - **兼容策略**：
       - 同一批次/目录中**首个**命中截断且无冲突的文件，保持生成 `runes[:235] + "_xxx.strm"`（与原脚本输出 100% 一致）。
       - 若检测到**不同源路径**映射到同一个截断目标路径（冲突），则使用确定性短哈希后缀：`runes[:228] + "_xxx_" + sha1(sourcePath)[:6] + ".strm"`，保证幂等且永不互相覆盖。

### 2.4 STRM 内容 URL 替换与编码规则（`strm.py` L49, L156, L179）
- **原脚本行为**：
  ```python
  b.replace(webdav_url, webdav_url_final)
   .replace(' ', '%20')
   .replace('\\', '%5C')
   .replace('#', '%23')
   .replace('^', '%5E')
   .replace('{', '%7B')
   .replace('}', '%7D')
   .replace('?', '%3F')
   .replace('&', '%26')
   .replace('=', '%3D')
   .replace('+', '%2B')
   .replace('@', '%40')
  ```
- **核心规律总结**：
  1. **保留中文可读性**：原脚本**不对中文 UTF-8 字符进行百分号编码**（如 `/d/每日更新/电视剧/...` 保持原样），仅对上述 **11 个 ASCII 特殊字符**（空格、`\`、`#`、`^`、`{`、`}`、`?`、`&`、`=`、`+`、`@`）做字面替换。
  2. **区分“由路径构造 URL”与“由 SQLite `content` 规范化 URL”**：
     - **场景 1（从源相对路径构造 URL）**：先将前缀替换为目标直链前缀（如 `http://xiaoya.host:5678/d`），再对**路径部分**执行上述 11 字符替换（因源文件名中若含 `?`、`&`、`=`、`#`、`+`，必须编码为 `%3F`、`%26`、`%3D`、`%23`、`%2B` 才不会被 HTTP 解析截断为 Query/Fragment）。
     - **场景 2（源来自 `strm_internal.db` 的 `content`，已含 `?sign=...` 或已部分编码）**：
       - **禁止直接原样盲写**，也**禁止对 `?sign=...` 查询串套用 `.replace('?', '%3F').replace('=', '%3D')`**！
       - 正确算法：
         1. 分离 URL 的 `Scheme://Host`、`Path` 与 `?Query`（以最后一个合法查询参数段如 `?sign=` 为界，或通过 URL 结构解析）。
         2. 若配置了自定义 `alist.strm_base_url`（URL 替换规则），替换 `Scheme://Host`（默认保留或标准化为配置地址）。
         3. 对 `Path` 部分执行**防二次编码**的扫描器：遇到合法的 `%[0-9A-Fa-f]{2}` 原样保留（绝不把 `%20` 变成 `%2520`），对未编码的上述 11 个特殊字符执行与 `strm.py` 完全一致的编码映射，保留 `/` 和中文字符。
         4. 重新拼接规范化后的 `?Query`（保留 `?sign=...` 结构完整）。

### 2.5 特殊符号文件名容错回退（`strm.py` L157–L181）
- **原脚本行为**：当 `open(strm_file, "w")` 因特殊字符在目标文件系统（如 SMB/NTFS/特殊挂载）报错时，执行字符替换表回退重试：
  - 目录全角冒号 `：` -> `.`
  - 相对路径字符替换表：
    `:` -> `.`、`|` -> `.`、`$` -> `.`、`%` -> `.`、`&` -> `_`、`#` -> `.`、`+` -> `_`、`\` -> `.`、`{` -> `.`、`}` -> `.`、`'` -> `.`
- **Go 迁移规范**：完整保留该二级 fallback 映射表（仅作用于输出根目录之后的**相对路径段**，不破坏 `output_root` 本身），并在任务日志中记录回退后的实际落盘路径。

### 2.6 幂等更新与原子写入（增强原脚本 L151 `os.path.exists`）
- **原脚本局限**：仅判断 `if not os.path.exists(strm_file)`，导致源 URL 变化（如更换片源、修复链接、更新 sign）时永远不会更新已有 `.strm`。
- **Go 迁移规范**：
  1. 计算期望写入内容的 `SHA-256`（或直接比对现有 `.strm` 字节内容）。
  2. 若目标文件已存在且内容完全一致：**跳过写入**，保持文件 `mtime` 不变（防止触发无意义的媒体库重扫）。
  3. 若文件不存在或内容发生变化：写入同目录隐藏临时文件 `.tmp-<name>-<rand>`，`fsync` 后通过 `os.Rename` **原子替换**目标 `.strm`。

### 2.7 附件同步规则（`strm.py` L184–L221 + 方案 5.4 `prefer` 策略）
- **URL 映射**：WebDAV `/dav/...` 替换为 `/d/...` 下载原始字节。
- **NFO `prefer` 策略**：
  1. 校验下载响应状态码必须为 `200 OK`，且内容非空、非 AList 错误 JSON/HTML（例如校验基本 XML/NFO 结构或非错误响应），**绝不创建空 NFO**。
  2. 计算远端/新内容 Hash 与本地已有 NFO Hash 比较，仅在新增或 Hash 变化时原子替换落盘。
  3. 无源端 NFO 时不生成占位空文件，交由媒体服务器 TMDB 刮削引擎处理。
- **字幕与图片**：
  - 完整保留源相对目录及原始文件名（以保证外挂字幕如 `E01.zh.ass`、`poster.jpg`、`tvshow.nfo` 与 `E01.strm` 同目录精确关联）。
  - 支持独立重试队列，单个附件下载失败不阻断 `.strm` 主文件入库。

---

## 3. Python / Go 兼容性测试矩阵（M6 验收基准）

在 M6 阶段将构建表驱动 Golden 测试集（必要时直接调用 `python3` 对照输出），覆盖以下全部场景：

| 编号 | 测试场景 | 输入样本特征 | 期望行为 |
|---|---|---|---|
| TC-01 | 标准剧集与中文路径 | `/每日更新/电视剧/国产剧/折月亮 (2026)/Season 1/01.mp4` | 保留中文路径，输出 `01.strm`，URL 中文不转义 |
| TC-02 | 扩展名并集（`M4V`） | `movie.m4v`（`strm.py` 支持、`strm2.py` 缺失） | 正常识别并生成 `movie.strm` |
| TC-03 | 特殊字符编码 | 路径含空格、`#`、`&`、`+`、`=`、`?`、`@`、`{tmdb-270431}` | 本地保留 `{tmdb-270431}`，STRM 内 URL 将 `{` `}` 编码为 `%7B` `%7D`、空格为 `%20` 等，与 `strm.py` 一致 |
| TC-04 | 已编码 URL 防二次编码 | 输入含 `%20` 或 `%E6%8A%98` | 本地文件名 `%20` 还原为空格；URL 内 `%20` 不变成 `%2520` |
| TC-05 | SQLite `.strm` 节点防重后缀 | `name = "E01.strm"` 或 `"01.mp4.strm"` | 输出 `E01.strm` / `01.strm`，不出现 `.strm.strm` |
| TC-06 | SQLite 无后缀节点 + 带 `sign` URL | `name = "E01"`, `content = "http://xiaoya.host:5678/d/.../01.mp4?sign=ab+cd="` | 输出 `E01.strm`，路径部分按规则编码，末尾 `?sign=ab+cd=` 查询参数原样保留 |
| TC-07 | 235 字符截断（无冲突） | `runeCount(strm_file_temp) == 245` | 输出 `runes[:235] + "_xxx.strm"`，与 `strm.py` 完全一致 |
| TC-08 | 235 字符截断（多集冲突） | 同目录两集前 235 字符完全相同 | 第 1 集 `_xxx.strm`，第 2 集 `_xxx_<hash6>.strm`，零覆盖丢失 |
| TC-09 | >5 层深层目录递归 | 7 层目录树（含 `{tmdb-270431}/Season 1/E01..E27`） | 27 集全部递归生成，突破原脚本 5 层限制 |
| TC-10 | 幂等重写与变更更新 | 连续两次相同输入 -> 第三次修改 URL | 第二次 `mtime` 不变；第三次原子更新为新 URL |
| TC-11 | NFO / 字幕 / 图片附件 | 同目录含 `tvshow.nfo`, `E01.ass`, `poster.jpg`, `fanart.webp` | 全部落盘至对应目录；空响应/500 响应不落空文件 |
