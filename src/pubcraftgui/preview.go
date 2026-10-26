package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// PreviewArtifact represents an immutable generated publication preview artifact (PDF or EPUB).
type PreviewArtifact struct {
	ID                  string    `json:"preview_id"`
	RequestID           string    `json:"request_id"`
	Vault               string    `json:"vault"`
	Target              string    `json:"target"` // "print" | "ebook"
	FilePath            string    `json:"-"`
	ContentType         string    `json:"-"`
	Filename            string    `json:"filename"`
	SourceRevision      string    `json:"source_revision"`
	SettingsFingerprint string    `json:"settings_fingerprint"`
	GeneratedAt         time.Time `json:"generated_at"`
	PageCount           int       `json:"page_count,omitempty"`
	Trim                string    `json:"trim,omitempty"`
	Bleed               bool      `json:"bleed"`
	Warnings            []string  `json:"warnings"`
	OutDir              string    `json:"-"`
}

var previewRegistry = struct {
	sync.RWMutex
	items      map[string]*PreviewArtifact
	vaultItems map[string][]string // vaultHash -> list of artifact IDs (ordered oldest to newest)
}{
	items:      make(map[string]*PreviewArtifact),
	vaultItems: make(map[string][]string),
}

var previewInstanceDir = fmt.Sprintf("instance-%d-%d", os.Getpid(), time.Now().UnixNano())

func previewCacheBaseDir() string {
	return filepath.Join(os.TempDir(), "pubcraft-previews", previewInstanceDir)
}

func randomHexID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("prev-%d", time.Now().UnixNano())
	}
	return "prev-" + hex.EncodeToString(b)
}

func vaultHashKey(vDir string) string {
	h := sha256.Sum256([]byte(filepath.Clean(vDir)))
	return hex.EncodeToString(h[:8])
}

// computeSourceRevision calculates a deterministic SHA-256 hash of the vault's
// editorial content (book.yaml, chapters, style.css, assets), excluding build
// directories and transient files.
func computeSourceRevision(vDir string) (string, error) {
	h := sha256.New()
	root := filepath.Clean(vDir)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || (info.IsDir() && (name == "build" || strings.HasPrefix(name, "out-stampa") || strings.HasPrefix(name, "out-"))) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if name == "print.html" || strings.HasSuffix(name, ".pdf") || strings.HasSuffix(name, ".epub") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func computeSettingsFingerprint(target string, trim string, bleed bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "target=%s;trim=%s;bleed=%t\n", target, trim, bleed)
	return hex.EncodeToString(h.Sum(nil))
}

func registerPreviewArtifact(art *PreviewArtifact) {
	previewRegistry.Lock()
	defer previewRegistry.Unlock()

	vHash := vaultHashKey(art.Vault)
	previewRegistry.items[art.ID] = art
	previewRegistry.vaultItems[vHash] = append(previewRegistry.vaultItems[vHash], art.ID)

	// Keep max 5 recent preview artifacts per vault, cleaning up older ones safely
	const maxRecent = 5
	// Tetto globale: evita crescita illimitata su istanze di lunga durata
	// che generano anteprime per molti progetti diversi.
	const maxTotal = 25
	if len(previewRegistry.items) >= maxTotal {
		oldestID := ""
		var oldest time.Time
		for id, a := range previewRegistry.items {
			if oldestID == "" || a.GeneratedAt.Before(oldest) {
				oldestID, oldest = id, a.GeneratedAt
			}
		}
		if oldestID != "" {
			if oldArt := previewRegistry.items[oldestID]; oldArt != nil {
				if oldArt.OutDir != "" && strings.HasPrefix(filepath.Clean(oldArt.OutDir), filepath.Clean(previewCacheBaseDir())) {
					_ = os.RemoveAll(oldArt.OutDir)
				}
				delete(previewRegistry.items, oldestID)
				vHashOld := vaultHashKey(oldArt.Vault)
				kept := previewRegistry.vaultItems[vHashOld][:0]
				for _, x := range previewRegistry.vaultItems[vHashOld] {
					if x != oldestID {
						kept = append(kept, x)
					}
				}
				previewRegistry.vaultItems[vHashOld] = kept
			}
		}
	}
	list := previewRegistry.vaultItems[vHash]
	if len(list) > maxRecent {
		overflow := len(list) - maxRecent
		for i := 0; i < overflow; i++ {
			oldID := list[i]
			if oldArt, exists := previewRegistry.items[oldID]; exists {
				if oldArt.OutDir != "" && strings.HasPrefix(filepath.Clean(oldArt.OutDir), filepath.Clean(previewCacheBaseDir())) {
					_ = os.RemoveAll(oldArt.OutDir)
				}
				delete(previewRegistry.items, oldID)
			}
		}
		previewRegistry.vaultItems[vHash] = list[overflow:]
	}
}

