package main

import (
	"net/http"
	"path/filepath"
	"sync"
)

// Serialize project-changing requests with project writes. A context check alone
// is insufficient if another request can switch vaults after the check.
var projectRequestMu sync.Mutex

func isProjectMutation(method, path string) bool {
	if method != http.MethodPost && method != http.MethodDelete && method != http.MethodPut {
		return false
	}
	clean := filepath.Clean(path)
	switch clean {
	case "/api/newchapter", "/api/deletechapter", "/api/reorder", "/api/setstyle",
		"/api/setfont", "/api/setopening", "/api/setscenebreak", "/api/addcover",
		"/api/delcover", "/api/useasepubcover", "/api/uploadimage", "/api/settitle",
		"/api/note-to-chapter", "/api/note", "/api/delnote":
		return true
	}
	return false
}

func projectGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := filepath.Clean(r.URL.Path)
		structural := isProjectMutation(r.Method, cleanPath)
		guarded := structural
		switch cleanPath {
		case "/api/save", "/api/build", "/api/publication-preview", "/api/openat",
			"/api/newproject", "/api/import", "/api/importdocx", "/api/import-manuscript",
			"/api/settitle", "/api/note-to-chapter", "/api/closebook", "/api/note", "/api/delnote":
			guarded = true
		}
		if !guarded {
			next.ServeHTTP(w, r)
			return
		}
		projectRequestMu.Lock()
		defer projectRequestMu.Unlock()
		if structural {
			origin := r.Header.Get("X-PubCraft-Vault")
			active := getVaultDir()
			if origin == "" || active == "" || filepath.Clean(origin) != filepath.Clean(active) {
				jsonErr(w, http.StatusConflict, "Il progetto è cambiato: operazione rifiutata. Riapri il libro prima di modificarlo.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
