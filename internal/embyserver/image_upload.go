package embyserver

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// Image writes require a logged-in administrator or an existing integration key.
// Keep public signed-image GET/HEAD handling separate from mutations.
func (a *App) imageUploadRoute(w http.ResponseWriter, r *http.Request, u User, p string) bool {
	id, kind, ok := imageRequest(p)
	if !ok || (r.Method != http.MethodPost && r.Method != http.MethodDelete) {
		return false
	}
	if !u.Admin && !u.API {
		fail(w, 403, "需要管理员或API密钥")
		return true
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if kind != "Primary" || len(parts) > 5 || (len(parts) == 5 && parts[4] != "0") || (q(r, "Index") != "" && q(r, "Index") != "0") {
		fail(w, 400, "仅支持主封面（索引0）操作")
		return true
	}
	exists, existsErr := a.coverEntityExists(id)
	if existsErr != nil || !exists {
		fail(w, 404, "媒体不存在")
		return true
	}
	if r.Method == http.MethodDelete {
		a.write.Lock()
		err := a.coverStore().Delete(a.db, id)
		a.write.Unlock()
		if err != nil {
			fail(w, 500, "清除封面失败")
			return true
		}
		a.invalidateLibraryCollage(id)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	const limit = 5 << 20
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(base64.StdEncoding.EncodedLen(limit)+4096)))
	if err != nil {
		fail(w, 413, "封面最大5MB")
		return true
	}
	mime := http.DetectContentType(data)
	if !coverMIME(mime) {
		data, err = base64.StdEncoding.DecodeString(string(bytes.TrimSpace(data)))
		if err != nil {
			fail(w, 400, "无效图片或Base64数据")
			return true
		}
		mime = http.DetectContentType(data)
	}
	if len(data) > limit {
		fail(w, 413, "封面最大5MB")
		return true
	}
	if !coverMIME(mime) {
		fail(w, 400, "仅支持JPEG、PNG、WebP")
		return true
	}
	a.write.Lock()
	err = a.coverStore().Save(a.db, id, mime, data)
	a.write.Unlock()
	if err != nil {
		fail(w, 500, "保存失败")
		return true
	}
	a.invalidateLibraryCollage(id)
	w.WriteHeader(http.StatusNoContent)
	return true
}
func coverMIME(mime string) bool {
	return mime == "image/jpeg" || mime == "image/png" || mime == "image/webp"
}
