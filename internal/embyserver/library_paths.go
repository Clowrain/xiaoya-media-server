package embyserver

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// An explicit empty list keeps a library with no indexed directories.
func (a *App) libraryPaths(lib, fallback string) []string {
	paths, err := a.libraryStore().Paths(lib, fallback)
	if err != nil {
		log.Printf("read library paths: %v", err)
	}
	return paths
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func (a *App) libraryFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "DELETE" && r.Method != "PUT" {
		fail(w, 405, "method")
		return
	}
	var b struct{ ID, Path, OldPath string }
	if !body(w, r, &b) {
		return
	}
	a.libraryConfig.Lock()
	defer a.libraryConfig.Unlock()
	if r.Method != "POST" {
		if !a.scan.TryLock() {
			fail(w, 409, "扫描中，请完成后再修改目录")
			return
		}
		defer a.scan.Unlock()
	}
	library, err := a.libraryStore().GetByID(b.ID)
	if err != nil {
		fail(w, 404, "媒体库不存在")
		return
	}
	fallback := library.Path
	paths := a.libraryPaths(b.ID, fallback)
	old := filepath.Clean(mediaPath(b.OldPath))
	if r.Method == "PUT" {
		found := false
		kept := []string{}
		for _, p := range paths {
			if p == old {
				found = true
			} else {
				kept = append(kept, p)
			}
		}
		if !found {
			fail(w, 404, "原目录不存在")
			return
		}
		paths = kept
	}
	path := filepath.Clean(mediaPath(b.Path))
	if r.Method == "POST" || r.Method == "PUT" {
		if allowedMediaPath(path) {
			_ = os.MkdirAll(path, 0755)
		}
		real, e := filepath.EvalSymlinks(path)
		if e != nil || !allowedMediaPath(real) {
			fail(w, 400, "请选择已挂载媒体目录下的现有目录")
			return
		}
		fi, e := os.Stat(real)
		if e != nil || !fi.IsDir() {
			fail(w, 400, "无效目录")
			return
		}
		path = real
		for _, lib := range a.libraries() {
			for _, p := range lib["Locations"].([]string) {
				if r.Method == "PUT" && lib["Id"] == b.ID && p == old {
					continue
				}
				if pathsOverlap(p, path) {
					fail(w, 409, "目录不能重复或互相包含")
					return
				}
			}
		}
		paths = append(paths, path)
	} else {
		found := false
		kept := []string{}
		for _, p := range paths {
			if p == path {
				found = true
			} else {
				kept = append(kept, p)
			}
		}
		if !found {
			fail(w, 404, "目录不属于此媒体库")
			return
		}
		paths = kept
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "索引事务失败")
		return
	}
	defer tx.Rollback()
	e = a.libraryStore().SavePaths(tx, b.ID, paths)
	if e == nil && (r.Method == "DELETE" || r.Method == "PUT") {
		removed := path
		if r.Method == "PUT" {
			removed = old
		}
		e = a.mediaItems().DeleteItemsUnderLibraryPath(tx, b.ID, removed)
	}
	primary := "/media/.go-emby-empty-" + b.ID
	if len(paths) > 0 {
		primary = paths[0]
	}
	if e == nil {
		e = a.libraryStore().UpdatePath(tx, b.ID, primary)
	}
	if e == nil {
		e = a.libraryStore().UpdateItemCount(tx, b.ID)
	}
	if e != nil {
		fail(w, 500, "目录索引更新失败")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "目录索引提交失败")
		return
	}
	if r.Method == "DELETE" {
		if e = a.cleanupMedia(); e != nil {
			fail(w, 500, "索引已移除，媒体信息清理失败")
			return
		}
	}
	respond(w, M{"ok": true, "Locations": paths, "queued": r.Method != "DELETE", "Message": "已加入队列，请在实时日志中查看"})
	if r.Method == "POST" || r.Method == "PUT" {
		go a.scanLibraryMode(b.ID, true)
	}
}
