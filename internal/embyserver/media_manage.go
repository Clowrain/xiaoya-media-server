package embyserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) manageMediaItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "DELETE" {
		fail(w, 405, "PUT or DELETE required")
		return
	}
	var b struct {
		ID, Name        string
		DeleteDirectory bool
	}
	if !body(w, r, &b) {
		return
	}
	x, e := a.item(b.ID)
	if e != nil {
		fail(w, 404, "媒体不存在")
		return
	}
	if r.Method == "PUT" {
		b.Name = strings.TrimSpace(b.Name)
		if b.Name == "" || len([]rune(b.Name)) > 256 {
			fail(w, 400, "名称长度必须为1–256字")
			return
		}
		tx, err := a.db.Begin()
		if err != nil {
			fail(w, 500, "保存失败")
			return
		}
		defer tx.Rollback()
		e = a.mediaItems().SetManualName(tx, x.ID, b.Name)
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "保存失败")
			return
		}
		respond(w, M{"ok": true})
		return
	}
	items, e := a.mediaItems().ItemTree(x.ID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	targets := []string{}
	if b.DeleteDirectory {
		p := x.Path
		if x.URL != "" {
			p = filepath.Dir(p)
		}
		targets = append(targets, p)
	} else {
		for _, v := range items {
			if v.URL != "" {
				if !strings.EqualFold(filepath.Ext(v.Path), ".strm") {
					fail(w, 400, "仅允许删除 STRM")
					return
				}
				targets = append(targets, v.Path)
			}
		}
	}
	// Validate every target before removing anything; never delete a library root or shared directory.
	for _, p := range targets {
		real, err := filepath.EvalSymlinks(p)
		if os.IsNotExist(err) && !b.DeleteDirectory {
			continue
		}
		if err != nil || !allowedMediaPath(real) {
			fail(w, 400, "路径无效或超出媒体目录")
			return
		}
		for _, root := range mediaRoots() {
			if real == root {
				fail(w, 400, "不能删除媒体挂载根目录")
				return
			}
		}
		if b.DeleteDirectory {
			for _, l := range a.libraries() {
				for _, root := range l["Locations"].([]string) {
					if root == real || strings.HasPrefix(root, real+"/") {
						fail(w, 400, "不能删除媒体库根目录")
						return
					}
				}
			}
			count := 0
			selected := map[string]bool{}
			for _, v := range items {
				selected[v.ID] = true
			}
			pathItems, err := a.mediaItems().ItemsAtOrUnderPath(real)
			if err != nil {
				fail(w, 500, err.Error())
				return
			}
			for _, pathItem := range pathItems {
				if !selected[pathItem.ID] {
					count++
				}
			}
			if count > 0 {
				fail(w, 400, "目录包含其他媒体，请取消连同目录删除")
				return
			}
		}
	}
	for _, p := range targets {
		if b.DeleteDirectory {
			e = os.RemoveAll(p)
		} else {
			e = os.Remove(p)
		}
		if e != nil && !os.IsNotExist(e) {
			fail(w, 500, fmt.Sprintf("删除文件失败: %v", e))
			return
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer tx.Rollback()
	for i := len(items) - 1; i >= 0; i-- {
		if e = a.mediaItems().DeleteItem(tx, items[i].ID); e != nil {
			fail(w, 500, e.Error())
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	respond(w, M{"ok": true})
}
func prefixedCols(prefix string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}