func getPreviewArtifact(id string) *PreviewArtifact {
	previewRegistry.RLock()
	defer previewRegistry.RUnlock()
	return previewRegistry.items[id]
}

// cleanAllPreviews safely purges the ephemeral preview cache directory for this instance.
func cleanAllPreviews() {
	previewRegistry.Lock()
	defer previewRegistry.Unlock()
	previewRegistry.items = make(map[string]*PreviewArtifact)
	previewRegistry.vaultItems = make(map[string][]string)
	_ = os.RemoveAll(previewCacheBaseDir())
}

// cleanOrphanedPreviewDirs purges leftover directories from dead instances while preserving active ones.
func cleanOrphanedPreviewDirs() {
	base := filepath.Join(os.TempDir(), "pubcraft-previews")
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == previewInstanceDir {
			continue
		}
		if !strings.HasPrefix(name, "instance-") {
			continue
		}
		parts := strings.Split(name, "-")
		if len(parts) >= 2 {
			pid, err := strconv.Atoi(parts[1])
			if err == nil && pid > 0 {
				if !processExists(pid) {
					_ = os.RemoveAll(filepath.Join(base, name))
				}
			}
		}
	}
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

type publicationPreviewReq struct {
	Vault     string `json:"vault"`
	Target    string `json:"target"` // "print" | "ebook"
	RequestID string `json:"request_id"`
	Settings  struct {
		Trim  string `json:"trim"`
		Bleed bool   `json:"bleed"`
	} `json:"settings"`
}

var supportedTrims = map[string]bool{
	"5x8":       true,
	"5.25x8":    true,
	"5.5x8.5":   true,
	"6x9":       true,
	"6.14x9.21": true,
	"6.69x9.61": true,
	"7x10":      true,
	"8.5x11":    true,
}

func apiPublicationPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	var req publicationPreviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "Richiesta non valida")
		return
	}

	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, http.StatusBadRequest, "Apri prima una cartella libro")
		return
	}

	headerVault := r.Header.Get("X-PubCraft-Vault")
	effectiveReqVault := req.Vault
	if effectiveReqVault == "" {
		effectiveReqVault = headerVault
	}
	if effectiveReqVault == "" || filepath.Clean(effectiveReqVault) != filepath.Clean(vDir) {
		jsonErr(w, http.StatusConflict, "Il progetto è cambiato: operazione rifiutata. Riapri il libro prima di modificare o generare anteprime.")
		return
	}

	if req.Target != "print" && req.Target != "ebook" {
		jsonErr(w, http.StatusBadRequest, "Target non valido: specificare 'print' o 'ebook'")
		return
	}

	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "Metadati del libro non validi: "+err.Error())
		return
	}
	// UUID stabile prima del calcolo revisione (evita warning spuri di
	// "sorgenti modificate" quando la CLI lo persiste durante la build).
	if err := ensureBookUUID(vDir, b); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Impossibile rendere stabile l'identificatore del libro: "+err.Error())
		return
	}

	if req.Target == "print" {
		trim := req.Settings.Trim
		if trim == "" {
			if b.Trim != "" {
				trim = b.Trim
			} else {
				trim = "6x9"
			}
		}
		if !supportedTrims[trim] {
			jsonErr(w, http.StatusBadRequest, "Formato trim non supportato: "+trim)
			return
		}
	}

	if toolPath == "" {
		jsonErr(w, http.StatusInternalServerError, "pubcraft non trovato")
		return
	}

	// Lock build execution to prevent concurrency collisions
	buildMu.Lock()
	defer buildMu.Unlock()

	sourceRevBefore, err := computeSourceRevision(vDir)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "Errore calcolo revisione sorgenti: "+err.Error())
		return
	}

	artID := randomHexID(8)
	vHash := vaultHashKey(vDir)
	artDir := filepath.Join(previewCacheBaseDir(), vHash, artID)
	if err := os.MkdirAll(artDir, 0700); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Impossibile creare cartella temporanea anteprima: "+err.Error())
		return
	}

	slug := printName(b)
	var art *PreviewArtifact
	var warnings []string

	switch req.Target {
	case "print":
		trim := req.Settings.Trim
		if trim == "" {
			if b.Trim != "" {
				trim = b.Trim
			} else {
				trim = "6x9"
			}
		}
		if !supportedTrims[trim] {
			_ = os.RemoveAll(artDir)
			jsonErr(w, http.StatusBadRequest, "Formato trim non supportato: "+trim)
			return
		}

		initialPages := b.Pages
		if initialPages <= 0 {
			initialPages = 200
		}

		_, pdfPath, _, finalPages, buildErr := renderPrintCore(vDir, b, trim, initialPages, req.Settings.Bleed, artDir)
		if buildErr != nil {
			_ = os.RemoveAll(artDir)
			jsonErr(w, http.StatusInternalServerError, "Generazione PDF anteprima fallita: "+buildErr.Error())
			return
		}

		sourceRevAfter, _ := computeSourceRevision(vDir)
		if sourceRevBefore != sourceRevAfter {
			warnings = append(warnings, "Le sorgenti sono state modificate durante la generazione dell'anteprima.")
		}

		art = &PreviewArtifact{
			ID:                  artID,
			RequestID:           req.RequestID,
			Vault:               vDir,
			Target:              "print",
			FilePath:            pdfPath,
			ContentType:         "application/pdf",
			Filename:            slug + ".pdf",
			SourceRevision:      sourceRevAfter,
			SettingsFingerprint: computeSettingsFingerprint("print", trim, req.Settings.Bleed),
			GeneratedAt:         time.Now(),
			PageCount:           finalPages,
			Trim:                trim,
			Bleed:               req.Settings.Bleed,
			Warnings:            warnings,
			OutDir:              artDir,
		}

	case "ebook":
		epubPath := filepath.Join(artDir, slug+".epub")
		cmd := exec.Command(toolPath, "build", vDir, epubPath, "--kindle")
		out, buildErr := runTool(cmd)
		if buildErr != nil {
			_ = os.RemoveAll(artDir)
			jsonErr(w, http.StatusInternalServerError, "Generazione EPUB anteprima fallita: "+buildErr.Error()+"\n"+out)
			return
		}

		sourceRevAfter, _ := computeSourceRevision(vDir)
		if sourceRevBefore != sourceRevAfter {
			warnings = append(warnings, "Le sorgenti sono state modificate durante la generazione dell'anteprima.")
		}

		art = &PreviewArtifact{
			ID:                  artID,
			RequestID:           req.RequestID,
			Vault:               vDir,
			Target:              "ebook",
			FilePath:            epubPath,
			ContentType:         "application/epub+zip",
			Filename:            slug + ".epub",
			SourceRevision:      sourceRevAfter,
			SettingsFingerprint: computeSettingsFingerprint("ebook", "", false),
			GeneratedAt:         time.Now(),
			Warnings:            warnings,
			OutDir:              artDir,
		}
	}

	registerPreviewArtifact(art)

	resp := map[string]any{
		"preview_id":           art.ID,
		"request_id":           art.RequestID,
		"target":               art.Target,
		"source_revision":      art.SourceRevision,
		"settings_fingerprint": art.SettingsFingerprint,
		"generated_at":         art.GeneratedAt.UTC().Format(time.RFC3339),
		"artifact_url":         "/api/publication-preview/" + art.ID + "/artifact",
		"download_url":         "/api/publication-preview/" + art.ID + "/download",
		"warnings":             art.Warnings,
	}
	if art.PageCount > 0 {
		resp["page_count"] = art.PageCount
	}
	if art.Target == "print" {
		resp["settings"] = map[string]any{"trim": art.Trim, "bleed": art.Bleed}
	}

	writeJSON(w, resp)
}

type EbookNavEntry struct {
	Title string `json:"title"`
	Href  string `json:"href"`
}

type EbookManifest struct {
	Title    string          `json:"title"`
	Author   string          `json:"author"`
	Language string          `json:"language"`
	Spine    []string        `json:"spine"`
	Nav      []EbookNavEntry `json:"nav"`
	BaseURL  string          `json:"base_url"`
}

