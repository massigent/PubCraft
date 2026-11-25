package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestApiReorderVaultAlignment verifica che il riordino rispetti il progetto
// corrente quando il client invia X-PubCraft-Vault e resti compatibile con i
// client che non lo inviano.
func TestApiReorderVaultAlignment(t *testing.T) {
	isolateTestEnv(t)
	vDir := makeTestVault(t, `title: "Riordino"
chapters:
- file: 01.md
  title: "Uno"
- file: 02.md
  title: "Due"
`, map[string]string{"01.md": "# Uno\n", "02.md": "# Due\n"})
	setVault(vDir)

	// Header disallineato: 409 e nessuna modifica a book.yaml.
	req := newJSONRequest("POST", "/api/reorder", `{"files":["02.md","01.md"]}`)
	req.Header.Set("X-PubCraft-Vault", filepath.Join(t.TempDir(), "Altro Libro"))
	w := httptest.NewRecorder()
	apiReorder(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("header disallineato: atteso 409, ottenuto %d", w.Code)
	}
	if b := loadBook(); b == nil || len(b.Chapters) != 2 || b.Chapters[0].File != "01.md" {
		t.Fatalf("book.yaml modificato nonostante il 409: %+v", b)
	}

	// Header allineato: riordino applicato.
	req = newJSONRequest("POST", "/api/reorder", `{"files":["02.md","01.md"]}`)
	req.Header.Set("X-PubCraft-Vault", vDir)
	w = httptest.NewRecorder()
	apiReorder(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("header allineato: atteso 200, ottenuto %d", w.Code)
	}
	if b := loadBook(); b == nil || len(b.Chapters) != 2 || b.Chapters[0].File != "02.md" {
		t.Fatalf("ordine non applicato con header allineato: %+v", b)
	}

	// Senza header: compatibilità con client non-GUI e test esistenti.
	req = newJSONRequest("POST", "/api/reorder", `{"files":["01.md","02.md"]}`)
	w = httptest.NewRecorder()
	apiReorder(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("senza header: atteso 200, ottenuto %d", w.Code)
	}
	if b := loadBook(); b == nil || len(b.Chapters) != 2 || b.Chapters[0].File != "01.md" {
		t.Fatalf("ordine non applicato senza header: %+v", b)
	}
}

// TestSecurityMiddlewareWatchPDFRebuild verifica la guardia Sec-Fetch-Site
// sulla rigenerazione PDF via GET.
func TestSecurityMiddlewareWatchPDFRebuild(t *testing.T) {
	handler := securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name           string
		target         string
		secFetchSite   string
		expectedStatus int
	}{
		{"cross-site con rebuild", "/api/watch/pdf?rebuild=1", "cross-site", http.StatusForbidden},
		{"same-origin con rebuild", "/api/watch/pdf?rebuild=1", "same-origin", http.StatusOK},
		{"same-site con rebuild", "/api/watch/pdf?rebuild=1", "same-site", http.StatusOK},
		{"header assente con rebuild", "/api/watch/pdf?rebuild=1", "", http.StatusOK},
		{"cross-site senza rebuild", "/api/watch/pdf", "cross-site", http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+tc.target, nil)
		if tc.secFetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.expectedStatus {
			t.Errorf("%s: atteso %d, ottenuto %d", tc.name, tc.expectedStatus, w.Code)
		}
	}
}

// TestSecurityMiddlewareMutatingEndpoints verifica che i POST aggiunti ai
// prefissi mutanti siano trattati come tali (GET rifiutata).
func TestSecurityMiddlewareMutatingEndpoints(t *testing.T) {
	handler := securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, p := range []string{"/api/publication-preview", "/api/recents/remove", "/api/recents/clear", "/api/recents/prune", "/api/workspace"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+p, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: atteso 405, ottenuto %d", p, w.Code)
		}
	}
}
