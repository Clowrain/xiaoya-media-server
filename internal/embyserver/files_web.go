package embyserver

import (
	"net/http"
	"strings"
)

func (a *App) filesRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/files" || r.URL.Path == "/api/files/rename" {
		a.filesAPI(w, r)
		return true
	}
	if r.URL.Path == "/files" || strings.HasPrefix(r.URL.Path, "/files/") {
		http.Redirect(w, r, "/", http.StatusFound)
		return true
	}
	return false
}