func extractEbookManifest(epubPath, artID string) (*EbookManifest, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// 1. Leggi META-INF/container.xml per individuare il percorso del pacchetto OPF
	var opfPath string
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err == nil {
				data, _ := io.ReadAll(rc)
				rc.Close()
				re := regexp.MustCompile(`full-path\s*=\s*["']([^"']+)["']`)
				if m := re.FindSubmatch(data); len(m) >= 2 {
					opfPath = string(m[1])
				}
			}
			break
		}
	}
	if opfPath == "" {
		opfPath = "OEBPS/content.opf"
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	manifest := &EbookManifest{
		BaseURL: "/api/publication-preview/" + artID + "/epub/",
		Spine:   []string{},
		Nav:     []EbookNavEntry{},
	}

	// 2. Leggi OPF per metadati, manifest e spine
	var navHref string
	itemMap := make(map[string]string) // id -> href

	for _, f := range zr.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err == nil {
				data, _ := io.ReadAll(rc)
				rc.Close()

				// Metadata
				if m := regexp.MustCompile(`(?s)<dc:title[^>]*>(.*?)</dc:title>`).FindSubmatch(data); len(m) >= 2 {
					manifest.Title = strings.TrimSpace(string(m[1]))
				}
				if m := regexp.MustCompile(`(?s)<dc:creator[^>]*>(.*?)</dc:creator>`).FindSubmatch(data); len(m) >= 2 {
					manifest.Author = strings.TrimSpace(string(m[1]))
				}
				if m := regexp.MustCompile(`(?s)<dc:language[^>]*>(.*?)</dc:language>`).FindSubmatch(data); len(m) >= 2 {
					manifest.Language = strings.TrimSpace(string(m[1]))
				}

				// Manifest items
				itemRe := regexp.MustCompile(`<item\s+[^>]*>`)
				for _, match := range itemRe.FindAll(data, -1) {
					idM := regexp.MustCompile(`id\s*=\s*["']([^"']+)["']`).FindSubmatch(match)
					hrefM := regexp.MustCompile(`href\s*=\s*["']([^"']+)["']`).FindSubmatch(match)
					propM := regexp.MustCompile(`properties\s*=\s*["']([^"']+)["']`).FindSubmatch(match)
					if len(idM) >= 2 && len(hrefM) >= 2 {
						id := string(idM[1])
						href := string(hrefM[1])
						fullHref := href
						if opfDir != "" && !strings.HasPrefix(href, opfDir+"/") {
							fullHref = path.Join(opfDir, href)
						}
						itemMap[id] = fullHref
						if len(propM) >= 2 && strings.Contains(string(propM[1]), "nav") {
							navHref = fullHref
						}
					}
				}

				// Spine
				spineRe := regexp.MustCompile(`(?s)<spine[^>]*>(.*?)</spine>`)
				if sm := spineRe.FindSubmatch(data); len(sm) >= 2 {
					itemrefRe := regexp.MustCompile(`idref\s*=\s*["']([^"']+)["']`)
					for _, ref := range itemrefRe.FindAllSubmatch(sm[1], -1) {
						if len(ref) >= 2 {
							idref := string(ref[1])
							if fullHref, ok := itemMap[idref]; ok {
								manifest.Spine = append(manifest.Spine, fullHref)
							}
						}
					}
				}
			}
			break
		}
	}

	// 3. Leggi indice da nav.xhtml
	if navHref != "" {
		for _, f := range zr.File {
			if f.Name == navHref {
				rc, err := f.Open()
				if err == nil {
					data, _ := io.ReadAll(rc)
					rc.Close()
					navDir := path.Dir(navHref)
					if navDir == "." {
						navDir = ""
					}
					aRe := regexp.MustCompile(`(?s)<a\s+[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
					for _, am := range aRe.FindAllSubmatch(data, -1) {
						if len(am) >= 3 {
							rawHref := string(am[1])
							title := strings.TrimSpace(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(am[2]), ""))
							fullHref := rawHref
							if navDir != "" && !strings.HasPrefix(rawHref, navDir+"/") {
								fullHref = path.Join(navDir, rawHref)
							}
							manifest.Nav = append(manifest.Nav, EbookNavEntry{
								Title: title,
								Href:  fullHref,
							})
						}
					}
				}
				break
			}
		}
	}

	// Fallback se indice nav non presente o vuoto: genera da spine
	if len(manifest.Nav) == 0 {
		for i, sp := range manifest.Spine {
			manifest.Nav = append(manifest.Nav, EbookNavEntry{
				Title: fmt.Sprintf("Capitolo %d", i+1),
				Href:  sp,
			})
		}
	}

	return manifest, nil
}

func serveEbookZipEntry(w http.ResponseWriter, r *http.Request, epubPath, resourcePath string) {
	cleanPath := path.Clean("/" + resourcePath)
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	if strings.Contains(cleanPath, "..") || strings.HasPrefix(cleanPath, "/") {
		http.Error(w, "Accesso negato", http.StatusForbidden)
		return
	}

	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		http.Error(w, "Impossibile aprire il pacchetto EPUB", http.StatusInternalServerError)
		return
	}
	defer zr.Close()

	var targetFile *zip.File
	for _, f := range zr.File {
		if f.Name == cleanPath || strings.HasSuffix(f.Name, "/"+cleanPath) {
			targetFile = f
			break
		}
	}

	if targetFile == nil {
		http.Error(w, "Risorsa non trovata nel pacchetto EPUB", http.StatusNotFound)
		return
	}

	const maxResourceSize = 50 * 1024 * 1024 // 50MB per risorsa
	if targetFile.UncompressedSize64 > maxResourceSize {
		http.Error(w, "Risorsa eccede i limiti dimensionali consentiti", http.StatusRequestEntityTooLarge)
		return
	}

	ext := strings.ToLower(filepath.Ext(targetFile.Name))
	mediaType := "application/octet-stream"
	switch ext {
	case ".xhtml", ".html":
		mediaType = "application/xhtml+xml; charset=utf-8"
	case ".css":
		mediaType = "text/css; charset=utf-8"
	case ".jpg", ".jpeg":
		mediaType = "image/jpeg"
	case ".png":
		mediaType = "image/png"
	case ".gif":
		mediaType = "image/gif"
	case ".svg":
		mediaType = "image/svg+xml"
	case ".woff":
		mediaType = "font/woff"
	case ".woff2":
		mediaType = "font/woff2"
	case ".ttf":
		mediaType = "font/ttf"
	case ".otf":
		mediaType = "font/otf"
	case ".xml", ".opf", ".ncx":
		mediaType = "application/xml; charset=utf-8"
	}

	rc, err := targetFile.Open()
	if err != nil {
		http.Error(w, "Errore lettura risorsa", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxResourceSize))
	if err != nil {
		http.Error(w, "Errore decompressione risorsa", http.StatusInternalServerError)
		return
	}

	// Isolamento di sicurezza rigoroso:
	// - I contenuti non possono eseguire script (script-src 'none')
	// - Non possono inviare dati in rete (connect-src 'none')
	// - Non possono incorporare frame o plugin esterni (frame-src 'none', object-src 'none')
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Security-Policy", "default-src 'self' data: blob:; script-src 'none'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob:; connect-src 'none'; frame-src 'none'; object-src 'none';")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cache-Control", "private, no-cache")

	http.ServeContent(w, r, filepath.Base(targetFile.Name), targetFile.Modified, bytes.NewReader(data))
}

func apiPublicationPreviewRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	// Path format:
	// /api/publication-preview/{id}/artifact
	// /api/publication-preview/{id}/download
	// /api/publication-preview/{id}/manifest
	// /api/publication-preview/{id}/epub/{resource...}
	rel := strings.TrimPrefix(r.URL.Path, "/api/publication-preview/")
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		http.Error(w, "Richiesta non valida", http.StatusBadRequest)
		return
	}

	artID := parts[0]
	action := parts[1]

	art := getPreviewArtifact(artID)
	if art == nil || !fileExists(art.FilePath) {
		http.Error(w, "Artefatto di anteprima non trovato o scaduto. Aggiorna l'anteprima manualmente.", http.StatusNotFound)
		return
	}

	switch action {
	case "artifact":
		w.Header().Set("Content-Type", art.ContentType)
		w.Header().Set("Cache-Control", "private, no-cache")
		http.ServeFile(w, r, art.FilePath)
	case "download":
		w.Header().Set("Content-Type", art.ContentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", art.Filename))
		http.ServeFile(w, r, art.FilePath)
	case "manifest":
		if art.Target == "ebook" {
			mf, err := extractEbookManifest(art.FilePath, art.ID)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, "Impossibile leggere il manifest dell'EPUB: "+err.Error())
				return
			}
			writeJSON(w, mf)
			return
		}
		writeJSON(w, map[string]any{
			"target":     "print",
			"page_count": art.PageCount,
			"download":   "/api/publication-preview/" + art.ID + "/download",
		})
	case "epub":
		subPath := strings.TrimPrefix(rel, artID+"/epub/")
		serveEbookZipEntry(w, r, art.FilePath, subPath)
	default:
		http.Error(w, "Azione non riconosciuta", http.StatusNotFound)
	}
}
