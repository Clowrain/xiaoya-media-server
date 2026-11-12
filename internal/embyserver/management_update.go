package embyserver

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) createLibrary(w http.ResponseWriter, name, kind, path string, paths []string) {
	a.libraryConfig.Lock()
	defer a.libraryConfig.Unlock()
	if len(paths) == 0 {
		paths = []string{path}
	}
	if strings.TrimSpace(name) == "" || len(paths) > 100 {
		fail(w, 400, "请输入名称，目录最多100个")
		return
	}
	validated := []string{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		mp := mediaPath(p)
		if allowedMediaPath(mp) {
			_ = os.MkdirAll(mp, 0755)
		}
		real, e := filepath.EvalSymlinks(mp)
		if e != nil || !allowedMediaPath(real) {
			fail(w, 400, "请选择已挂载媒体目录下的现有目录: "+p)
			return
		}
		fi, e := os.Stat(real)
		if e != nil || !fi.IsDir() {
			fail(w, 400, "无效目录: "+p)
			return
		}
		for _, prev := range validated {
			if pathsOverlap(real, prev) {
				fail(w, 409, "目录不能重复或互相包含")
				return
			}
		}
		for _, lib := range a.libraries() {
			for _, prev := range lib["Locations"].([]string) {
				if pathsOverlap(real, prev) {
					fail(w, 409, "媒体库目录不能重复或互相包含")
					return
				}
			}
		}
		validated = append(validated, real)
	}
	if len(validated) == 0 {
		fail(w, 400, "请至少指定一个有效的媒体目录")
		return
	}
	if kind != "tvshows" {
		kind = "movies"
	}
	key := id()
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "事务失败")
		return
	}
	defer tx.Rollback()
	e = a.libraryStore().Create(tx, LibraryRecord{ID: key, Name: name, Path: validated[0], Kind: kind})
	if e == nil {
		e = a.libraryStore().SavePaths(tx, key, validated)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 409, "媒体库保存失败")
		return
	}
	go a.scanLibrary(key)
	respond(w, M{"Id": key, "queued": true, "Message": "已加入队列，请在实时日志中查看"})
}

func (a *App) deleteLibrary(w http.ResponseWriter, libID string) {
	a.libraryConfig.Lock()
	defer a.libraryConfig.Unlock()

	libID = strings.TrimSpace(libID)
	if libID == "" {
		fail(w, 400, "缺少媒体库 ID 参数")
		return
	}

	if _, err := a.libraryStore().GetByID(libID); err != nil {
		fail(w, 404, "媒体库不存在或已被删除")
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "启动数据库事务失败: "+err.Error())
		return
	}
	defer tx.Rollback()

	// 彻底级联清理从属表，杜绝孤儿数据
	if err := a.libraryStore().DeleteOrderPosition(tx, libID); err != nil {
		log.Printf("delete library order: %v", err)
	}
	_, _ = tx.Exec("DELETE FROM scan_snapshots WHERE lib=?", libID)
	if err := a.libraryStore().DeletePaths(tx, libID); err != nil {
		log.Printf("delete library paths: %v", err)
	}
	if err := a.mediaItems().DeleteItemsByLibrary(tx, libID); err != nil {
		log.Printf("delete library media items: %v", err)
	}
	res, err := a.libraryStore().Delete(tx, libID)
	if err != nil {
		fail(w, 500, "删除媒体库失败: "+err.Error())
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		fail(w, 404, "媒体库不存在或已被删除")
		return
	}

	if err := tx.Commit(); err != nil {
		fail(w, 500, "提交删除事务失败: "+err.Error())
		return
	}

	// 异步清理关联孤儿元数据，不阻塞当前响应
	go func() {
		_ = a.cleanupMedia()
	}()

	respond(w, M{"ok": true, "message": "媒体库已成功删除"})
}

