# Xiaoya 播放链路修复方案与验证

## 使用与配置

播放器连接本项目的 Emby 兼容接口。服务端读取 STRM，向 Xiaoya 获取播放地址，再向播放器返回 302。最终地址是网盘 CDN 时由播放器直连 CDN；Xiaoya 直接出流时由播放器访问 Xiaoya。

在 `deployments/.env` 中设置：

```dotenv
ALIST_BASE_URL=http://<NAS_IP>:5678
STRM_TARGET_BASE_URL=http://xiaoya.host:5678
XIAOYA_USERNAME=<AList 用户名>
XIAOYA_PASSWORD=<AList 密码>
ALIST_TOKEN=
```

用户名和密码方式会自动登录并在令牌失效后重新登录。`ALIST_TOKEN` 是另一种接入方式；填写后优先使用该固定令牌，不再通过用户名密码自动刷新。凭据放在 Git 忽略的 `.env` 中。

`ALIST_BASE_URL` 需要能被服务端访问；当 Xiaoya 返回代理出流地址时，播放器也需要能访问该地址。媒体库应包含 STRM 输出目录。

## 问题与修复决策

1. **首次直接出流被拒绝**：原代码遇到首个 `200 video/*` 或 `audio/*` 响应返回 502。现在首次与中间跳转后的媒体响应统一处理：关闭响应体、返回当前播放地址，不在解析阶段搬运视频。
2. **循环与跳转耗尽被当成成功**：为 HTTP 重定向、JSON URL、文本 URL 共用已访问地址集合；重复地址报循环错误。每次解析最多发出 10 次请求，第 10 次得到有效终点仍可成功，耗尽后报错。
3. **AList 刷新吞掉内部地址解析失败**：`raw_url` 指向内部地址时必须解析成功才能返回；失败会向调用方传递。若同时获得新签名，仍允许用新签名重试原始 `/d/` 地址。
4. **实机内部端口跳转形成回环**：本次两类资源的 `/d/` 入口均返回 `172.17.0.2:45678`，改写为 NAS 入口后回到原路径。检测到 `/d/` 循环时，复用已有的已认证 AList `/api/fs/get` 查询真实地址；查询和新签名重试均失败则返回 502，不缓存失败结果。

正常 CDN 重定向继续使用原流程。播放器 UA 同时通过 `User-Agent` 与 `X-Alist-OriUA` 传给解析服务，Emby 登录令牌不转发给 Xiaoya。

## 回归入口

```bash
go test ./internal/embyserver -run 'Test(ValidateResolvedLink|Xiaoya|DirectSTRM)' -count=1
go test ./...
go build ./cmd/server
```

`xiaoya_redirect_test.go` 覆盖：首次音视频出流、解析时关闭响应且不读取视频、客户端跟随 302 后的 HEAD/Range、三种响应格式下的自环/双节点循环/次数上限、最后一次请求成功、失败不缓存、AList 回环恢复和新签名重试。

`xiaoya_live_test.go` 提供显式启用的实机检查。通过运行环境注入 AList 凭据，设置 `LIVE_XIAOYA_URL` 和 `LIVE_XIAOYA_PATH`，执行：

```bash
go test ./internal/embyserver -run '^TestXiaoyaLivePlayback$' -count=1 -v
```

该测试调用项目 STRM URL 生成器、播放解析器和 AList 登录客户端，再分别读取视频开头与 1 MB 偏移处的 16 字节，要求 HTTP 206 及正确的 `Content-Range`。日志只显示播放主机与读取结果，不输出凭据和带签名 URL。普通 `go test` 不主动访问真实网盘。

## 2026-11-07 实机结果

本次 `go test ./... -count=1` 有 68 个顶层测试通过；75 个数据库相关测试因未配置 `TEST_DATABASE_URL` 跳过，40 万条目录规模测试未启用，实机测试在普通回归中跳过后按下表单独执行。`go build ./cmd/server` 成功。

测试服务为用户已打开的局域网 Xiaoya，镜像页面显示 `v.2026.1002.2008(B)`、数据版本 `0.58.71`。游客 API 返回 401，配置 AList 用户名密码后自动登录成功。

| 样本 | 项目解析器得到的主机 | Range 0–15 | Range 1048576–1048591 |
|---|---|---|---|
| 阿里：`/每日更新/电视剧/国产剧/折月亮/08.mp4` | `dl1-v6.aliyundrive.cloud` | 206，16 字节 | 206，16 字节 |
| 115：`/每日更新/电视剧/国产剧（115）/雨霖铃/Zhan.Zhao.Adventures.S01E01.2026.2160p.WEB-DL.HQ.HDR.H.265.AAC-HiveWeb.mp4` | `cdnfhnfile.115cdn.net` | 206，16 字节 | 206，16 字节 |

AList 页面中两类样本均进入播放状态；115 视频读取到 `readyState=4`，播放进度推进至约 8 秒。

当前实机证据覆盖资源可访问性、项目解析器取得真实 CDN 地址与分段读取。NAS 上部署修复版本后，仍需在目标 Emby 客户端完成海报选集、字幕和观看进度续播验收；负责人为部署操作人及客户端验收人。
