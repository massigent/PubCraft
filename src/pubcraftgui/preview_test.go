package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationPreviewValidationAndGuards(t *testing.T) {
	tmpDir := t.TempDir()
	bookYAML := `title: Test Book
author: Author Name
language: it
chapters:
  - file: ch1.md
    title: Capitolo 1
`
	if err := os.WriteFile(filepath.Join(tmpDir, "book.yaml"), []byte(bookYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ch1.md"), []byte("# Capitolo 1\n\nTesto prova."), 0600); err != nil {
		t.Fatal(err)
	}

	setVault(tmpDir)
	defer setVault("")

	handler := projectGuard(http.HandlerFunc(apiPublicationPreview))

	// 1. Vault mancante -> 400
	{
		body, _ := json.Marshal(map[string]any{
			"vault":  "",
			"target": "print",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/publication-preview", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusConflict && w.Code != http.StatusBadRequest {
			t.Fatalf("Atteso 400 o 409 su vault mancante, ottenuto %d", w.Code)
		}
	}

	// 2. Vault disallineato (altro progetto) -> 409 Conflict
	{
		body, _ := json.Marshal(map[string]any{
			"vault":  "/tmp/altro-progetto",
			"target": "print",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/publication-preview", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("Atteso 409 su vault disallineato, ottenuto %d", w.Code)
		}
	}

	// 3. Target non valido -> 400
	{
		body, _ := json.Marshal(map[string]any{
			"vault":  tmpDir,
			"target": "audiobook",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/publication-preview", bytes.NewReader(body))
		req.Header.Set("X-PubCraft-Vault", tmpDir)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("Atteso 400 su target non valido, ottenuto %d: %s", w.Code, w.Body.String())
		}
	}

	// 4. Trim non valido -> 400
	{
		body, _ := json.Marshal(map[string]any{
			"vault":  tmpDir,
			"target": "print",
			"settings": map[string]any{
				"trim": "invalid_trim_size",
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/publication-preview", bytes.NewReader(body))
		req.Header.Set("X-PubCraft-Vault", tmpDir)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("Atteso 400 su trim non valido, ottenuto %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestPublicationPreviewFingerprintsAndCache(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "book.yaml"), []byte("title: Hello\nchapters: []\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rev1, err := computeSourceRevision(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Modifica file -> revisione cambia
	if err := os.WriteFile(filepath.Join(tmpDir, "ch1.md"), []byte("# First Chapter"), 0600); err != nil {
		t.Fatal(err)
	}
	rev2, err := computeSourceRevision(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if rev1 == rev2 {
		t.Fatalf("Attesa revisione differente dopo aggiunta file")
	}

	// Settings fingerprint
	fp1 := computeSettingsFingerprint("print", "6x9", false)
	fp2 := computeSettingsFingerprint("print", "5x8", false)
	fp3 := computeSettingsFingerprint("print", "6x9", true)
	fpEbook := computeSettingsFingerprint("ebook", "", false)

	if fp1 == fp2 || fp1 == fp3 || fp1 == fpEbook {
		t.Fatalf("Attese impronte impostazioni distinte: fp1=%s fp2=%s fp3=%s fpEbook=%s", fp1, fp2, fp3, fpEbook)
	}
}

func TestPublicationPreviewArtifactRoute(t *testing.T) {
	// ID inesistente -> 404
	req := httptest.NewRequest(http.MethodGet, "/api/publication-preview/non-existent-id/artifact", nil)
	w := httptest.NewRecorder()
	apiPublicationPreviewRoute(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("Atteso 404 per artefatto inesistente, ottenuto %d", w.Code)
	}

	// Azione non valida -> 404
	req2 := httptest.NewRequest(http.MethodGet, "/api/publication-preview/non-existent-id/invalid-action", nil)
	w2 := httptest.NewRecorder()
	apiPublicationPreviewRoute(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("Atteso 404 per azione non valida, ottenuto %d", w2.Code)
	}
}

func TestPublicationPreviewCacheRotation(t *testing.T) {
	vault := "/tmp/test-vault-rotation"
	base := previewCacheBaseDir()

	var createdDirs []string
	for i := 0; i < 7; i++ {
		id := randomHexID(8)
		dir := filepath.Join(base, vaultHashKey(vault), id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		dummyFile := filepath.Join(dir, "book.pdf")
		if err := os.WriteFile(dummyFile, []byte("%PDF-1.4 dummy"), 0600); err != nil {
			t.Fatal(err)
		}
		createdDirs = append(createdDirs, dir)

		art := &PreviewArtifact{
			ID:          id,
			Vault:       vault,
			Target:      "print",
			FilePath:    dummyFile,
			ContentType: "application/pdf",
			Filename:    "book.pdf",
			OutDir:      dir,
		}
		registerPreviewArtifact(art)
	}

	// Devono rimanere max 5 artefatti registrati per questo vault
	previewRegistry.RLock()
	count := len(previewRegistry.vaultItems[vaultHashKey(vault)])
	previewRegistry.RUnlock()

	if count != 5 {
		t.Fatalf("Attesi 5 artefatti recenti registrati, trovati %d", count)
	}

	// I primi 2 (più vecchi) devono essere stati eliminati dal filesystem
	if _, err := os.Stat(createdDirs[0]); !os.IsNotExist(err) {
		t.Fatalf("La directory dell'artefatto più vecchio non è stata rimossa: %s", createdDirs[0])
	}
	if _, err := os.Stat(createdDirs[1]); !os.IsNotExist(err) {
		t.Fatalf("La seconda directory dell'artefatto più vecchio non è stata rimossa: %s", createdDirs[1])
	}

	cleanAllPreviews()
}

func TestPublicationPreviewEbookManifestAndSecurity(t *testing.T) {
	tmpDir := t.TempDir()
	epubFile := filepath.Join(tmpDir, "test.epub")

	// Crea archivio EPUB sintetico
	f, err := os.Create(epubFile)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	// mimetype (non compresso)
	mw, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	mw.Write([]byte("application/epub+zip"))

	// META-INF/container.xml
	cw, _ := zw.Create("META-INF/container.xml")
	cw.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))

	// OEBPS/content.opf
	ow, _ := zw.Create("OEBPS/content.opf")
	ow.Write([]byte(`<?xml version="1.0"?><package version="3.0" xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Ebook Di Test</dc:title><dc:creator>Autore Prova</dc:creator><dc:language>it</dc:language></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="chap001.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`))

	// OEBPS/nav.xhtml
	nw, _ := zw.Create("OEBPS/nav.xhtml")
	nw.Write([]byte(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="chap001.xhtml">Capitolo 1</a></li></ol></nav></body></html>`))

	// OEBPS/chap001.xhtml
	chw, _ := zw.Create("OEBPS/chap001.xhtml")
	chw.Write([]byte(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Capitolo 1</h1><p>Testo contenuto.</p></body></html>`))

	zw.Close()
	f.Close()

	artID := "test-ebook-art"
	art := &PreviewArtifact{
		ID:          artID,
		Vault:       tmpDir,
		Target:      "ebook",
		FilePath:    epubFile,
		ContentType: "application/epub+zip",
		Filename:    "test.epub",
		OutDir:      tmpDir,
	}
	registerPreviewArtifact(art)
	defer cleanAllPreviews()

	// 1. Verifica manifest
	reqMf := httptest.NewRequest(http.MethodGet, "/api/publication-preview/"+artID+"/manifest", nil)
	wMf := httptest.NewRecorder()
	apiPublicationPreviewRoute(wMf, reqMf)
	if wMf.Code != http.StatusOK {
		t.Fatalf("Atteso 200 su manifest, ottenuto %d", wMf.Code)
	}

	var mf EbookManifest
	if err := json.Unmarshal(wMf.Body.Bytes(), &mf); err != nil {
		t.Fatal(err)
	}
	if mf.Title != "Ebook Di Test" || mf.Author != "Autore Prova" {
		t.Fatalf("Metadati manifest errati: %+v", mf)
	}
	if len(mf.Spine) != 1 || mf.Spine[0] != "OEBPS/chap001.xhtml" {
		t.Fatalf("Spine manifest errata: %+v", mf.Spine)
	}
	if len(mf.Nav) != 1 || mf.Nav[0].Title != "Capitolo 1" {
		t.Fatalf("Nav manifest errata: %+v", mf.Nav)
	}

	// 2. Verifica lettura risorsa con CSP rigorosa
	reqRes := httptest.NewRequest(http.MethodGet, "/api/publication-preview/"+artID+"/epub/OEBPS/chap001.xhtml", nil)
	wRes := httptest.NewRecorder()
	apiPublicationPreviewRoute(wRes, reqRes)
	if wRes.Code != http.StatusOK {
		t.Fatalf("Atteso 200 su risorsa chap001.xhtml, ottenuto %d", wRes.Code)
	}
	if !strings.Contains(wRes.Header().Get("Content-Type"), "application/xhtml+xml") {
		t.Fatalf("Content-Type atteso xhtml+xml, ottenuto %s", wRes.Header().Get("Content-Type"))
	}
	csp := wRes.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'none'") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("CSP non rispetta l'isolamento di sicurezza: %s", csp)
	}

	// 3. Verifica blocco path traversal
	reqTrav := httptest.NewRequest(http.MethodGet, "/api/publication-preview/"+artID+"/epub/../../etc/passwd", nil)
	wTrav := httptest.NewRecorder()
	apiPublicationPreviewRoute(wTrav, reqTrav)
	if wTrav.Code != http.StatusForbidden && wTrav.Code != http.StatusNotFound {
		t.Fatalf("Atteso 403 o 404 su path traversal, ottenuto %d", wTrav.Code)
	}
}

func TestApiUploadImage(t *testing.T) {
	tmp := t.TempDir()
	vTarget := filepath.Join(tmp, "mybook")
	if err := os.MkdirAll(vTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	vaultMu.Lock()
	vaultDir = vTarget
	vaultMu.Unlock()
	defer func() {
		vaultMu.Lock()
		vaultDir = ""
		vaultMu.Unlock()
	}()

	// 1. Upload tramite JSON base64
	pngPixel := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	bodyJSON := fmt.Sprintf(`{"name": "Mio Disegno!.png", "data": "data:image/png;base64,%s"}`, pngPixel)
	req := httptest.NewRequest(http.MethodPost, "/api/uploadimage", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	apiUploadImage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Atteso 200 su upload immagine, ottenuto %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	fn, ok := resp["filename"].(string)
	if !ok || !strings.HasPrefix(fn, "mio-disegno") || !strings.HasSuffix(fn, ".png") {
		t.Fatalf("Nome file inatteso: %v", resp["filename"])
	}

	savedFile := filepath.Join(vTarget, "images", fn)
	if !fileExists(savedFile) {
		t.Fatalf("File non salvato su disco in images/: %s", savedFile)
	}

	// 2. Collisione: secondo upload con stesso nome deve aggiungere suffisso -2
	req2 := httptest.NewRequest(http.MethodPost, "/api/uploadimage", strings.NewReader(bodyJSON))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	apiUploadImage(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("Atteso 200 su collisione, ottenuto %d", w2.Code)
	}
	var resp2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	fn2 := resp2["filename"].(string)
	if fn2 != "mio-disegno-2.png" {
		t.Fatalf("Atteso mio-disegno-2.png per collisione, ottenuto: %s", fn2)
	}
}