func (a *App) updateLibrary(w http.ResponseWriter, libID, name, kind string, paths []string) {
	a.libraryConfig.Lock()
	defer a.libraryConfig.Unlock()

	libID = strings.TrimSpace(libID)
	if libID == "" {
		fail(w, 400, "媒体库 ID 不能为空")
		return
	}

	currentLibrary, err := a.libraryStore().GetByID(libID)
	if err != nil {
		fail(w, 404, "媒体库不存在")
		return
	}
	curName, curKind := currentLibrary.Name, currentLibrary.Kind

	newName := strings.TrimSpace(name)
	if newName == "" {
		newName = curName
	}
	newKind := strings.TrimSpace(kind)
	if newKind == "" {
		newKind = curKind
	}
	if newKind != "tvshows" && newKind != "movies" {
		newKind = "movies"
	}

	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "启动数据库事务失败: "+err.Error())
		return
	}
	defer tx.Rollback()

	if err := a.libraryStore().UpdateDetails(tx, libID, newName, newKind); err != nil {
		fail(w, 500, "更新媒体库失败: "+err.Error())
		return
	}

	if len(paths) > 0 {
		validated := []string{}
		for _, p := range paths {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			mp := mediaPath(p)
			if allowedMediaPath(mp) {
				_ = os.MkdirAll(mp, 0755)
			}
			real, e := filepath.EvalSymlinks(mp)
			if e != nil || !allowedMediaPath(real) {
				fail(w, 400, "请选择已挂载媒体目录下的现有目录: "+p)
				return
			}
			fi, e := os.Stat(real)
			if e != nil || !fi.IsDir() {
				fail(w, 400, "无效目录: "+p)
				return
			}
			for _, prev := range validated {
				if pathsOverlap(real, prev) {
					fail(w, 409, "目录不能重复或互相包含")
					return
				}
			}
			for _, lib := range a.libraries() {
				if lib["Id"].(string) == libID {
					continue
				}
				for _, prev := range lib["Locations"].([]string) {
					if pathsOverlap(real, prev) {
						fail(w, 409, "媒体库目录与现有媒体库重复或互相包含")
						return
					}
				}
			}
			validated = append(validated, real)
		}
		if len(validated) == 0 {
			fail(w, 400, "请至少保留一个有效的媒体目录")
			return
		}
		if err := a.libraryStore().SavePaths(tx, libID, validated); err != nil {
			fail(w, 500, "更新媒体库目录失败: "+err.Error())
			return
		}
		if err := a.libraryStore().UpdatePath(tx, libID, validated[0]); err != nil {
			fail(w, 500, "更新媒体库主路径失败: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		fail(w, 500, "提交更新事务失败: "+err.Error())
		return
	}

	respond(w, M{"ok": true, "message": "媒体库已成功更新"})
}

type scanSchedule struct {
	Enabled   bool
	Frequency string
	Time      string
	Weekday   int
	Minutes   int
	Next      int64
}

func (a *App) getScanSchedule() scanSchedule {
	c := scanSchedule{Frequency: "daily", Time: "03:00", Minutes: 60}
	var raw string
	if a.db.QueryRow("SELECT v FROM settings WHERE k='full_scan_schedule'").Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	return c
}
func nextScan(c scanSchedule, now time.Time) time.Time {
	if c.Frequency == "minutes" {
		return now.Add(time.Duration(c.Minutes) * time.Minute)
	}
	t, _ := time.Parse("15:04", c.Time)
	n := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if !n.After(now) {
		n = n.AddDate(0, 0, 1)
	}
	if c.Frequency == "weekly" {
		for int(n.Weekday()) != c.Weekday {
			n = n.AddDate(0, 0, 1)
		}
	}
	return n
}
func (a *App) saveScanSchedule(c scanSchedule) error {
	raw, _ := json.Marshal(c)
	_, e := a.db.Exec("INSERT INTO settings(k,v) VALUES('full_scan_schedule',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", string(raw))
	return e
}
func (a *App) scanScheduleAPI(w http.ResponseWriter, r *http.Request) {
	a.libraryConfig.Lock()
	defer a.libraryConfig.Unlock()
	if r.Method == "GET" {
		respond(w, a.getScanSchedule())
		return
	}
	if r.Method != "PUT" {
		fail(w, 405, "method")
		return
	}
	var c scanSchedule
	if !body(w, r, &c) {
		return
	}
	_, e := time.Parse("15:04", c.Time)
	if (c.Frequency != "daily" && c.Frequency != "weekly" && c.Frequency != "minutes") || e != nil || c.Weekday < 0 || c.Weekday > 6 || c.Minutes < 1 || c.Minutes > 10080 {
		fail(w, 400, "无效定时时间")
		return
	}
	c.Next = nextScan(c, time.Now().UTC()).Unix()
	if e = a.saveScanSchedule(c); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	respond(w, c)
}
func (a *App) runScanSchedule() {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for now := range tick.C {
		a.libraryConfig.Lock()
		c := a.getScanSchedule()
		due := c.Enabled && c.Next > 0 && now.Unix() >= c.Next
		if due {
			c.Next = nextScan(c, now.UTC()).Unix()
			if a.saveScanSchedule(c) != nil {
				due = false
			}
		}
		a.libraryConfig.Unlock()
		if due {
			a.jobs.mu.Lock()
			busy := len(a.jobs.pending) > 0 || a.jobs.running[0] > 0
			a.jobs.mu.Unlock()
			if !busy {
				a.scanAll()
			}
		}
	}
}
func (a *App) authWarning(r *http.Request, name, reason string) {
	// Avoid amplifying repeated unauthenticated requests into unbounded log work.
	a.activity.mu.Lock()
	for i := len(a.activity.entries) - 1; i >= 0; i-- {
		v := a.activity.entries[i]
		if v.Category == "warning" && v.Username == name && v.IP == r.RemoteAddr && v.Error == reason && time.Since(v.Updated) < time.Minute {
			v.Updated = time.Now()
			a.activity.mu.Unlock()
			return
		}
	}
	a.activity.mu.Unlock()
	key := a.newActivity("warning", "", "用户验证失败")
	a.changeActivity(key, func(v *activityEntry) {
		v.State = "error"
		v.Username = name
		v.IP = r.RemoteAddr
		v.Device = device(r)
		v.Client = r.UserAgent()
		v.Error = reason
	})
}
