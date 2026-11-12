package embyserver

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) userIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		fail(w, 405, "PUT required")
		return
	}
	var b struct{ ID, Name, Password string }
	if !body(w, r, &b) {
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || (b.Password != "" && (len(b.Password) < 10 || len(b.Password) > 72)) {
		fail(w, 400, "名称不能为空，密码需要10–72字节")
		return
	}
	passwordHash := ""
	if b.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
		if err != nil {
			fail(w, 400, "无效密码")
			return
		}
		passwordHash = string(hash)
	}
	updated, err := a.userStore().UpdateIdentity(b.ID, b.Name, passwordHash)
	if err != nil {
		var identityErr *UserIdentityUpdateError
		if errors.As(err, &identityErr) {
			switch identityErr.Failure {
			case UserIdentityNameFailure:
				fail(w, 409, "用户名已存在或保存失败")
				return
			case UserIdentityPasswordFailure:
				fail(w, 500, "密码保存失败")
				return
			}
		}
		fail(w, 500, "保存失败")
		return
	}
	if !updated {
		fail(w, 404, "用户不存在")
		return
	}
	respond(w, M{"ok": true})
}
