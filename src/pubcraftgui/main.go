// pubcraftgui — GUI locale e server web per PubCraft.
//
// Binario unico: incorpora l'interfaccia web e la serve su 127.0.0.1.
// Nessuna dipendenza esterna (solo libreria standard Go). Il vault resta
// markdown puro: Obsidian, git e `pubcraft build` funzionano perfettamente.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

//go:embed web
var webFS embed.FS

type Chapter struct {
	File  string `json:"file"`
	Title string `json:"title"`
	ID    string `json:"id"`
	Type  string `json:"type,omitempty"`
}

type Book struct {
	Title       string            `json:"title"`
	Author      string            `json:"author"`
	Language    string            `json:"language"`
	UUID        string            `json:"uuid"`
	Cover       string            `json:"cover,omitempty"`
	Style       string            `json:"style"`
	Opening     string            `json:"opening"`
	Font        string            `json:"font"`
	SceneBreak  string            `json:"scenebreak"`
	Trim        string            `json:"trim,omitempty"`
	Pages       int               `json:"pages,omitempty"`
	Bleed       bool              `json:"bleed,omitempty"`
	Chapters    []Chapter         `json:"chapters"`
	ExtraFields map[string]string `json:"extra_fields,omitempty"`
}

type State struct {
	Vault        string `json:"vault"`
	Book         *Book  `json:"book"`
	HomeDir      string `json:"homeDir,omitempty"`
	DocsDir      string `json:"docsDir,omitempty"`
	DesktopDir   string `json:"desktopDir,omitempty"`
	DownloadsDir string `json:"downloadsDir,omitempty"`
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	VaultModTime string `json:"vaultModTime,omitempty"`
	FileCount    int    `json:"fileCount,omitempty"`
}

var (
	vaultMu      sync.RWMutex
	vaultDir     string
	buildMu      sync.Mutex
	toolPath     string // percorso di pubcraft trovato all'avvio
	watchMode    bool
	lastPrintMu  sync.RWMutex
	lastPrintMap = make(map[string]string) // vaultDir -> produced print.html
	watchMu      sync.Mutex
	watchClients = make(map[chan struct{}]struct{})
)

func getVaultDir() string {
	vaultMu.RLock()
	defer vaultMu.RUnlock()
	return vaultDir
}

func getLastPrintHTML(vDir string) string {
	lastPrintMu.RLock()
	defer lastPrintMu.RUnlock()
	if vDir == "" {
		return ""
	}
	return lastPrintMap[filepath.Clean(vDir)]
}

func setLastPrintHTML(vDir, p string) {
	lastPrintMu.Lock()
	defer lastPrintMu.Unlock()
	if vDir != "" {
		lastPrintMap[filepath.Clean(vDir)] = p
	}
}

func setVault(p string) {
	vaultMu.Lock()
	if p == "" {
		vaultDir = ""
		vaultMu.Unlock()
		invalidateVaultFingerprint()
		return
	}
	if abs, err := filepath.Abs(p); err == nil {
		vaultDir = abs
	} else {
		vaultDir = p
	}
	cur := vaultDir
	vaultMu.Unlock()
	invalidateVaultFingerprint()

	if cur != "" {
		go recordRecentProject(cur)
	}
}

func broadcastWatchChange() {
	watchMu.Lock()
	defer watchMu.Unlock()
	for ch := range watchClients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// VaultFingerprint calcola un'impronta FNV-1a a 64 bit basata sui metadati del filesystem
// (percorsi relativi, dimensioni dei file e timestamp di modifica mtime al nanosecondo).
//
// NOTA BENE SULLA DOCUMENTAZIONE DEI LIMITI:
// Il fingerprint composito si basa esclusivamente su metadati del filesystem (percorsi, dimensioni, mtime)
// e non rileva cambiamenti che preservano questi metadati. Non viene introdotto un hashing completo continuo
// dei manoscritti per evitare I/O intensivo continuo su disco e preservare la massima reattività.
type VaultFingerprint struct {
	MaxModTime time.Time
	FileCount  int
	TotalSize  int64
	Hash       string
}

var (
	vaultFingerprintMu sync.RWMutex
	cachedVaultDir     string
	cachedFingerprint  VaultFingerprint
)

func computeVaultFingerprint(dir string) VaultFingerprint {
	var maxT time.Time
	count := 0
	var totalSize int64
	var h uint64 = 14695981039346656037
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "build" || strings.HasPrefix(name, "out-stampa") || name == "notes" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			rel = p
		}
		if info.IsDir() {
			if p == dir {
				return nil
			}
			for i := 0; i < len(rel); i++ {
				h ^= uint64(rel[i])
				h *= 1099511628211
			}
			return nil
		}
		count++
		sz := info.Size()
		totalSize += sz
		mt := info.ModTime()
		if mt.After(maxT) {
			maxT = mt
		}
		for i := 0; i < len(rel); i++ {
			h ^= uint64(rel[i])
			h *= 1099511628211
		}
		h ^= uint64(sz)
		h *= 1099511628211
		h ^= uint64(mt.UnixNano())
		h *= 1099511628211
		return nil
	})
	hexHash := ""
	if count > 0 {
		hexHash = fmt.Sprintf("%016x", h)
	}
	return VaultFingerprint{
		MaxModTime: maxT,
		FileCount:  count,
		TotalSize:  totalSize,
		Hash:       hexHash,
	}
}

func getCachedVaultFingerprint(dir string) VaultFingerprint {
	if dir == "" {
		return VaultFingerprint{}
	}
	vaultFingerprintMu.RLock()
	if cachedVaultDir == dir && cachedFingerprint.Hash != "" {
		fp := cachedFingerprint
		vaultFingerprintMu.RUnlock()
		return fp
	}
	vaultFingerprintMu.RUnlock()

	fp := computeVaultFingerprint(dir)
	vaultFingerprintMu.Lock()
	cachedVaultDir = dir
	cachedFingerprint = fp
	vaultFingerprintMu.Unlock()
	return fp
}

func invalidateVaultFingerprint() {
	vaultFingerprintMu.Lock()
	cachedFingerprint = VaultFingerprint{}
	vaultFingerprintMu.Unlock()
}

func startVaultWatcher() {
	go func() {
		var lastFingerprint VaultFingerprint
		for {
			time.Sleep(400 * time.Millisecond)
			vDir := getVaultDir()
			if vDir == "" {
				continue
			}
			fp := computeVaultFingerprint(vDir)
			vaultFingerprintMu.Lock()
			cachedVaultDir = vDir
			cachedFingerprint = fp
			vaultFingerprintMu.Unlock()

			if lastFingerprint.Hash != "" && fp.Hash != lastFingerprint.Hash {
				broadcastWatchChange()
			}
			lastFingerprint = fp
		}
	}()
}

func getVaultModTime(dir string) (time.Time, int) {
	fp := getCachedVaultFingerprint(dir)
	return fp.MaxModTime, fp.FileCount
}

func apiVaultFingerprint(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		writeJSON(w, map[string]any{"vault": "", "vaultModTime": "", "fileCount": 0})
		return
	}
	fp := getCachedVaultFingerprint(vDir)
	writeJSON(w, map[string]any{
		"vault":        vDir,
		"vaultModTime": fp.Hash,
		"fileCount":    fp.FileCount,
	})
}

func apiWatchEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE non supportato", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	ch := make(chan struct{}, 1)
	watchMu.Lock()
	watchClients[ch] = struct{}{}
	watchMu.Unlock()

	defer func() {
		watchMu.Lock()
		delete(watchClients, ch)
		watchMu.Unlock()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprintf(w, "event: change\ndata: {\"timestamp\": %d}\n\n", time.Now().UnixMilli())
			flusher.Flush()
		}
	}
}

type WatchChapter struct {
	File    string `json:"file"`
	Title   string `json:"title"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type WatchBookResponse struct {
	Vault         string         `json:"vault"`
	Book          *Book          `json:"book"`
	StyleCSS      string         `json:"styleCSS"`
	FontCSS       string         `json:"fontCSS"`
	SceneBreakCSS string         `json:"sceneBreakCSS"`
	Chapters      []WatchChapter `json:"chapters"`
}

func apiWatchBook(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun vault aperto")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 404, "book.yaml non trovato")
		return
	}
	resp := WatchBookResponse{
		Vault: vDir,
		Book:  b,
	}
	if b.Style != "" {
		if css, ok := cssFor(b.Style); ok {
			resp.StyleCSS = css
		}
	} else if css, ok := cssFor("classic"); ok {
		resp.StyleCSS = css
	}
	if b.Font != "" {
		if css, ok := fontCSSFor(b.Font); ok {
			resp.FontCSS = css
		}
	}
	if sb, ok := sceneBreakCSSFor(b.SceneBreak); ok {
		resp.SceneBreakCSS = sb
	}
	for _, c := range b.Chapters {
		body := ""
		if data, err := os.ReadFile(filepath.Join(vDir, c.File)); err == nil {
			body = string(data)
		}
		resp.Chapters = append(resp.Chapters, WatchChapter{
			File:    c.File,
			Title:   c.Title,
			ID:      c.ID,
			Type:    c.Type,
			Content: body,
		})
	}
	writeJSON(w, resp)
}

// ---------------------------------------------------------------- book.yaml

func bookYAMLPath() string {
	vDir := getVaultDir()
	if vDir == "" {
		return ""
	}
	return filepath.Join(vDir, "book.yaml")
}

func cleanYAMLVal(s string) string {
	v := strings.TrimSpace(s)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			if v[0] == '"' {
				if decoded, err := strconv.Unquote(v); err == nil {
					return decoded
				}
			}
			v = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
	}
	return v
}

func isKnownField(k string) bool {
	switch k {
	case "title", "author", "language", "style", "opening", "font", "scenebreak", "trim", "pages", "bleed", "uuid", "cover", "chapters":
		return true
	default:
		return false
	}
}

// parseBookYAML legge il file book.yaml preservando eventuali campi extra.
func parseBookYAML(path string) (*Book, error) {
	if path == "" {
		return nil, errors.New("nessun percorso specificato")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := decodeBookYAML(data)
	if err != nil {
		return nil, fmt.Errorf("metadati non validi in %s: %w", path, err)
	}
	// Titoli mancanti e tipi derivati.
	for i := range b.Chapters {
		if b.Chapters[i].Title == "" {
			b.Chapters[i].Title = strings.TrimSuffix(b.Chapters[i].File, ".md")
		}
		if b.Chapters[i].Type == "" {
			low := strings.ToLower(b.Chapters[i].File + " " + b.Chapters[i].Title)
			switch {
			case strings.Contains(low, "dedica") || strings.Contains(low, "dedication"):
				b.Chapters[i].Type = "dedication"
			case strings.Contains(low, "epigra") || strings.Contains(low, "epigraph"):
				b.Chapters[i].Type = "epigraph"
			case strings.Contains(low, "autore") || strings.Contains(low, "about"):
				b.Chapters[i].Type = "about-author"
			case strings.Contains(low, "ringrazia") || strings.Contains(low, "acknowledg"):
				b.Chapters[i].Type = "acknowledgments"
			case strings.Contains(low, "prefazio") || strings.Contains(low, "preface") || strings.Contains(low, "foreword"):
				b.Chapters[i].Type = "preface"
			default:
				b.Chapters[i].Type = "chapter"
			}
		}
	}
	return b, nil
}

func writeBookYAML(b *Book) error {
	return writeBookYAMLAt(bookYAMLPath(), b)
}

func writeBookYAMLAt(p string, b *Book) error {
	if p == "" {
		return errors.New("nessun vault aperto")
	}
	var header strings.Builder
	existingExtra := make(map[string]string)
	if orig, err := os.ReadFile(p); err == nil {
		if previous, err := decodeBookYAML(orig); err == nil {
			existingExtra = previous.ExtraFields
		}
		for _, line := range strings.Split(string(orig), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "#") {
				header.WriteString(line + "\n")
			} else if t == "chapters:" {
				break
			}
		}
	}
	var s strings.Builder
	s.WriteString(header.String())
	if b.Title != "" {
		fmt.Fprintf(&s, "title: %s\n", yamlScalar(b.Title))
	}
	if b.Author != "" {
		fmt.Fprintf(&s, "author: %s\n", yamlScalar(b.Author))
	}
	if b.Language == "" {
		b.Language = "it"
	}
	fmt.Fprintf(&s, "language: %s\n", b.Language)
	if b.Style != "" {
		fmt.Fprintf(&s, "style: %s\n", b.Style)
	}
	if b.Opening != "" {
		fmt.Fprintf(&s, "opening: %s\n", b.Opening)
	}
	if b.Font != "" {
		fmt.Fprintf(&s, "font: %s\n", b.Font)
	}
	if b.SceneBreak != "" {
		fmt.Fprintf(&s, "scenebreak: %s\n", b.SceneBreak)
	}
	if b.Trim != "" {
		fmt.Fprintf(&s, "trim: %s\n", b.Trim)
	}
	if b.Pages > 0 {
		fmt.Fprintf(&s, "pages: %d\n", b.Pages)
	}
	if b.Bleed {
		fmt.Fprintf(&s, "bleed: true\n")
	}
	if b.UUID != "" {
		fmt.Fprintf(&s, "uuid: %s\n", b.UUID)
	}
	if b.Cover != "" {
		fmt.Fprintf(&s, "cover: %s\n", yamlScalar(b.Cover))
	}
	// Scrivi campi extra unificati e ordinati determinisicamente, senza duplicati
	mergedExtra := make(map[string]string)
	for k, v := range existingExtra {
		if !isKnownField(k) {
			mergedExtra[k] = v
		}
	}
	if b.ExtraFields != nil {
		for k, v := range b.ExtraFields {
			if !isKnownField(k) {
				mergedExtra[k] = v
			}
		}
	}
	var extraKeys []string
	for k := range mergedExtra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		fmt.Fprintf(&s, "%s: %s\n", yamlScalar(k), yamlScalar(mergedExtra[k]))
	}
	s.WriteString("chapters:\n")
	for _, c := range b.Chapters {
		fmt.Fprintf(&s, "- file: %s\n", c.File)
		if c.ID != "" {
			fmt.Fprintf(&s, "  id: %s\n", c.ID)
		}
		fmt.Fprintf(&s, "  title: %s\n", yamlScalar(c.Title))
		if c.Type != "" && c.Type != "chapter" {
			fmt.Fprintf(&s, "  type: %s\n", c.Type)
		}
	}
	return atomicWriteFile(p, []byte(s.String()), 0o644)
}

func cutKV(s string) (string, string, bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(s[:i])
	if k == "" {
		return "", "", false
	}
	return k, cleanYAMLVal(s[i+1:]), true
}

func extractMarkdownTitle(path, fallbackFileName string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		inFrontmatter := false
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "---" {
				inFrontmatter = !inFrontmatter
				continue
			}
			if inFrontmatter {
				if k, v, ok := cutKV(l); ok && k == "title" {
					v = strings.Trim(v, `"'`)
					if v != "" {
						return v
					}
				}
				continue
			}
			if strings.HasPrefix(l, "# ") {
				t := strings.TrimSpace(strings.TrimPrefix(l, "# "))
				if t != "" {
					return t
				}
			}
		}
	}
	base := strings.TrimSuffix(fallbackFileName, filepath.Ext(fallbackFileName))
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.TrimSpace(base)
	if len(base) > 0 {
		base = strings.ToUpper(base[:1]) + base[1:]
	}
	return base
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func naturalLess(s1, s2 string) bool {
	i, j := 0, 0
	for i < len(s1) && j < len(s2) {
		c1, c2 := s1[i], s2[j]
		if isDigit(c1) && isDigit(c2) {
			start1, start2 := i, j
			for i < len(s1) && isDigit(s1[i]) {
				i++
			}
			for j < len(s2) && isDigit(s2[j]) {
				j++
			}
			num1 := s1[start1:i]
			num2 := s2[start2:j]
			trim1 := strings.TrimLeft(num1, "0")
			trim2 := strings.TrimLeft(num2, "0")
			if len(trim1) != len(trim2) {
				return len(trim1) < len(trim2)
			}
			if trim1 != trim2 {
				return trim1 < trim2
			}
			if len(num1) != len(num2) {
				return len(num1) < len(num2)
			}
			continue
		}
		lower1 := unicode.ToLower(rune(c1))
		lower2 := unicode.ToLower(rune(c2))
		if lower1 != lower2 {
			return lower1 < lower2
		}
		i++
		j++
	}
	return len(s1) < len(s2)
}

func sortNatural(list []string) {
	sort.Slice(list, func(i, j int) bool {
		return naturalLess(list[i], list[j])
	})
}

func isSpecialUserDir(dir string) bool {
	dir = filepath.Clean(dir)
	if dir == "/" || dir == "." || dir == ".." || dir == "/tmp" || dir == "/private/tmp" {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		home = filepath.Clean(home)
		if dir == home {
			return true
		}
		if dir == filepath.Join(home, "Desktop") ||
			dir == filepath.Join(home, "Downloads") ||
			dir == filepath.Join(home, "Documents") {
			return true
		}
	}
	return false
}

func autoInitVaultFromMarkdown(dir string) *Book {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var mdFiles []string
	hasOtherThanReadme := false
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".md" || ext == ".markdown" {
			mdFiles = append(mdFiles, e.Name())
			base := strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
			if base != "readme" {
				hasOtherThanReadme = true
			}
		}
	}
	if len(mdFiles) == 0 {
		return nil
	}
	if hasOtherThanReadme {
		filtered := make([]string, 0, len(mdFiles))
		for _, f := range mdFiles {
			base := strings.ToLower(strings.TrimSuffix(f, filepath.Ext(f)))
			if base != "readme" {
				filtered = append(filtered, f)
			}
		}
		mdFiles = filtered
	}
	sortNatural(mdFiles)

	folderName := filepath.Base(dir)
	title := folderName
	if title == "" || title == "." || title == "/" {
		title = "Mio Libro"
	}
	title = strings.ReplaceAll(title, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")
	title = strings.TrimSpace(title)
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}

	bookUUID, _ := newUUID()
	var chapters []Chapter
	for _, fn := range mdFiles {
		chTitle := extractMarkdownTitle(filepath.Join(dir, fn), fn)
		chUUID, _ := newUUID()
		chType := "chapter"
		lower := strings.ToLower(fn)
		if strings.Contains(lower, "dedic") {
			chType = "dedication"
		} else if strings.Contains(lower, "epigr") {
			chType = "epigraph"
		} else if strings.Contains(lower, "pref") || strings.Contains(lower, "prolog") || strings.Contains(lower, "intro") {
			chType = "prologue"
		} else if strings.Contains(lower, "epilog") || strings.Contains(lower, "conclus") {
			chType = "epilogue"
		} else if strings.Contains(lower, "ringraz") || strings.Contains(lower, "acknowledg") {
			chType = "acknowledgments"
		} else if strings.Contains(lower, "autore") || strings.Contains(lower, "author") {
			chType = "about-author"
		}
		chapters = append(chapters, Chapter{
			File:  fn,
			Title: chTitle,
			ID:    chUUID,
			Type:  chType,
		})
	}

	bk := &Book{
		Title:      title,
		Author:     "",
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
		Chapters:   chapters,
	}
	_ = os.MkdirAll(filepath.Join(dir, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	_ = writeBookYAML(bk)
	return bk
}

func loadBook() *Book {
	vDir := getVaultDir()
	if vDir == "" {
		return nil
	}
	p := bookYAMLPath()
	if p == "" {
		return nil
	}
	b, err := parseBookYAML(p)
	if err == nil && b != nil {
		return b
	}
	if auto := autoInitVaultFromMarkdown(vDir); auto != nil {
		return auto
	}
	return nil
}

// ---------------------------------------------------------------------- API

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func apiState(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	st := State{Vault: vDir}
	if home, err := os.UserHomeDir(); err == nil {
		st.HomeDir = home
		docs := filepath.Join(home, "Documents")
		if fileExists(docs) {
			st.DocsDir = docs
		} else {
			st.DocsDir = home
		}
		desktop := filepath.Join(home, "Desktop")
		if fileExists(desktop) {
			st.DesktopDir = desktop
		}
		downloads := filepath.Join(home, "Downloads")
		if fileExists(downloads) {
			st.DownloadsDir = downloads
		}
	}
	cfg := loadUserConfig()
	st.WorkspaceDir = cfg.WorkspaceDir
	if vDir != "" {
		if b := loadBook(); b != nil {
			st.Book = b
		}
		fp := getCachedVaultFingerprint(vDir)
		st.VaultModTime = fp.Hash
		st.FileCount = fp.FileCount
	}
	writeJSON(w, st)
}

// importSelectedFiles inizializza o apre il vault nella cartella dir includendo
// unicamente i file specificati dall'utente (selezione multipla con Cmd/Ctrl).
func importSelectedFiles(dir string, filenames []string) (*Book, error) {
	dir = strings.TrimSpace(dir)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("cartella non trovata: %s", dir)
	}

	var validFiles []string
	seen := map[string]bool{}
	for _, fn := range filenames {
		fn = filepath.Base(strings.TrimSpace(fn))
		if fn == "" || fn == "." || fn == ".." || strings.HasPrefix(fn, ".") {
			continue
		}
		if seen[fn] {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fn))
		if ext == ".md" || ext == ".markdown" {
			fullPath := filepath.Join(dir, fn)
			if fileExists(fullPath) {
				validFiles = append(validFiles, fn)
				seen[fn] = true
			}
		}
	}

	if len(validFiles) == 0 {
		return nil, fmt.Errorf("nessun file valido selezionato in %s", dir)
	}

	sortNatural(validFiles)

	folderName := filepath.Base(dir)
	title := folderName
	if title == "" || title == "." || title == "/" {
		title = "Mio Libro"
	}
	title = strings.ReplaceAll(title, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")
	title = strings.TrimSpace(title)
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}

	var chapters []Chapter
	for _, fn := range validFiles {
		fullPath := filepath.Join(dir, fn)
		chTitle := extractMarkdownTitle(fullPath, fn)
		chUUID, _ := newUUID()
		chType := "chapter"
		lower := strings.ToLower(fn)
		if strings.Contains(lower, "dedic") {
			chType = "dedication"
		} else if strings.Contains(lower, "epigr") {
			chType = "epigraph"
		} else if strings.Contains(lower, "pref") || strings.Contains(lower, "prolog") || strings.Contains(lower, "intro") {
			chType = "prologue"
		} else if strings.Contains(lower, "epilog") || strings.Contains(lower, "conclus") {
			chType = "epilogue"
		} else if strings.Contains(lower, "ringraz") || strings.Contains(lower, "acknowledg") {
			chType = "acknowledgments"
		} else if strings.Contains(lower, "autore") || strings.Contains(lower, "author") {
			chType = "about-author"
		}
		chapters = append(chapters, Chapter{
			File:  fn,
			Title: chTitle,
			ID:    chUUID,
			Type:  chType,
		})
	}

	yamlPath := filepath.Join(dir, "book.yaml")
	existingBook, _ := parseBookYAML(yamlPath)

	var bk *Book
	if existingBook != nil {
		bk = existingBook
		bk.Chapters = chapters
	} else {
		bookUUID, _ := newUUID()
		bk = &Book{
			Title:      title,
			Author:     "",
			Language:   "it",
			Style:      "classico",
			Opening:    "drop-cap",
			SceneBreak: "asterischi",
			UUID:       bookUUID,
			Chapters:   chapters,
		}
	}

	setVault(dir)
	_ = os.MkdirAll(filepath.Join(dir, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	if err := writeBookYAML(bk); err != nil {
		return nil, fmt.Errorf("scrittura book.yaml fallita: %w", err)
	}

	return bk, nil
}

// apiOpenAt apre un vault con percorso esplicito (da file browser web).
// Politica A: se cartella vuota o non valida rifiuta. Se contiene file .md auto-inizializza.
// Se l'utente seleziona un file manoscritto (.docx, .vellum, .md, .odt, .pdf), lo apre/importa.
func apiOpenAt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string   `json:"path"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		jsonErrWithDiag(w, 400, "Percorso mancante", "connection", "CONNECTION_ERROR", "apiOpenAt")
		return
	}
	dirty, dirtyDesc, _ := getAnyClientDirty()
	if dirty {
		jsonErrWithDiag(w, http.StatusConflict, fmt.Sprintf("Impossibile aprire un altro libro: modifiche non salvate in corso (%s)", dirtyDesc), "app", "CLIENT_DIRTY", "apiOpenAt")
		return
	}
	p := strings.TrimSpace(req.Path)
	if len(req.Files) > 0 {
		if len(req.Files) == 1 {
			singleExt := strings.ToLower(filepath.Ext(req.Files[0]))
			if singleExt != ".md" && singleExt != ".markdown" {
				fullSingle := filepath.Join(p, req.Files[0])
				b, err := importAnyManuscript(fullSingle)
				if err != nil {
					jsonErrWithDiag(w, 400, err.Error(), "import", "IMPORT_FAILED", "apiOpenAt")
					return
				}
				writeJSON(w, State{Vault: getVaultDir(), Book: b})
				return
			}
		}
		b, err := importSelectedFiles(p, req.Files)
		if err != nil {
			jsonErrWithDiag(w, 400, err.Error(), "import", "IMPORT_FAILED", "apiOpenAt")
			return
		}
		writeJSON(w, State{Vault: getVaultDir(), Book: b})
		return
	}
	st, err := os.Stat(p)
	if err != nil {
		jsonErrWithDiag(w, 400, "Percorso non trovato: "+p, "connection", "CONNECTION_ERROR", "apiOpenAt")
		return
	}
	if !st.IsDir() {
		b, err := importAnyManuscript(p)
		if err != nil {
			jsonErrWithDiag(w, 400, err.Error(), "import", "IMPORT_FAILED", "apiOpenAt")
			return
		}
		writeJSON(w, State{Vault: getVaultDir(), Book: b})
		return
	}
	yamlPath := filepath.Join(p, "book.yaml")
	book, err := parseBookYAML(yamlPath)
	if err != nil || book == nil {
		if os.IsNotExist(err) {
			if autoBook := autoInitVaultFromMarkdown(p); autoBook != nil {
				_ = writeBookYAML(autoBook)
				book = autoBook
			} else {
				jsonErrWithDiag(w, 400, "Cartella non valida: book.yaml assente in "+p, "connection", "CONNECTION_ERROR", "apiOpenAt")
				return
			}
		} else {
			jsonErrWithDiag(w, 400, "Metadati del libro non validi o malformati in "+yamlPath, "connection", "CONNECTION_ERROR", "apiOpenAt")
			return
		}
	}
	setVault(p)
	writeJSON(w, State{Vault: getVaultDir(), Book: book})
}

// apiNewProject crea un nuovo progetto libro (vault con book.yaml, capitolo iniziale, cover/ e images/)
func apiNewProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentDir string `json:"parentDir"`
		Folder    string `json:"folder"`
		Title     string `json:"title"`
		Author    string `json:"author"`
		Style     string `json:"style"`
		Opening   string `json:"opening"`
		Language  string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Nuovo Libro"
	}
	req.Author = strings.TrimSpace(req.Author)
	req.Folder = strings.TrimSpace(req.Folder)
	if req.Folder == "" {
		req.Folder = slugify(req.Title)
	}
	if req.Folder == "." || req.Folder == ".." || filepath.Base(req.Folder) != req.Folder || strings.ContainsAny(req.Folder, `/\\`) {
		jsonErr(w, http.StatusBadRequest, "Nome cartella non valido")
		return
	}
	req.ParentDir = strings.TrimSpace(req.ParentDir)
	if req.ParentDir == "" {
		home, _ := os.UserHomeDir()
		req.ParentDir = filepath.Join(home, "Documents")
		if !fileExists(req.ParentDir) {
			req.ParentDir = home
		}
	}
	if !fileExists(req.ParentDir) {
		jsonErr(w, 400, "La cartella di destinazione non esiste: "+req.ParentDir)
		return
	}

	targetDir := filepath.Join(req.ParentDir, req.Folder)
	for i := 1; fileExists(targetDir); i++ {
		targetDir = filepath.Join(req.ParentDir, fmt.Sprintf("%s-%d", req.Folder, i+1))
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		jsonErr(w, 500, "Impossibile creare la cartella: "+err.Error())
		return
	}
	_ = os.MkdirAll(filepath.Join(targetDir, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(targetDir, "images"), 0o755)

	if req.Language == "" {
		req.Language = "it"
	}
	if req.Style == "" {
		req.Style = "classic"
	}
	if req.Opening == "" {
		req.Opening = "dropcap"
	}

	chUUID, _ := newUUID()
	bookUUID, _ := newUUID()
	ch1File := "01-capitolo-1.md"
	ch1Title := "Capitolo 1"

	ch1Content := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\nInizia a scrivere qui il tuo libro...\n", chUUID, ch1Title)
	if err := os.WriteFile(filepath.Join(targetDir, ch1File), []byte(ch1Content), 0o644); err != nil {
		jsonErr(w, 500, "Errore creazione primo capitolo: "+err.Error())
		return
	}

	book := &Book{
		Title:    req.Title,
		Author:   req.Author,
		Language: req.Language,
		Style:    req.Style,
		Opening:  req.Opening,
		UUID:     bookUUID,
		Chapters: []Chapter{
			{File: ch1File, ID: chUUID, Title: ch1Title, Type: "chapter"},
		},
	}

	if err := writeBookYAMLAt(filepath.Join(targetDir, "book.yaml"), book); err != nil {
		jsonErr(w, 500, "Errore scrittura book.yaml: "+err.Error())
		return
	}
	setVault(targetDir)
	writeJSON(w, State{Vault: getVaultDir(), Book: book})
}

// apiMkdir crea una sottocartella (per le destinazioni di esportazione).
func apiMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonErr(w, 400, "Nome mancante")
		return
	}
	dir := filepath.Join(strings.TrimSpace(req.Path), filepath.Base(strings.TrimSpace(req.Name)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		jsonErr(w, 500, "Creazione fallita: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": dir})
}

// apiFS: navigazione di cartelle per il file browser della webapp.
// Strumento pensato per uso locale/personale: mostra l'intero disco.
func apiFS(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	home, _ := os.UserHomeDir()
	cfg := loadUserConfig()
	if path == "" || path == "~" {
		if cfg.WorkspaceDir != "" && fileExists(cfg.WorkspaceDir) {
			path = cfg.WorkspaceDir
		} else if home != "" {
			docs := filepath.Join(home, "Documents")
			if fileExists(docs) {
				path = docs
			} else {
				path = home
			}
		} else {
			path = "."
		}
	}
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		jsonErr(w, 400, "Cartella non trovata: "+path)
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	mode := r.URL.Query().Get("mode")
	if mode != "files" {
		mode = "dirs"
	}
	var exts map[string]bool
	if e := strings.TrimSpace(r.URL.Query().Get("exts")); e != "" {
		exts = map[string]bool{}
		for _, x := range strings.Split(e, ",") {
			exts[strings.ToLower(strings.TrimSpace(x))] = true
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		jsonErr(w, 400, "Lettura fallita: "+err.Error())
		return
	}
	type item struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
		Size int64  `json:"size"`
	}
	items := []item{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			items = append(items, item{name, true, 0})
			continue
		}
		if mode == "files" {
			ok := exts == nil || exts[strings.ToLower(filepath.Ext(name))]
			if ok {
				if info, err2 := e.Info(); err2 == nil {
					items = append(items, item{name, false, info.Size()})
				}
			}
		} else {
			// In modalità "dirs", mostriamo comunque i file presenti nella cartella
			// (così l'utente vede i file .md / documenti e sa di essere nella cartella giusta).
			if info, err2 := e.Info(); err2 == nil {
				items = append(items, item{name, false, info.Size()})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return items[i].Name < items[j].Name
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	writeJSON(w, map[string]any{
		"path":       path,
		"home":       home,
		"parent":     parent,
		"workspace":  cfg.WorkspaceDir,
		"quick_dirs": getQuickDirs(cfg.WorkspaceDir),
		"items":      items,
	})
}

// apiImport scompatta un archivio in un vault markdown con `pubcraft export-md`
// e lo apre. La cartella viene creata accanto al file, col nome del file.
// ----------------------------------------------------------- import .docx

// Converte un manoscritto Word (.docx) in un vault markdown nuovo.
// Heading1/Title → nuovo capitolo (file NN-titolo.md), Heading2/3 → "## "
// nel capitolo, corsivo/grassetto dei run → *x* / **x**. Le immagini
// esistono ma pubcraft le gestisce dal vault: qui si importa solo il testo
// (la regola di Atticus: mai portare media dentro il manoscritto).

// Parser docx a token (xml.Decoder): controllo totale su run, hyperlink,
// stili paragrafo — encoding/xml su struct con ",any" è insidioso coi
// namespace di OOXML, qui niente sorprese.

type docxRunData struct {
	text string
	bold bool
	ital bool
	link string // rId del hyperlink che lo contiene ("" = nessuno)
}

type docxParaData struct {
	style string
	runs  []docxRunData
}

// docxParseParas estrae i paragrafi con stili, flag di run e id-link.
func docxParseParas(data []byte) []docxParaData {
	type fw struct {
		bold, ital bool
		link       string
	}
	var paras []docxParaData
	var p *docxParaData
	var run *docxRunData
	// stack di contesto: cosa stiamo attraversando
	inBody, inPPr, inRPr := false, false, false
	linkID := ""
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var text strings.Builder
	flushText := func() {
		if run != nil && text.Len() > 0 {
			run.text += text.String()
			text.Reset()
		}
	}
	local := func(name xml.Name) string { return name.Local }
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch local(t.Name) {
			case "body":
				inBody = true
			case "p":
				if inBody {
					paras = append(paras, docxParaData{})
					p = &paras[len(paras)-1]
				}
			case "pPr":
				if p != nil {
					inPPr = true
				}
			case "pStyle":
				if inPPr && p != nil {
					for _, a := range t.Attr {
						if local(a.Name) == "val" {
							p.style = a.Value
						}
					}
				}
			case "r":
				if p != nil {
					flushText()
					p.runs = append(p.runs, docxRunData{link: linkID})
					run = &p.runs[len(p.runs)-1]
				}
			case "rPr":
				if run != nil {
					inRPr = true
				}
			case "b":
				if inRPr && run != nil {
					run.bold = true
					for _, a := range t.Attr {
						if local(a.Name) == "val" {
							v := strings.ToLower(a.Value)
							if v == "false" || v == "0" || v == "off" {
								run.bold = false
							}
						}
					}
				}
			case "i":
				if inRPr && run != nil {
					run.ital = true
					for _, a := range t.Attr {
						if local(a.Name) == "val" {
							v := strings.ToLower(a.Value)
							if v == "false" || v == "0" || v == "off" {
								run.ital = false
							}
						}
					}
				}
			case "t":
				// il testo arriva come chardata nel caso "default" qui sotto
			case "hyperlink":
				if p != nil {
					for _, a := range t.Attr {
						if local(a.Name) == "id" {
							linkID = a.Value
						}
					}
				}
			}
		case xml.CharData:
			if run != nil {
				text.Write(t)
			}
		case xml.EndElement:
			switch local(t.Name) {
			case "t":
				flushText()
			case "rPr":
				inRPr = false
			case "r":
				flushText()
				run = nil
			case "pPr":
				inPPr = false
			case "hyperlink":
				linkID = ""
			case "p":
				run = nil
				text.Reset()
				p = nil
			case "body":
				inBody = false
			}
		}
	}
	return paras
}

func isExternalURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "mailto:")
}

// docxParaMarkdown: paragrafo → riga markdown (grassetto/corsivo/link).
// Gli spazi di chiusura di Word restano fuori dai delimitatori.
func docxParaMarkdown(p docxParaData, rels map[string]string) string {
	emph := func(t, m string) string {
		lead, trail := "", ""
		for strings.HasPrefix(t, " ") {
			lead += " "
			t = t[1:]
		}
		for strings.HasSuffix(t, " ") {
			trail += " "
			t = t[:len(t)-1]
		}
		if t == "" {
			return lead + trail
		}
		return lead + m + t + m + trail
	}
	markers := func(b, i bool) string {
		switch {
		case b && i:
			return "***"
		case b:
			return "**"
		case i:
			return "*"
		}
		return ""
	}
	var out strings.Builder
	// raggruppa i run consecutivi con lo stesso link
	seg := func(runs []docxRunData) {}
	_ = seg
	i := 0
	for i < len(p.runs) {
		if p.runs[i].link != "" {
			target := rels[p.runs[i].link]
			var ltxt strings.Builder
			j := i
			for j < len(p.runs) && p.runs[j].link == p.runs[i].link {
				m := markers(p.runs[j].bold, p.runs[j].ital)
				t := p.runs[j].text
				if m != "" {
					ltxt.WriteString(emph(t, m))
				} else {
					ltxt.WriteString(t)
				}
				j++
			}
			if isExternalURL(target) {
				out.WriteString("[" + strings.TrimSpace(ltxt.String()) + "](" + target + ")")
			} else {
				out.WriteString(ltxt.String())
			}
			i = j
			continue
		}
		m := markers(p.runs[i].bold, p.runs[i].ital)
		if m != "" {
			out.WriteString(emph(p.runs[i].text, m))
		} else {
			out.WriteString(p.runs[i].text)
		}
		i++
	}
	return out.String()
}

func mdEscapeLine(s string) string {
	s = strings.TrimSpace(s)
	// non far scambiare la riga per markdown
	if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, ">") ||
		strings.HasPrefix(s, "!") || strings.HasPrefix(s, "|") {
		return "\\" + s
	}
	return s
}

func isHeading1Style(s string) bool {
	s = strings.ToLower(s)
	return s == "heading1" || s == "titolo1" || s == "title" || s == "titolo"
}

func isHeading2Style(s string) bool {
	s = strings.ToLower(s)
	return s == "heading2" || s == "titolo2"
}

func docxReadZip(r *zip.Reader, name string) ([]byte, error) {
	for _, f := range r.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s non trovato nel docx", name)
}

// importDocxVault converte src (.docx) in un vault nuovo e ne restituisce il Book.
// Heading1/Title → nuovo capitolo, Heading2/3 → "## " nel capitolo.
// Le immagini non vengono portate dentro (media separati dal manoscritto).
func importDocxVault(src string) (*Book, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("non è un .docx valido (zip): %w", err)
	}
	defer zr.Close()
	zrReader := &zr.Reader
	docData, err := docxReadZip(zrReader, "word/document.xml")
	if err != nil {
		return nil, err
	}
	paras := docxParseParas(docData)
	// metadati (se ci sono)
	title, author := "", ""
	if core, err2 := docxReadZip(zrReader, "docProps/core.xml"); err2 == nil {
		type coreProps struct {
			Title   string `xml:"title"`
			Creator string `xml:"creator"`
		}
		var cp coreProps
		if xml.Unmarshal(core, &cp) == nil {
			title, author = strings.TrimSpace(cp.Title), strings.TrimSpace(cp.Creator)
		}
	}
	// rels dei link: rId → target
	rels := map[string]string{}
	if rd, err2 := docxReadZip(zrReader, "word/_rels/document.xml.rels"); err2 == nil {
		type rel struct {
			Id     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		}
		type relsDoc struct {
			R []rel `xml:"Relationship"`
		}
		var rd2 relsDoc
		if xml.Unmarshal(rd, &rd2) == nil {
			for _, x := range rd2.R {
				rels[x.Id] = x.Target
			}
		}
	}

	// impacchetta in capitoli
	type ch struct {
		title string
		lines []string
	}
	chapters := []ch{}
	cur := (*ch)(nil)
	addLine := func(s string) {
		if s == "" {
			return
		}
		if cur == nil {
			chapters = append(chapters, ch{title: "Manoscritto"})
			cur = &chapters[len(chapters)-1]
		}
		cur.lines = append(cur.lines, s)
	}
	for _, p := range paras {
		style := strings.ToLower(p.style)
		line := mdEscapeLine(docxParaMarkdown(p, rels))
		switch {
		case isHeading1Style(style):
			if line != "" {
				chapters = append(chapters, ch{title: strings.TrimPrefix(line, "\\")})
				cur = &chapters[len(chapters)-1]
			}
		case isHeading2Style(style):
			if line != "" {
				addLine("## " + strings.TrimPrefix(line, "\\"))
			}
		default:
			addLine(line)
		}
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("il docx non contiene paragrafi leggibili")
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}

	// cartella vault accanto al docx
	name := slugify(strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
	if name == "" || name == "capitolo" {
		name = "libro"
	}
	dest := filepath.Join(filepath.Dir(src), name+"-md")
	for i := 2; fileExists(filepath.Join(dest, "book.yaml")); i++ {
		dest = filepath.Join(filepath.Dir(src), fmt.Sprintf("%s-md-%d", name, i))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(dest, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "images"), 0o755)
	bookUUID, _ := newUUID()
	book := &Book{
		Title:      title,
		Author:     author,
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
	}
	for i, c := range chapters {
		t := c.title
		if t == "" {
			t = fmt.Sprintf("Capitolo %d", i+1)
		}
		file := fmt.Sprintf("%02d-%s.md", i+1, slugify(t))
		id, _ := newUUID()
		body := ""
		if len(c.lines) > 0 {
			body = strings.Join(c.lines, "\n\n") + "\n"
		}
		md := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", id, t, body)
		if err := os.WriteFile(filepath.Join(dest, file), []byte(md), 0o644); err != nil {
			return nil, err
		}
		book.Chapters = append(book.Chapters, Chapter{File: file, ID: id, Title: t})
	}
	setVault(dest)
	if err := writeBookYAML(book); err != nil {
		return nil, err
	}
	return loadBook(), nil
}

func importVellumVault(src string) (*Book, error) {
	if toolPath == "" {
		return nil, fmt.Errorf("pubcraft non trovato. Compila con 'make' o metti pubcraft nella stessa cartella di pubcraftgui.")
	}
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	name := slugify(base)
	if name == "" || name == "capitolo" {
		name = "libro"
	}
	dest := filepath.Join(filepath.Dir(src), name+"-md")
	for i := 2; fileExists(filepath.Join(dest, "book.yaml")); i++ {
		dest = filepath.Join(filepath.Dir(src), fmt.Sprintf("%s-md-%d", name, i))
	}
	cmd := exec.Command(toolPath, "export-md", src, dest)
	out, err := runTool(cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, out)
	}
	setVault(dest)
	b := loadBook()
	if b == nil {
		return nil, fmt.Errorf("import completato ma il vault non contiene un book.yaml valido")
	}
	return b, nil
}

func importMarkdownVault(src string) (*Book, error) {
	parentDir := filepath.Dir(src)
	if fileExists(filepath.Join(parentDir, "book.yaml")) {
		b, err := parseBookYAML(filepath.Join(parentDir, "book.yaml"))
		if err == nil && b != nil {
			setVault(parentDir)
			return b, nil
		}
	}

	// Se la cartella del file contiene più capitoli markdown (o è la cartella del libro),
	// apri l'intera cartella con tutti i capitoli anziché isolare il singolo file selezionato.
	if !isSpecialUserDir(parentDir) {
		if auto := autoInitVaultFromMarkdown(parentDir); auto != nil && len(auto.Chapters) > 1 {
			_ = writeBookYAML(auto)
			setVault(parentDir)
			return auto, nil
		}
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("lettura file markdown fallita: %w", err)
	}

	raw := string(data)
	lines := strings.Split(raw, "\n")
	var frontmatterTitle, frontmatterAuthor string

	bodyStartLine := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		inFM := true
		for idx := 1; idx < len(lines); idx++ {
			line := strings.TrimSpace(lines[idx])
			if line == "---" {
				inFM = false
				bodyStartLine = idx + 1
				break
			}
			if k, v, ok := cutKV(line); ok {
				v = strings.Trim(v, `"'`)
				if k == "title" && frontmatterTitle == "" {
					frontmatterTitle = v
				} else if k == "author" && frontmatterAuthor == "" {
					frontmatterAuthor = v
				}
			}
		}
		if inFM {
			bodyStartLine = 0
		}
	}

	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	title := frontmatterTitle
	if title == "" {
		title = strings.ReplaceAll(base, "-", " ")
		title = strings.ReplaceAll(title, "_", " ")
		title = strings.TrimSpace(title)
		if len(title) > 0 {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
	}

	name := slugify(base)
	if name == "" {
		name = "libro"
	}
	dest := filepath.Join(filepath.Dir(src), name+"-md")
	for i := 2; fileExists(filepath.Join(dest, "book.yaml")); i++ {
		dest = filepath.Join(filepath.Dir(src), fmt.Sprintf("%s-md-%d", name, i))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(dest, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "images"), 0o755)

	type chData struct {
		title string
		lines []string
	}
	var chapters []chData
	currentCh := chData{title: "", lines: []string{}}

	hasNonEmptyLines := func(ls []string) bool {
		for _, l := range ls {
			if strings.TrimSpace(l) != "" {
				return true
			}
		}
		return false
	}

	for i := bodyStartLine; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			chTitle := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			if hasNonEmptyLines(currentCh.lines) || currentCh.title != "" {
				chapters = append(chapters, currentCh)
			}
			currentCh = chData{title: chTitle, lines: []string{}}
			continue
		}
		if currentCh.title == "" && len(currentCh.lines) == 0 && trimmed == "" {
			continue
		}
		currentCh.lines = append(currentCh.lines, line)
	}
	if hasNonEmptyLines(currentCh.lines) || currentCh.title != "" {
		chapters = append(chapters, currentCh)
	}
	if len(chapters) == 0 {
		chapters = append(chapters, chData{title: title, lines: lines[bodyStartLine:]})
	}

	bookUUID, _ := newUUID()
	book := &Book{
		Title:      title,
		Author:     frontmatterAuthor,
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
	}

	for i, c := range chapters {
		t := c.title
		if t == "" {
			t = fmt.Sprintf("Capitolo %d", i+1)
		}
		file := fmt.Sprintf("%02d-%s.md", i+1, slugify(t))
		id, _ := newUUID()
		body := strings.Join(c.lines, "\n")
		md := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", id, t, strings.TrimSpace(body))
		if err := os.WriteFile(filepath.Join(dest, file), []byte(md), 0o644); err != nil {
			return nil, err
		}
		book.Chapters = append(book.Chapters, Chapter{File: file, ID: id, Title: t, Type: "chapter"})
	}

	setVault(dest)
	if err := writeBookYAML(book); err != nil {
		return nil, err
	}
	return loadBook(), nil
}

func importOdtVault(src string) (*Book, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("non è un file .odt valido (zip): %w", err)
	}
	defer zr.Close()

	var title, author string
	if mf, err := zr.Open("meta.xml"); err == nil {
		dec := xml.NewDecoder(mf)
		var curElement string
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				curElement = t.Name.Local
			case xml.CharData:
				txt := strings.TrimSpace(string(t))
				if txt != "" {
					if curElement == "title" && title == "" {
						title = txt
					} else if curElement == "creator" && author == "" {
						author = txt
					}
				}
			case xml.EndElement:
				curElement = ""
			}
		}
		_ = mf.Close()
	}

	cf, err := zr.Open("content.xml")
	if err != nil {
		return nil, fmt.Errorf("content.xml non trovato nel file .odt: %w", err)
	}
	defer cf.Close()

	type chData struct {
		title string
		paras []string
	}
	var chapters []chData
	currentCh := chData{title: "", paras: []string{}}

	dec := xml.NewDecoder(cf)
	var inHeading, inPara bool
	var currentText strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "h" {
				inHeading = true
				currentText.Reset()
			} else if t.Name.Local == "p" {
				inPara = true
				currentText.Reset()
			}
		case xml.CharData:
			if inHeading || inPara {
				currentText.WriteString(string(t))
			}
		case xml.EndElement:
			if t.Name.Local == "h" && inHeading {
				inHeading = false
				hText := strings.TrimSpace(currentText.String())
				if hText != "" {
					if len(currentCh.paras) > 0 || currentCh.title != "" {
						chapters = append(chapters, currentCh)
					}
					currentCh = chData{title: hText, paras: []string{}}
				}
				currentText.Reset()
			} else if t.Name.Local == "p" && inPara {
				inPara = false
				pText := strings.TrimSpace(currentText.String())
				if pText != "" {
					currentCh.paras = append(currentCh.paras, pText)
				}
				currentText.Reset()
			}
		}
	}
	if len(currentCh.paras) > 0 || currentCh.title != "" {
		chapters = append(chapters, currentCh)
	}

	if len(chapters) == 0 {
		return nil, fmt.Errorf("il file .odt non contiene capitoli o paragrafi leggibili")
	}

	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if title == "" {
		title = strings.ReplaceAll(base, "-", " ")
		title = strings.ReplaceAll(title, "_", " ")
		title = strings.TrimSpace(title)
		if len(title) > 0 {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
	}
	name := slugify(base)
	if name == "" {
		name = "libro"
	}
	dest := filepath.Join(filepath.Dir(src), name+"-md")
	for i := 2; fileExists(filepath.Join(dest, "book.yaml")); i++ {
		dest = filepath.Join(filepath.Dir(src), fmt.Sprintf("%s-md-%d", name, i))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(dest, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "images"), 0o755)

	bookUUID, _ := newUUID()
	book := &Book{
		Title:      title,
		Author:     author,
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
	}

	for i, c := range chapters {
		t := c.title
		if t == "" {
			t = fmt.Sprintf("Capitolo %d", i+1)
		}
		file := fmt.Sprintf("%02d-%s.md", i+1, slugify(t))
		id, _ := newUUID()
		body := strings.Join(c.paras, "\n\n") + "\n"
		md := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", id, t, body)
		if err := os.WriteFile(filepath.Join(dest, file), []byte(md), 0o644); err != nil {
			return nil, err
		}
		book.Chapters = append(book.Chapters, Chapter{File: file, ID: id, Title: t, Type: "chapter"})
	}

	setVault(dest)
	if err := writeBookYAML(book); err != nil {
		return nil, err
	}
	return loadBook(), nil
}

func extractSimplePDFText(data []byte) string {
	var buf strings.Builder
	btIdx := 0
	for {
		bt := bytes.Index(data[btIdx:], []byte("BT"))
		if bt < 0 {
			break
		}
		start := btIdx + bt + 2
		et := bytes.Index(data[start:], []byte("ET"))
		if et < 0 {
			break
		}
		chunk := data[start : start+et]
		btIdx = start + et + 2

		inParen := false
		var curStr []byte
		for i := 0; i < len(chunk); i++ {
			c := chunk[i]
			if c == '(' && (i == 0 || chunk[i-1] != '\\') {
				inParen = true
				curStr = nil
			} else if c == ')' && inParen && (i == 0 || chunk[i-1] != '\\') {
				inParen = false
				if len(curStr) > 0 {
					buf.Write(curStr)
					buf.WriteByte(' ')
				}
			} else if inParen {
				curStr = append(curStr, c)
			}
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}

func importPdfVault(src string) (*Book, error) {
	var rawText string
	pdftotextPath, err := exec.LookPath("pdftotext")
	if err != nil {
		for _, candidate := range []string{"/usr/local/bin/pdftotext", "/opt/homebrew/bin/pdftotext"} {
			if fileExists(candidate) {
				pdftotextPath = candidate
				break
			}
		}
	}
	if pdftotextPath != "" {
		cmd := exec.Command(pdftotextPath, "-layout", src, "-")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			rawText = string(out)
		}
	}
	if strings.TrimSpace(rawText) == "" {
		data, rErr := os.ReadFile(src)
		if rErr != nil {
			return nil, rErr
		}
		extracted := extractSimplePDFText(data)
		if strings.TrimSpace(extracted) == "" {
			return nil, fmt.Errorf("impossibile estrarre testo dal PDF (nessun testo vettoriale o pdftotext assente)")
		}
		rawText = extracted
	}

	pages := strings.Split(rawText, "\x0c")
	type chData struct {
		title string
		paras []string
	}
	var chapters []chData
	currentCh := chData{title: "", paras: []string{}}

	chapterHeadingRe := regexp.MustCompile(`(?i)^(?:#+\s+|capitolo\s+\d+|chapter\s+\d+)`)

	for _, page := range pages {
		pLines := strings.Split(page, "\n")
		var curPara []string
		for _, line := range pLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				if len(curPara) > 0 {
					pText := strings.Join(curPara, " ")
					if chapterHeadingRe.MatchString(pText) && len(pText) < 80 {
						if len(currentCh.paras) > 0 || currentCh.title != "" {
							chapters = append(chapters, currentCh)
						}
						currentCh = chData{title: strings.TrimPrefix(pText, "# "), paras: []string{}}
					} else {
						currentCh.paras = append(currentCh.paras, pText)
					}
					curPara = nil
				}
				continue
			}
			curPara = append(curPara, trimmed)
		}
		if len(curPara) > 0 {
			pText := strings.Join(curPara, " ")
			if chapterHeadingRe.MatchString(pText) && len(pText) < 80 {
				if len(currentCh.paras) > 0 || currentCh.title != "" {
					chapters = append(chapters, currentCh)
				}
				currentCh = chData{title: strings.TrimPrefix(pText, "# "), paras: []string{}}
			} else {
				currentCh.paras = append(currentCh.paras, pText)
			}
		}
	}
	if len(currentCh.paras) > 0 || currentCh.title != "" {
		chapters = append(chapters, currentCh)
	}

	if len(chapters) == 0 {
		return nil, fmt.Errorf("nessun capitolo o paragrafo estratto dal PDF")
	}

	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	title := strings.ReplaceAll(base, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")
	title = strings.TrimSpace(title)
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	name := slugify(base)
	if name == "" {
		name = "libro"
	}
	dest := filepath.Join(filepath.Dir(src), name+"-md")
	for i := 2; fileExists(filepath.Join(dest, "book.yaml")); i++ {
		dest = filepath.Join(filepath.Dir(src), fmt.Sprintf("%s-md-%d", name, i))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(dest, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "images"), 0o755)

	bookUUID, _ := newUUID()
	book := &Book{
		Title:      title,
		Author:     "",
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
	}

	for i, c := range chapters {
		t := c.title
		if t == "" {
			t = fmt.Sprintf("Capitolo %d", i+1)
		}
		file := fmt.Sprintf("%02d-%s.md", i+1, slugify(t))
		id, _ := newUUID()
		body := strings.Join(c.paras, "\n\n") + "\n"
		md := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", id, t, body)
		if err := os.WriteFile(filepath.Join(dest, file), []byte(md), 0o644); err != nil {
			return nil, err
		}
		book.Chapters = append(book.Chapters, Chapter{File: file, ID: id, Title: t, Type: "chapter"})
	}

	setVault(dest)
	if err := writeBookYAML(book); err != nil {
		return nil, err
	}
	return loadBook(), nil
}

func importAnyManuscript(src string) (*Book, error) {
	src = strings.TrimSpace(src)
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("file non trovato: %s", src)
	}
	if st.IsDir() {
		yamlPath := filepath.Join(src, "book.yaml")
		if fileExists(yamlPath) {
			b, err := parseBookYAML(yamlPath)
			if err == nil && b != nil {
				setVault(src)
				return b, nil
			}
		}
		if auto := autoInitVaultFromMarkdown(src); auto != nil {
			_ = writeBookYAML(auto)
			setVault(src)
			return auto, nil
		}
		if entries, rErr := os.ReadDir(src); rErr == nil {
			var manus []string
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				le := strings.ToLower(filepath.Ext(e.Name()))
				if le == ".docx" || le == ".odt" || le == ".pdf" || le == ".vellum" {
					manus = append(manus, filepath.Join(src, e.Name()))
				}
			}
			if len(manus) == 1 {
				return importAnyManuscript(manus[0])
			}
		}
		return nil, fmt.Errorf("cartella non valida: nessun manoscritto o book.yaml trovato in %s", src)
	}

	ext := strings.ToLower(filepath.Ext(src))
	switch ext {
	case ".docx":
		return importDocxVault(src)
	case ".vellum":
		return importVellumVault(src)
	case ".md", ".markdown":
		return importMarkdownVault(src)
	case ".odt":
		return importOdtVault(src)
	case ".pdf":
		return importPdfVault(src)
	default:
		return nil, fmt.Errorf("formato '%s' non supportato. Formati supportati: .docx, .vellum, .md, .odt, .pdf", ext)
	}
}

func apiImportManuscript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File  string   `json:"file"`
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrWithDiag(w, 400, "Richiesta non valida", "import", "INVALID_REQUEST", "apiImportManuscript")
		return
	}
	req.File = strings.TrimSpace(req.File)
	req.Dir = strings.TrimSpace(req.Dir)

	if len(req.Files) > 0 && req.Dir != "" {
		if len(req.Files) == 1 {
			singleExt := strings.ToLower(filepath.Ext(req.Files[0]))
			if singleExt != ".md" && singleExt != ".markdown" {
				fullSingle := filepath.Join(req.Dir, req.Files[0])
				b, err := importAnyManuscript(fullSingle)
				if err != nil {
					jsonErrWithDiag(w, 500, err.Error(), "import", "IMPORT_FAILED", "apiImportManuscript")
					return
				}
				writeJSON(w, State{Vault: getVaultDir(), Book: b})
				return
			}
		}
		b, err := importSelectedFiles(req.Dir, req.Files)
		if err != nil {
			jsonErrWithDiag(w, 500, err.Error(), "import", "IMPORT_FAILED", "apiImportManuscript")
			return
		}
		writeJSON(w, State{Vault: getVaultDir(), Book: b})
		return
	}

	target := req.File
	if target == "" {
		target = req.Dir
	}
	if target == "" {
		jsonErrWithDiag(w, 400, "File o cartella mancante", "import", "MISSING_PATH", "apiImportManuscript")
		return
	}
	b, err := importAnyManuscript(target)
	if err != nil {
		jsonErrWithDiag(w, 500, err.Error(), "import", "IMPORT_FAILED", "apiImportManuscript")
		return
	}
	writeJSON(w, State{Vault: getVaultDir(), Book: b})
}

func apiImportDocx(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.File) == "" {
		jsonErr(w, 400, "File .docx mancante")
		return
	}
	src := strings.TrimSpace(req.File)
	if _, err := os.Stat(src); err != nil {
		jsonErr(w, 400, "File non trovato: "+src)
		return
	}
	b, err := importAnyManuscript(src)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, State{Vault: getVaultDir(), Book: b})
}

func apiImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.File) == "" {
		jsonErr(w, 400, "File sorgente mancante")
		return
	}
	src := strings.TrimSpace(req.File)
	if _, err := os.Stat(src); err != nil {
		jsonErr(w, 400, "File non trovato: "+src)
		return
	}
	b, err := importAnyManuscript(src)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, State{Vault: getVaultDir(), Book: b})
}

func loadBookOrNull() *Book {
	if getVaultDir() == "" {
		return nil
	}
	return loadBook()
}

func safeChapter(w http.ResponseWriter, r *http.Request) string {
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("file")))
	if name == "" || name == "." || !strings.HasSuffix(name, ".md") {
		return ""
	}
	return name
}

func apiChapter(w http.ResponseWriter, r *http.Request) {
	name := safeChapter(w, r)
	if name == "" {
		jsonErr(w, 400, "Capitolo non valido")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun vault aperto")
		return
	}
	data, err := os.ReadFile(filepath.Join(vDir, name))
	if err != nil {
		jsonErr(w, 404, "Non riesco a leggere "+name)
		return
	}
	writeJSON(w, map[string]string{"file": name, "content": string(data), "version": chapterVersion(data), "vault": vDir})
}

var saveMu sync.Mutex

func chapterVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func apiSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File        string `json:"file"`
		Content     string `json:"content"`
		BaseVersion string `json:"base_version"`
		Vault       string `json:"vault"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrWithDiag(w, 400, "Richiesta non valida", "save", "INVALID_REQUEST", "apiSave")
		return
	}
	name := filepath.Base(strings.TrimSpace(req.File))
	if name == "" || name == "." || !strings.HasSuffix(name, ".md") {
		jsonErrWithDiag(w, 400, "Capitolo non valido", "save", "INVALID_CHAPTER", "apiSave")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErrWithDiag(w, 400, "Nessun vault aperto", "save", "NO_VAULT", "apiSave")
		return
	}
	if req.Vault == "" || filepath.Clean(req.Vault) != filepath.Clean(vDir) {
		jsonErrWithDiag(w, 409, "Vault disallineato: richiesta per altro progetto rifiutata", "save", "BOOK_CHANGED", "apiSave")
		return
	}

	fullPath := filepath.Join(vDir, name)
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		jsonErrWithDiag(w, 404, "Il capitolo non esiste: "+name, "save", "CHAPTER_NOT_FOUND", "apiSave")
		return
	}

	saveMu.Lock()
	defer saveMu.Unlock()

	if req.BaseVersion == "" {
		jsonErrWithDiag(w, http.StatusPreconditionRequired, "Versione di partenza mancante: ricarica il capitolo", "save", "VERSION_MISSING", "apiSave")
		return
	}
	current, err := os.ReadFile(fullPath)
	if err != nil {
		jsonErrWithDiag(w, 500, "Lettura prima del salvataggio fallita: "+err.Error(), "save", "READ_FAILED", "apiSave")
		return
	}
	if chapterVersion(current) != req.BaseVersion {
		jsonErrWithDiag(w, 409, "Il file è stato modificato da un altro editor. Conserva il testo e ricarica/confronta il capitolo prima di salvare.", "save", "VERSION_CONFLICT", "apiSave")
		return
	}
	if err := atomicWriteFile(fullPath, []byte(req.Content), st.Mode().Perm()); err != nil {
		jsonErrWithDiag(w, 500, "Scrittura atomica fallita: "+err.Error(), "save", "WRITE_FAILED", "apiSave")
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()
	writeJSON(w, map[string]interface{}{"ok": true, "version": chapterVersion([]byte(req.Content))})
}

var reservedFiles = map[string]bool{"book.md": true}

func apiNewChapter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title    string `json:"title"`
		Type     string `json:"type"`
		Position string `json:"position"` // "start" | "end" | "after-current"
		After    string `json:"after"`    // capitolo attivo dopo cui inserire
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		jsonErr(w, 400, "Serve un titolo")
		return
	}
	title := strings.TrimSpace(req.Title)
	chType := strings.TrimSpace(req.Type)
	if chType == "" {
		chType = "chapter"
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun vault aperto")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Apri prima una cartella libro (con book.yaml)")
		return
	}
	slug := slugify(title)
	if slug == "" {
		slug = "sezione"
	}
	var file string
	var md string
	id, _ := newUUID()

	switch chType {
	case "dedication":
		file = "00-dedica.md"
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("00-dedica-%d.md", i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: dedication\n---\n\n*A chi crede nella forza delle parole.*\n", id, title)
	case "epigraph":
		file = "00-epigrafe.md"
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("00-epigrafe-%d.md", i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: epigraph\n---\n\n> Non c'è vento favorevole per il marinaio che non sa dove andare.\n> — Lucio Anneo Seneca\n", id, title)
	case "about-author":
		file = "99-sull-autore.md"
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("99-sull-autore-%d.md", i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: about-author\n---\n\n**Nome Autore** è scrittore e divulgatore.\n\nVive e lavora tra la scrittura e la ricerca.\n", id, title)
	case "acknowledgments":
		file = "98-ringraziamenti.md"
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("98-ringraziamenti-%d.md", i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: acknowledgments\n---\n\nDesidero ringraziare di cuore tutti coloro che hanno reso possibile la nascita di questo libro.\n", id, title)
	case "preface":
		file = "00-prefazione.md"
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("00-prefazione-%d.md", i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: preface\n---\n\nQuesto testo nasce dall'esigenza di esplorare...\n", id, title)
	default:
		// Capitolo ordinario: prossimo numero 01, 02, …
		max := 0
		for _, c := range b.Chapters {
			var n int
			if _, err := fmt.Sscanf(c.File, "%d", &n); err == nil && n > max && n < 90 {
				max = n
			}
		}
		file = fmt.Sprintf("%02d-%s.md", max+1, slug)
		for i := 0; fileExists(filepath.Join(vDir, file)); i++ {
			file = fmt.Sprintf("%02d-%s-%d.md", max+1, slug, i+2)
		}
		md = fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n", id, title)
	}

	if err := os.WriteFile(filepath.Join(vDir, file), []byte(md), 0o644); err != nil {
		jsonErr(w, 500, "Creazione fallita: "+err.Error())
		return
	}
	newCh := Chapter{File: file, ID: id, Title: title, Type: chType}

	pos := req.Position
	if pos == "" {
		if chType == "dedication" || chType == "epigraph" || chType == "preface" {
			pos = "start"
		} else if chType == "about-author" || chType == "acknowledgments" {
			pos = "end"
		} else if req.After != "" {
			pos = "after-current"
		} else {
			pos = "end"
		}
	}

	switch pos {
	case "start":
		b.Chapters = append([]Chapter{newCh}, b.Chapters...)
	case "after-current":
		idx := -1
		if req.After != "" {
			for i, c := range b.Chapters {
				if c.File == req.After {
					idx = i
					break
				}
			}
		}
		if idx >= 0 {
			b.Chapters = append(b.Chapters[:idx+1], append([]Chapter{newCh}, b.Chapters[idx+1:]...)...)
		} else {
			b.Chapters = append(b.Chapters, newCh)
		}
	default: // "end"
		b.Chapters = append(b.Chapters, newCh)
	}

	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()
	writeJSON(w, map[string]any{"ok": true, "file": file, "book": b})
}

// apiReorder riordina i capitoli in book.yaml in base all'ordine trascinato dall'utente.
func apiReorder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	chMap := make(map[string]Chapter, len(b.Chapters))
	for _, c := range b.Chapters {
		chMap[c.File] = c
	}
	var reordered []Chapter
	seen := make(map[string]bool)
	for _, f := range req.Files {
		if c, ok := chMap[f]; ok && !seen[f] {
			reordered = append(reordered, c)
			seen[f] = true
		}
	}
	// Per sicurezza, preserviamo eventuali capitoli non inclusi nella lista
	for _, c := range b.Chapters {
		if !seen[c.File] {
			reordered = append(reordered, c)
		}
	}
	b.Chapters = reordered
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Salvataggio book.yaml fallito: "+err.Error())
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

func apiDeleteChapter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	name := filepath.Base(strings.TrimSpace(req.File))
	if name == "" || name == "." || !strings.HasSuffix(name, ".md") {
		jsonErr(w, 400, "Capitolo non valido")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun vault aperto")
		return
	}
	// Conferma lato client: qui eliminiamo davvero (file nella "cestina").
	del := filepath.Join(vDir, name)
	st, err := os.Stat(del)
	if err != nil || st.IsDir() {
		jsonErr(w, 404, "Capitolo inesistente")
		return
	}
	// Sposto nel cestino locale .cestina
	dst := filepath.Join(vDir, ".cestina")
	_ = os.MkdirAll(dst, 0o755)
	if err := os.Rename(del, filepath.Join(dst, name)); err != nil {
		jsonErr(w, 500, "Impossibile spostare: "+err.Error())
		return
	}
	for i, c := range b.Chapters {
		if c.File == name {
			b.Chapters = append(b.Chapters[:i], b.Chapters[i+1:]...)
			break
		}
	}
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

// apiBuild esegue le azioni di pubblicazione con serializzazione e blocco delle concorrenze.
func apiBuild(w http.ResponseWriter, r *http.Request) {
	if !buildMu.TryLock() {
		jsonErrWithDiag(w, 409, "Un'altra operazione di pubblicazione è già in corso. Attendi che termini.", "export", "BUILD_BUSY", "apiBuild")
		return
	}
	defer buildMu.Unlock()

	var req struct {
		Kind   string `json:"kind"` // "kindle" | "print"
		Trim   string `json:"trim"`
		Pages  int    `json:"pages"`
		Bleed  *bool  `json:"bleed"`
		OutDir string `json:"outdir"` // cartella di destinazione scelta dall'utente
		Vault  string `json:"vault"`  // vault di origine per prevenire build su progetto disallineato
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrWithDiag(w, 400, "Richiesta non valida", "export", "INVALID_REQUEST", "apiBuild")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErrWithDiag(w, 400, "Apri prima una cartella libro", "export", "NO_VAULT", "apiBuild")
		return
	}
	if req.Vault != "" && filepath.Clean(req.Vault) != filepath.Clean(vDir) {
		jsonErrWithDiag(w, 409, "Vault disallineato: richiesta di build per altro progetto rifiutata", "export", "BOOK_CHANGED", "apiBuild")
		return
	}
	if toolPath == "" {
		jsonErrWithDiag(w, 500, "pubcraft non trovato. Compila con 'make && sudo make install' "+
			"o metti pubcraft nella stessa cartella di pubcraftgui.", "export", "TOOL_NOT_FOUND", "apiBuild")
		return
	}
	b, bookErr := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if bookErr != nil {
		jsonErrWithDiag(w, 400, "Metadati del libro non validi", "export", "INVALID_BOOK", "apiBuild")
		return
	}
	name := printName(b)
	var produced string // file/dir da mostrare nel Finder a fine build
	var cmd *exec.Cmd
	switch req.Kind {
	case "kindle":
		dest := req.OutDir
		if dest == "" {
			dest = vDir
		}
		epub := filepath.Join(dest, name+".epub")
		produced = epub
		cmd = exec.Command(toolPath, "build", vDir, epub, "--kindle")
	case "print":
		cfg := loadPrintConfig(vDir, b)
		if req.Trim != "" {
			cfg.Trim = req.Trim
		}
		if req.Pages > 0 {
			cfg.Pages = req.Pages
		}
		if req.Bleed != nil {
			cfg.Bleed = *req.Bleed
		}
		if req.OutDir != "" {
			cfg.OutDir = req.OutDir
		}
		html, pdf, out, err := buildPrint(vDir, b, cfg)
		resp := map[string]any{"ok": err == nil, "pdf_created": err == nil, "path": pdf, "output": out, "view_print_url": "/api/viewprint/"}
		if err != nil {
			ev := RecordDiagError("export", "PRINT_BUILD_FAILED", "buildPrint", err.Error(), out, "")
			resp["error"] = err.Error()
			resp["error_id"] = ev.ErrorID
			resp["path"] = html
			resp["html_path"] = html
		}
		writeJSON(w, resp)
		return
	default:
		jsonErrWithDiag(w, 400, "Azione non riconosciuta", "export", "INVALID_ACTION", "apiBuild")
		return
	}
	out, err := runTool(cmd)
	resp := map[string]any{"ok": err == nil, "output": out, "path": produced}
	if err != nil {
		ev := RecordDiagError("export", "KINDLE_BUILD_FAILED", "buildKindle", err.Error(), out, "")
		resp["error"] = err.Error()
		resp["error_id"] = ev.ErrorID
		writeJSON(w, resp)
		return
	}
	writeJSON(w, resp)
}

func runTool(c *exec.Cmd) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmdWithCtx := exec.CommandContext(ctx, c.Path, c.Args[1:]...)
	cmdWithCtx.Dir = c.Dir
	cmdWithCtx.Env = c.Env
	out, err := cmdWithCtx.CombinedOutput()
	s := string(out)
	if strings.TrimSpace(s) == "" {
		s = "(nessun output)"
	}
	return s, err
}

// ---------------------------------------------------------------- PDF headless

// findPDFBrowser restituisce il binario per generare PDF headless.
func findPDFBrowser() string {
	if p := os.Getenv("CHROMIUM_BIN"); p != "" && fileExists(p) {
		return p
	}
	switch runtime.GOOS {
	case "darwin":
		type cand struct{ app, bin string }
		cands := []cand{
			{"Brave Browser.app", "Brave Browser"},
			{"Google Chrome.app", "Google Chrome"},
			{"Microsoft Edge.app", "Microsoft Edge"},
			{"Chromium.app", "Chromium"},
			{"Arc.app", "Arc"},
			{"Opera.app", "Opera"},
			{"Vivaldi.app", "Vivaldi"},
		}
		dirs := []string{"/Applications", os.Getenv("HOME") + "/Applications"}
		for _, d := range dirs {
			for _, c := range cands {
				p := filepath.Join(d, c.app, "Contents", "MacOS", c.bin)
				if fileExists(p) {
					return p
				}
			}
		}
		for _, n := range []string{"brave-browser", "google-chrome", "chromium", "msedge"} {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
	case "windows":
		// Cerca prima nel PATH
		for _, n := range []string{"msedge", "chrome", "brave"} {
			if p, err := exec.LookPath(n + ".exe"); err == nil {
				return p
			}
		}
		// Cerca nei percorsi di installazione standard Windows (Edge è sempre presente su Win 10/11)
		progFiles := os.Getenv("ProgramFiles")
		progFilesX86 := os.Getenv("ProgramFiles(x86)")
		localAppData := os.Getenv("LOCALAPPDATA")
		cands := []string{
			filepath.Join(progFilesX86, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFilesX86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		}
		for _, c := range cands {
			if c != "" && fileExists(c) {
				return c
			}
		}
	default: // linux e altri unix
		for _, n := range []string{
			"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
			"brave-browser", "microsoft-edge",
			"/snap/bin/chromium", "/snap/bin/google-chrome", "/snap/bin/brave",
			"wkhtmltopdf", "weasyprint",
		} {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
			if fileExists(n) {
				return n
			}
		}
	}
	return ""
}

func kdpGutter(pages int) float64 {
	if pages <= 150 {
		return 0.375
	}
	if pages <= 300 {
		return 0.500
	}
	if pages <= 500 {
		return 0.625
	}
	if pages <= 700 {
		return 0.750
	}
	if pages <= 800 {
		return 0.875
	}
	return 1.0
}

func countPDFPages(pdfPath string) int {
	data, err := os.ReadFile(pdfPath)
	if err != nil || len(data) == 0 {
		return 0
	}
	pages, err := ParsePDFData(data)
	if err == nil && pages > 0 {
		return pages
	}
	return 0
}

func validatePDFStructure(path string) (int, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat fallito: %w", err)
	}
	if st.Size() < 32 {
		return 0, fmt.Errorf("file PDF troppo piccolo (%d byte)", st.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("lettura fallita: %w", err)
	}
	pages, err := ParsePDFData(data)
	if err != nil {
		return 0, fmt.Errorf("struttura PDF non valida: %w", err)
	}
	return pages, nil
}

// htmlToPDF genera un PDF da una pagina HTML con il motore disponibile con timeout e verifica.
func htmlToPDF(browser, html, pdf string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	absHtml, err := filepath.Abs(html)
	if err != nil {
		absHtml = html
	}
	absPdf, err := filepath.Abs(pdf)
	if err != nil {
		absPdf = pdf
	}

	// Usa un file temporaneo univoco nella stessa cartella per garantire scrittura atomica
	tmpPdf := filepath.Join(filepath.Dir(absPdf), fmt.Sprintf(".tmp-%d-%d.pdf", os.Getpid(), time.Now().UnixNano()))
	defer os.Remove(tmpPdf)

	base := strings.ToLower(filepath.Base(browser))

	// 1. Motore nativo macOS WebKit
	if strings.Contains(base, "html2pdf") {
		cmd := exec.CommandContext(ctx, browser, absHtml, tmpPdf)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pubcraft-html2pdf fallito: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	} else if strings.Contains(base, "wkhtmltopdf") {
		// 2. wkhtmltopdf con dimensioni pagina lette dal CSS
		var pw, ph string
		if data, err := os.ReadFile(absHtml); err == nil {
			re := regexp.MustCompile(`size:\s*([0-9.]+)in\s+([0-9.]+)in`)
			if m := re.FindSubmatch(data); len(m) >= 3 {
				pw = string(m[1]) + "in"
				ph = string(m[2]) + "in"
			}
		}
		args := []string{"--enable-local-file-access", "-T", "0", "-B", "0", "-L", "0", "-R", "0"}
		if pw != "" && ph != "" {
			args = append(args, "--page-width", pw, "--page-height", ph)
		}
		args = append(args, absHtml, tmpPdf)
		cmd := exec.CommandContext(ctx, browser, args...)
		sanitizeLinuxEnv(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("wkhtmltopdf fallito: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	} else if strings.Contains(base, "weasyprint") {
		// 3. weasyprint
		cmd := exec.CommandContext(ctx, browser, absHtml, tmpPdf)
		sanitizeLinuxEnv(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("weasyprint fallito: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	} else {
		// 4. Browser Chromium-based (Chrome, Chromium, Edge, Brave)
		filePath := filepath.ToSlash(absHtml)
		if runtime.GOOS == "windows" {
			filePath = "/" + filePath
		}
		u := (&url.URL{Scheme: "file", Path: filePath}).String()

		cmd := exec.CommandContext(ctx, browser,
			"--headless=new",
			"--disable-gpu",
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--no-pdf-header-footer",
			"--run-all-compositor-stages-before-draw",
			"--virtual-time-budget=5000",
			"--print-to-pdf="+tmpPdf,
			u,
		)
		sanitizeLinuxEnv(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil || !fileExists(tmpPdf) {
			isValid := false
			if fileExists(tmpPdf) {
				if _, valErr := validatePDFStructure(tmpPdf); valErr == nil {
					isValid = true
				}
			}
			if !isValid {
				cmdOld := exec.CommandContext(ctx, browser,
					"--headless",
					"--disable-gpu",
					"--no-sandbox",
					"--disable-dev-shm-usage",
					"--no-pdf-header-footer",
					"--print-to-pdf="+tmpPdf,
					u,
				)
				sanitizeLinuxEnv(cmdOld)
				out2, err2 := cmdOld.CombinedOutput()
				if err2 != nil || !fileExists(tmpPdf) {
					errMsg := strings.TrimSpace(string(out))
					if errMsg == "" {
						errMsg = strings.TrimSpace(string(out2))
					}
					return fmt.Errorf("browser headless fallito (%s)", errMsg)
				}
			}
		}
	}

	if _, err := validatePDFStructure(tmpPdf); err != nil {
		return fmt.Errorf("validazione PDF fallita: %w", err)
	}

	return os.Rename(tmpPdf, absPdf)
}

// ---------------------------------------------------------------- copertine

// Cartella cover/ nel vault: archivio ordinato dei file di copertina
// (immagini per l'EPUB, PDF per la stampa, PSD sorgente, …).
func coverDir() string { return filepath.Join(getVaultDir(), "cover") }

// estensioni che il browser sa mostrare direttamente
var renderable = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true}

type Cover struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "image" | "pdf" | "thumb" | "other"
}

func coverKind(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch {
	case renderable[ext]:
		return "image"
	case ext == "pdf":
		return "pdf"
	case ext == "tif" || ext == "tiff" || ext == "psd" || ext == "ai" || ext == "heic":
		return "thumb"
	default:
		return "other"
	}
}

func listCovers() []Cover {
	entries, err := os.ReadDir(coverDir())
	if err != nil {
		return []Cover{}
	}
	var list []Cover
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue // niente .thumbs e file di sistema
		}
		list = append(list, Cover{Name: name, Kind: coverKind(name)})
	}
	if list == nil {
		list = []Cover{}
	}
	return list
}

func apiCovers(w http.ResponseWriter, r *http.Request) {
	if getVaultDir() == "" {
		jsonErr(w, 400, "Apri prima una cartella libro")
		return
	}
	_ = os.MkdirAll(coverDir(), 0o755)
	writeJSON(w, map[string]any{"covers": listCovers()})
}

// apiAddCover copia un file scelto dall'utente nella cartella cover/.
func apiAddCover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		jsonErr(w, 400, "Percorso mancante")
		return
	}
	src := strings.TrimSpace(req.Path)
	st, err := os.Stat(src)
	if err != nil || st.IsDir() {
		jsonErr(w, 400, "File non trovato: "+src)
		return
	}
	if err := os.MkdirAll(coverDir(), 0o755); err != nil {
		jsonErr(w, 500, "Impossibile creare cover/: "+err.Error())
		return
	}
	name := filepath.Base(src)
	if ext := filepath.Ext(name); ext != "" {
		base := strings.TrimSuffix(name, ext)
		for i := 2; fileExists(filepath.Join(coverDir(), name)); i++ {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
	}
	if err := copyFile(src, filepath.Join(coverDir(), name)); err != nil {
		jsonErr(w, 500, "Copia fallita: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": name, "covers": listCovers()})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func cleanImageSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastDash = false
		} else if r == '-' || r == '_' || r == ' ' || r == '.' {
			if !lastDash && sb.Len() > 0 {
				sb.WriteByte('-')
				lastDash = true
			}
		}
	}
	res := strings.Trim(sb.String(), "-.")
	if len(res) > 50 {
		res = res[:50]
	}
	return res
}

// apiUploadImage carica o salva un'immagine nella cartella images/ del libro corrente.
func apiUploadImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, http.StatusBadRequest, "Nessun progetto aperto")
		return
	}
	imagesDir := filepath.Join(vDir, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Impossibile creare la cartella images/: "+err.Error())
		return
	}

	const maxImageBytes = 25 << 20 // 25 MiB decoded image, excluding transport overhead
	ct := r.Header.Get("Content-Type")
	maxBodyBytes := int64(base64.StdEncoding.EncodedLen(maxImageBytes) + 4096)
	if strings.HasPrefix(ct, "multipart/form-data") {
		maxBodyBytes = int64(maxImageBytes) + (64 << 10)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var filename string
	var fileData []byte
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxImageBytes); err != nil {
			jsonErr(w, http.StatusRequestEntityTooLarge, "File troppo grande o richiesta non valida (max 25MB)")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("image")
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "File immagine mancante nel form")
			return
		}
		defer file.Close()
		filename = header.Filename
		data, err := io.ReadAll(io.LimitReader(file, int64(maxImageBytes)+1))
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "Errore durante la lettura del file: "+err.Error())
			return
		}
		if len(data) > maxImageBytes {
			jsonErr(w, http.StatusRequestEntityTooLarge, "File troppo grande (max 25MB)")
			return
		}
		fileData = data
	} else {
		// JSON con base64 o path
		var req struct {
			Name string `json:"name"`
			Data string `json:"data"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var sizeErr *http.MaxBytesError
			if errors.As(err, &sizeErr) {
				jsonErr(w, http.StatusRequestEntityTooLarge, "Richiesta troppo grande (max 25MB per immagine)")
			} else {
				jsonErr(w, http.StatusBadRequest, "Richiesta JSON non valida")
			}
			return
		}
		filename = req.Name
		if req.Path != "" {
			src := strings.TrimSpace(req.Path)
			st, err := os.Stat(src)
			if err != nil || st.IsDir() {
				jsonErr(w, http.StatusBadRequest, "File non trovato: "+src)
				return
			}
			if st.Size() > int64(maxImageBytes) {
				jsonErr(w, http.StatusRequestEntityTooLarge, "File troppo grande (max 25MB)")
				return
			}
			data, err := os.ReadFile(src)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, "Impossibile leggere il file: "+err.Error())
				return
			}
			fileData = data
			if filename == "" {
				filename = filepath.Base(src)
			}
		} else if req.Data != "" {
			raw := req.Data
			if idx := strings.Index(raw, ","); idx != -1 && strings.Contains(raw[:idx], "base64") {
				raw = raw[idx+1:]
			}
			if len(raw) > (maxImageBytes*4/3)+1024 {
				jsonErr(w, http.StatusRequestEntityTooLarge, "Immagine troppo grande (max 25MB)")
				return
			}
			data, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, "Codifica base64 non valida")
				return
			}
			if len(data) > maxImageBytes {
				jsonErr(w, http.StatusRequestEntityTooLarge, "Immagine troppo grande (max 25MB)")
				return
			}
			fileData = data
		} else {
			jsonErr(w, http.StatusBadRequest, "Dati immagine mancanti")
			return
		}
	}

	if len(fileData) == 0 {
		jsonErr(w, http.StatusBadRequest, "Il file immagine è vuoto")
		return
	}

	// Controllo rigoroso del contenuto effettivo (sniffing dei magic bytes)
	sniffedMime := http.DetectContentType(fileData)
	var canonicalExt string
	switch sniffedMime {
	case "image/jpeg":
		canonicalExt = ".jpg"
	case "image/png":
		canonicalExt = ".png"
	case "image/gif":
		canonicalExt = ".gif"
	case "image/webp":
		canonicalExt = ".webp"
	default:
		jsonErr(w, http.StatusBadRequest, "Il file non è un'immagine valida (supportati: PNG, JPEG, GIF, WEBP)")
		return
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext != canonicalExt && !(canonicalExt == ".jpg" && ext == ".jpeg") {
		ext = canonicalExt
	}

	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	base = cleanImageSlug(base)
	if base == "" {
		base = "image-" + time.Now().Format("20060102-150405")
	}

	finalName := base + ext
	for i := 2; fileExists(filepath.Join(imagesDir, finalName)); i++ {
		finalName = fmt.Sprintf("%s-%d%s", base, i, ext)
	}

	destPath := filepath.Join(imagesDir, finalName)
	if err := atomicWriteFile(destPath, fileData, 0o644); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Impossibile salvare l'immagine: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"ok":       true,
		"filename": finalName,
		"rel_path": "images/" + finalName,
		"url":      "/images/" + finalName,
	})
}

// apiDelCover sposta un file di copertina in .cestina (non lo elimina).
func apiDelCover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Name))
	if name == "" || name == "." || strings.HasPrefix(name, ".") {
		jsonErr(w, 400, "File non valido")
		return
	}
	src := filepath.Join(coverDir(), name)
	if !fileExists(src) {
		jsonErr(w, 404, "File inesistente")
		return
	}
	dst := filepath.Join(getVaultDir(), ".cestina")
	_ = os.MkdirAll(dst, 0o755)
	if err := os.Rename(src, filepath.Join(dst, name)); err != nil {
		jsonErr(w, 500, "Impossibile spostare: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "covers": listCovers()})
}

// apiCoverMedia serve il file di copertina (immagine) o la sua miniatura
// generata con Quick Look (PDF, TIFF, PSD, …), con cache in cover/.thumbs.
func apiCoverMedia(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("file"))
	thumb := r.URL.Query().Get("thumb") == "1"
	path := filepath.Join(coverDir(), name)
	if !fileExists(path) {
		http.Error(w, "non trovato", 404)
		return
	}
	mime := "application/octet-stream"
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg":
		mime = "image/jpeg"
	case "png":
		mime = "image/png"
	case "gif":
		mime = "image/gif"
	case "webp":
		mime = "image/webp"
	}
	if !thumb && renderable[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))] {
		w.Header().Set("Content-Type", mime)
		http.ServeFile(w, r, path)
		return
	}
	// Miniatura via Quick Look (qlmanage), messa in cache.
	thumbs := filepath.Join(coverDir(), ".thumbs")
	_ = os.MkdirAll(thumbs, 0o755)
	cached := filepath.Join(thumbs, name+".png")
	if !fileExists(cached) {
		exec.Command("qlmanage", "-t", "-s", "1200", "-o", thumbs, path).Run()
	}
	if !fileExists(cached) {
		http.Error(w, "miniatura non disponibile", 404)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, cached)
}

// apiUseAsEpubCover: rende un'immagine la copertina dell'EPUB. La copia in
// images/ e crea/aggiorna il capitolo 00-copertina.md con il solo riferimento:
// sendo la prima immagine del libro, pubcraft la marca cover-image.
func apiUseAsEpubCover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Name))
	if coverKind(name) != "image" {
		jsonErr(w, 400, "Per la copertina EPUB serve un'immagine (JPEG/PNG; KDP vuole JPEG/TIFF 2560px)")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	images := filepath.Join(vDir, "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		jsonErr(w, 500, "Impossibile creare images/: "+err.Error())
		return
	}
	dst := filepath.Join(images, name)
	if ext := filepath.Ext(name); ext != "" {
		base := strings.TrimSuffix(name, ext)
		for i := 2; fileExists(dst); i++ {
			dst = filepath.Join(images, fmt.Sprintf("%s-%d%s", base, i, ext))
		}
	}
	if err := copyFile(filepath.Join(coverDir(), name), dst); err != nil {
		jsonErr(w, 500, "Copia in images/ fallita: "+err.Error())
		return
	}
	imgName := filepath.Base(dst)
	// Capitolo 00-copertina: solo il riferimento all'immagine.
	coverFile := "00-copertina.md"
	id, _ := newUUID()
	md := fmt.Sprintf("---\nid: %s\ntitle: Copertina\n---\n\n![](images/%s)\n", id, imgName)
	if err := os.WriteFile(filepath.Join(vDir, coverFile), []byte(md), 0o644); err != nil {
		jsonErr(w, 500, "Creazione 00-copertina.md fallita: "+err.Error())
		return
	}
	found := false
	for i, c := range b.Chapters {
		if c.File == coverFile {
			b.Chapters[i] = Chapter{File: coverFile, ID: id, Title: "Copertina"}
			found = true
			break
		}
	}
	if !found {
		b.Chapters = append([]Chapter{{File: coverFile, ID: id, Title: "Copertina"}}, b.Chapters...)
	}
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

// apiOpenCover apre il file di copertina con l'applicazione predefinita.
func apiOpenCover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Name))
	path := filepath.Join(coverDir(), name)
	if !fileExists(path) {
		jsonErr(w, 404, "File inesistente")
		return
	}
	if err := openPath(path); err != nil {
		jsonErr(w, 500, "Impossibile aprire il file")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// -------------------------------------------------------------- stili libro

// Ornamenti decorativi SVG autentici
const pubcraftSwashSVG = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 288 96'%3E%3Cpath d='M243.442 20.752c0 3.5-2.226 6.372-6.668 8.628-4.454 2.25-11.172 3.524-20.172 3.828-.21 3-1.258 6.046-3.148 9.148 3.61 1.106 6.75 3 9.446 5.704 2.704 2.698 4.054 5.944 4.054 9.75 0 3.398-1.172 6.55-3.528 9.446s-5.39 5.39-9.14 7.5c-3.75 2.102-7.972 3.726-12.668 4.87-4.7 1.142-9.398 1.724-14.11 1.724-3.398 0-6.808-.278-10.2-.84-3.396-.548-7.146-1.462-11.25-2.766-4.114-1.3-8.778-3.058-14.028-5.25-5.25-2.2-11.528-4.946-18.828-8.25a296.124 296.124 0 01-11.102 7.722c-3.7 2.45-7.348 4.582-10.95 6.376-3.602 1.794-7.148 3.222-10.648 4.28-3.5 1.048-6.902 1.58-10.204 1.58-4.996 0-9.07-1.228-12.226-3.68-3.148-2.446-4.976-5.63-5.472-9.516-1.102.202-2.652.422-4.648.664-2 .262-3.95.376-5.852.376-3.3 0-6.352-.432-9.148-1.288-2.796-.856-5.25-2.106-7.352-3.75a16.764 16.764 0 01-4.872-6.08c-1.152-2.382-1.726-5.132-1.726-8.25 0-4.296 1.296-8.026 3.898-11.172 2.602-3.148 6-5.746 10.2-7.796 4.2-2.046 8.95-3.578 14.25-4.578 5.3-1 10.652-1.5 16.05-1.5.796-2.598 2.328-4.922 4.578-6.976 2.25-2.046 4.95-3.796 8.102-5.25 3.148-1.45 6.68-2.546 10.578-3.296a62.583 62.583 0 0111.852-1.128c3.496 0 7.32.324 11.476.98 4.144.648 8.32 1.45 12.524 2.398 4.196.954 8.266 1.98 12.226 3.078a1624.86 1624.86 0 0110.578 3c4.996-2.5 9.696-4.454 14.102-5.852 4.398-1.394 9.304-2.098 14.708-2.098 2.484 0 5.042.356 7.636 1.05 2.594.696 4.75 1.5 6.438 2.398 2.898-1 5.958-1.894 9.152-2.7 3.2-.796 6.796-1.2 10.804-1.2 4.692 0 8.418.876 11.168 2.624 2.75 1.748 4.12 3.796 4.12 6.092zm-110.248 20.1c-6.102-2.102-11.528-3.876-16.274-5.328-4.75-1.444-8.976-2.644-12.67-3.596-3.704-.95-6.954-1.654-9.75-2.102-2.8-.454-5.25-.726-7.348-.828l-.3 2.25c-.3 2.204-.204 4 .3 5.398.496 1.406 1.176 2.5 2.02 3.3.848.796 1.754 1.376 2.704 1.73.946.352 1.77.52 2.476.52.3 0 .372.128.218.372-.148.254-.472.324-.968.23-.704 0-1.676-.2-2.93-.602-1.246-.394-2.454-1.05-3.602-1.95-1.144-.898-2.144-2.074-3-3.524-.852-1.446-1.27-3.226-1.27-5.328 0-.898.102-1.746.3-2.546-4.304 0-8.73.324-13.282.976-4.546.652-8.672 1.828-12.376 3.528-3.696 1.708-6.726 4.074-9.07 7.122-2.352 3.054-3.524 6.93-3.524 11.626 0 6 1.624 10.628 4.872 13.876 3.248 3.248 7.476 4.872 12.672 4.872 2 0 3.95-.102 5.852-.298 1.894-.202 3.394-.452 4.5-.75.094-2.902.898-5.48 2.398-7.73s3.5-4.196 6-5.852c2.504-1.648 5.296-2.894 8.402-3.75 3.102-.856 6.296-1.266 9.598-1.266 2.898 0 5.726.27 8.48.82 2.746.548 4.672.97 5.77 1.278a261.25 261.25 0 009.902-6 466.17 466.17 0 009.9-6.448zm-22.792 14.25c-1.304-.598-3.128-1.146-5.48-1.652-2.352-.49-4.624-.75-6.82-.75-5.906 0-10.8 1.156-14.7 3.456-3.902 2.3-5.852 5.652-5.852 10.046v1.204c0 .5.046.898.148 1.2 2.102-.496 4.55-1.246 7.352-2.25 2.792-.996 5.7-2.122 8.7-3.376 3-1.242 5.976-2.542 8.922-3.888 2.952-1.364 5.526-2.688 7.73-3.99zm21.448 8.552a526.336 526.336 0 01-10.726-4.426c-3.656-1.544-6.726-2.716-9.23-3.53-4.5 2.308-9.472 4.678-14.922 7.132-5.454 2.456-11.778 4.784-18.976 6.976.696 3.5 2.52 6.294 5.476 8.388 2.946 2.112 6.372 3.148 10.274 3.148 3.196 0 6.426-.508 9.672-1.5 3.25-1.004 6.496-2.34 9.75-3.974a86.157 86.157 0 009.598-5.69c3.152-2.14 6.178-4.32 9.084-6.524zm28.2-39.902c-3.208-1-6.878-2.098-11.03-3.296a442.362 442.362 0 00-12.602-3.454 193.844 193.844 0 00-12.296-2.77c-3.958-.75-7.328-1.128-10.128-1.128-7.5 0-13.554 1.2-18.152 3.602-4.602 2.398-7.402 6.102-8.398 11.098 5.296.2 12.176 1.004 20.624 2.402 8.458 1.394 18.23 4 29.332 7.796 1.2-.796 2.746-1.82 4.648-3.07a224.47 224.47 0 016.152-3.898c2.204-1.352 4.352-2.672 6.446-3.972 2.1-1.306 3.904-2.408 5.404-3.31zm-7.2 24.454c-2.396-.902-5.274-1.976-8.626-3.23a658.83 658.83 0 00-9.676-3.52 507.986 507.986 0 01-9.75 6.226c-3.3 2.054-6.554 4.07-9.75 6.074 1.2.304 2.624.726 4.27 1.278 1.648.55 3.394 1.148 5.25 1.8 1.856.648 3.726 1.328 5.628 2.024a97.95 97.95 0 015.25 2.108c3-2.3 5.93-4.476 8.778-6.53 2.848-2.054 5.726-4.134 8.626-6.23zm69.752 9.744c0-7.884-3.352-12.744-10.05-14.544-2.602 3.5-6.45 6.37-11.554 8.626-5.098 2.25-11.394 3.374-18.894 3.374-2.398 0-4.624-.146-6.676-.456a47.129 47.129 0 01-6.222-1.348 116.453 116.453 0 01-6.75-2.168 272.6 272.6 0 00-8.25-2.782 606.44 606.44 0 00-8.624 6.296 627.121 627.121 0 01-8.774 6.454c2.796 1.108 6.454 2.626 10.954 4.574 4.5 1.958 9.32 3.866 14.476 5.708a183.078 183.078 0 0015.826 4.876c5.39 1.394 10.382 2.098 15 2.098 3.5 0 6.996-.348 10.5-1.05 3.5-.696 6.676-1.868 9.528-3.524a20.518 20.518 0 006.898-6.372c1.724-2.604 2.612-5.86 2.612-9.762zm-41.704-28.494a678.603 678.603 0 00-9.528-2.48c-3.75-.95-7.074-1.872-9.972-2.77-1.886 1.2-3.95 2.476-6.14 3.82a449.982 449.982 0 00-6.376 3.976 381.345 381.345 0 01-5.78 3.602c-1.798 1.106-3.254 2.05-4.348 2.852 1.5.496 3.126 1.07 4.874 1.728a343.96 343.96 0 009.97 3.524c1.5.504 2.746.898 3.75 1.2a283.346 283.346 0 0111.626-8.024 316.173 316.173 0 0111.924-7.428zm32.098 3.75c-4.102 0-8.648-.23-13.644-.68-5.008-.45-10.554-1.324-16.648-2.628a194.624 194.624 0 00-12.216 7.282 176.687 176.687 0 00-11.626 8.172c6 2.3 11.646 4.154 16.95 5.546 5.294 1.406 9.394 2.112 12.3 2.112 5.394 0 9.972-.876 13.722-2.624 3.75-1.748 6.622-4.226 8.622-7.422-.602-.2-1.696-.3-3.3-.3-.89 0-2.032.078-3.372.23a18.393 18.393 0 00-3.828.828c-.398.204-.648.148-.75-.148-.11-.304.102-.554.602-.75.898-.398 2.11-.704 3.61-.906 1.5-.196 2.942-.296 4.356-.296.614 0 1.188.024 1.804.078.602.046 1.054.07 1.352.07a20.823 20.823 0 001.418-4.196c.34-1.5.554-2.974.648-4.368zm-8.394-15.904c-1.304-1-3.078-1.824-5.328-2.476-2.25-.652-4.422-.976-6.528-.976-4.402 0-8.602.574-12.598 1.726-4.008 1.152-8.402 2.922-13.208 5.32 1.308.406 2.932.876 4.874 1.43 1.95.55 3.894 1.106 5.864 1.648 1.946.55 3.714 1.028 5.32 1.422 1.598.406 2.696.704 3.304.902 1.594-.902 3.27-1.8 5.02-2.7a512.407 512.407 0 015.176-2.628c1.7-.856 3.254-1.606 4.652-2.25 1.392-.648 2.542-1.118 3.452-1.418zm8.25 14.404c.196-2.598-.43-5.206-1.876-7.8-1.454-2.598-3.176-4.546-5.176-5.852-1 .398-2.278.926-3.824 1.57a72.807 72.807 0 00-4.8 2.25c-1.644.856-3.286 1.708-4.878 2.554-1.618.848-3 1.624-4.2 2.32 1.5.398 3.286.876 5.336 1.43 2.04.55 4.16 1.07 6.368 1.57a97.51 97.51 0 006.676 1.27c2.24.362 4.366.582 6.374.688zm26.09-10.204c0-2.192-.996-4.07-3-5.628-1.992-1.546-5.106-2.32-9.292-2.32-3.704 0-6.86.3-9.458.902a57.075 57.075 0 00-7.792 2.398c2.296 1.5 4.124 3.704 5.472 6.602 1.336 2.894 1.918 5.7 1.714 8.398 6.8 0 12.222-.876 16.286-2.628 4.054-1.748 6.07-4.322 6.07-7.724z' fill='%231c1a17' fill-rule='evenodd'/%3E%3C/svg%3E"
const pubcraftFellFlowerSVG = "data:image/svg+xml,%3Csvg%20fill%3D%22%231b1917%22%20width%3D%22144%22%20height%3D%2272%22%20xmlns%3D%22http%3A//www.w3.org/2000/svg%22%3E%3Cpath%20d%3D%22M76.664%2022.303v.252c0%20.332.082.672.41.672h.176c1.6-.754%202.438-2.268%203.943-3.107%202.438-1.342%206.135-1.428%208.824-1.428h2.016c.422.082%204.377%201.26%205.631%201.68%202.438.84%204.289%202.855%206.223%204.283.164.084%201.09.76%201.172.842.252.17.844.586.844.586h1.676c.082%200%20.504.252.586.252.094%200%201.354.252%201.688.338h.164c1.26%200%201.26-1.936%201.26-2.777%200-1.088-1.09-1.678-1.09-2.688v-.252c.586-1.008%201.342-1.176%202.438-1.176%203.527%200%209.75%204.371%209.75%208.154%200%20.844-.34%201.512-.34%202.355%200%20.082.094.504.094.586.504.08%203.105.25%203.609.332h2.854c.428%200%201.605.172%201.688.252.252.17%201.688%201.514%202.021%202.018.756.926.926%202.102.926%203.191%200%201.008-.088%202.77-1.178%203.273-.17.086-1.014.258-1.178.336%200%200-.422.168-.592.254-.416.25-1.852%201.01-1.928%201.09-.34.25-.34.342-.34.588v.34c.756%201.936%202.268%203.363%204.447%203.363.504%200%201.09-.34%201.518-.592.164-.332%201.008-1.938%201.172-2.268.088-.174.68-.504.844-.504%201.342%200%202.273%202.268%202.273%203.363%200%203.689-3.281%204.535-6.311%204.535-1.764%200-3.439-.662-5.051-1.428-.164-.084-1.512-.754-1.676-.834h-.164c-1.266%200-1.436%201.842-1.852%202.607-.082.078-.756%201.008-.844%201.086-.75%201.008-1.676%202.867-3.111%203.113a12.542%2012.542%200%2001-3.023.336c-3.281%200-4.793-3.527-6.393-5.715-.082-.168-1.254-1.512-1.342-1.68-.416-.762-.416-2.607-1.6-2.607h-.416c-1.354%201.521-2.525%202.865-4.541%202.865-1.348%200-1.77-.426-2.859-1.434-2.355-2.109-2.438-4.289-3.363-7.398-.252-.586-.598-1.008-.598-1.684%200-.166%200-.586.258-.586h4.289c.082%200%20.932-.332%201.09-.504%201.348-1.008%201.764-3.023%201.764-4.629%200-3.613-4.365-7.23-7.143-8.742-.082-.084-1.008-.252-1.09-.252-.34-.168-2.613-1.092-2.859-1.176h-.844c-.164%200-.75-.17-.838-.252-.34%200-1.852-.084-2.186-.084-3.695%200-4.285.672-7.225%202.352-.084%200-.504.252-.504.252-1.854.766-2.943%201.773-2.943%203.793v.422c0%20.67.086%201.342.67%201.764h3.951c.162%200%201.344.254%201.674.586.51.424%202.279%202.268%202.533%202.52%201.766%202.016.584%205.041%203.443%206.217.164.17.834.334.834.334.174%200%201.512-.504%201.686-.586h.33c.258%200%20.42%200%20.512.252.082.334.504%201.846.586%202.268v.34c0%202.936-1.176%206.223-4.545%206.223h-1.088c-.172%200-1.514-.516-1.766-.598-.252%200-1.344.164-1.602.164%200%20.176-.082.844-.082%201.014%200%20.088.25.756.34.838v1.19c0%20.076.504.75.504.832v.252c-.923%202.935-3.53%205.373-6.811%205.373-2.016%200-3.105-2.859-4.365-2.859h-.428c-1.344%201.09-3.275%202.613-5.125%202.613-2.355%200-6.557-1.02-6.557-4.125%200-1.348.592-2.52%201.771-3.281h.25c1.17%200%20.84%203.193%202.27%203.949.25.176%201.428.586%201.68.586.617.088%202.889-1.928%202.889-2.689v-.164c-.67-.926-2.523-.762-2.857-1.43%200-.082.252-.762.252-.844-.846%200-1.682-.252-2.527-.252h-.252c-.082%200-.756.252-.836.252h-.172c-1.09%200-2.27-.504-3.277-.844-2.771-.838-4.289-1.43-4.289-4.693%200-1.102.252-2.279%201.176-2.947.252%200%20.846.082%201.35.082.588%200%201.092-.082%201.26-.082.756-.17%201.766-1.09%202.27-1.682.082-.592.504-3.193.504-3.697v-2.016c.252-2.268%203.365-3.363%205.301-3.363h.08c.504%200%201.684.252%201.768.252.086%200%20.756.504.844.586h.586c1.008%200%202.016-1.26%202.016-2.27%200-.676-.078-1.008-.334-1.684-2.016-2.355-4.123-4.285-7.141-5.123-.252-.084-1.01-.252-1.098-.336h-.838c-.17%200-1.26-.504-1.43-.504-.586-.168-2.355-.334-2.859-.334h-.256c-4.033.838-6.887%202.688-9.912%205.375-1.68%201.512-3.441%204.875-3.441%207.143v.584c.504%202.27%202.018%203.117%204.285%203.117%202.609%200%203.945-2.613%204.961-2.613h.164c0%20.088.25.252.25.34v.252l-.248.262v1.17c-.338%201.684-1.26%203.193-2.268%204.541-2.688%203.363-2.941%205.209-7.393%206.217-.178.088-1.012.258-1.186.334h-.252c-1.098%200-2.771-.504-3.441-1.43-.084-.164-.756-1.336-.838-1.436-.086-.086-.422-.504-.504-.586h-.342c-1.854.586-2.27%202.689-3.367%203.943-2.346%202.701-4.871%204.635-8.488%204.635-3.361%200-4.955-2.689-6.553-5.133-.168%200-.756-.076-.846-.076-1.178%200-1.596.504-2.604.932-1.588.58-3.607%201.084-5.371%201.084-.088%200-.764-.252-.844-.252-2.521-.756-5.711-1.934-5.711-4.951v-.504c0-.68.08-1.523.588-1.939.334-.252%201.348-.838%201.428-.838h1.34c.592%200%201.268.082%201.52.586-.424.838-1.52%201.26-1.52%202.186%200%20.756.254.922.924%201.18.254%200%201.432.08%201.684.08.504%200%201.68-.08%201.764-.08.088%200%201.262-.504%201.348-.59.84-.258%202.102-.512%202.102-1.52%200-.086-.082-.412-.082-.412-.582-1.35-1.684-1.436-2.014-2.027-.352-.732-1.107-2.578-1.107-3.674%200-1.594.756-2.098%201.428-3.363%200-.08.17-1.088.254-1.18.168-.08%201.512-.756%201.682-.836.166-.08%201.006-.756%201.174-.838.504-.338%201.26-.338%201.932-.338h.76c1.592%200%202.938.592%204.535.592.844%200%201.016-.758%201.266-1.348v-.336c-.094-.252-.758-2.271-.848-2.523v-.168c0-2.855%202.268-4.367%204.289-6.051.084-.082.76-.838.84-.924.51-.342%201.52-.51%201.938-.51.252-.084%202.018-.252%202.611-.336h.252c.414.084%201.586.252%201.68.336.084%200%20.252.168.336.252.076.168.504.756.58.84v.168c0%20.504-.504%201.008-.92%201.26-.09.084-1.516%201.014-1.68%201.176-.34.252-1.684%201.596-1.934%201.936-.084.086-.084.258-.084.338%200%201.01%202.182%201.684%203.025%201.684%201.68%200%203.529-1.432%204.953-2.016.09%200%201.01-.252%201.18-.252.42-.172%202.184-.756%202.52-.84%201.008-.42%201.68-1.262%202.527-1.766.334-.174%201.934-.93%202.266-1.098.086-.082.928-.25%201.18-.25%201.68-.422%203.189-.926%204.789-1.43.342%200%201.932-.082%202.273-.082h1.512c3.191%200%206.385.082%208.994%202.016.084.082%201.678%201.512%202.27%202.016.588.504.756%201.178%201.43%201.682.082-.084.252-.504.334-.588v-.248c-.082-.168-.504-.756-.586-.842-.094-.504-.256-2.688-.344-3.113v-.584c.504-1.26.924-2.268%202.273-2.268%201.766%200%202.604%201.844%202.604%203.359-.004.835-.338%201.593-.338%202.525zm-34.293%207.73h-.924c-1.682%200-2.939.418-4.451.838-1.098.338-1.352.172-2.273.59-3.277%201.426-7.729%203.695-7.729%207.9%200%201.26%201.17%201.764%202.354%201.764.844%200%203.188-1.09%203.943-1.764.252-.17%202.021-2.016%202.277-2.268.924-.844%202.016-1.514%202.854-2.521.09-.17%201.016-1.26%201.098-1.432.078-.08.92-.428%201.172-.584.082%200%201.008-.17%201.09-.252.252-.172%201.26-1.18%201.43-1.43l-.841-.841zm32.611%202.276c-.084%200-.338-.086-.422-.086-.928%200-1.182%201.934-1.262%202.605-.08.252-.252%202.098-.252%202.27-.082.164-.586%201.088-.586%201.172-.34.762-.594%202.023-.594%202.863%200%201.686%201.512%202.859%203.031%202.859%202.604%200%203.523-2.102%203.523-4.449.002-2.701-1.334-5.475-3.438-7.234zm6.299%2018.566c0-.34%200-.674-.41-.674h-.17c-.68%200-3.873.258-4.541.258-.34.082-.34.34-.34.586v.258c.094.164.926%201.084%201.178%201.43.252.328%201.096%201.09%201.096%201.166h.668c1.512%200%202.52-1.166%202.52-2.689l-.001-.335zm37.395-12.861c-1.336-1.426-5.039-3.779-6.967-3.779-.586%200-1.26.17-1.26%201.094v.082c.082.252%201.008%202.016%201.178%202.268%200%20.08.17.838.258.838.076.086.75.508.838.59.076.08.252.254.328.254.932%201.512%201.939%202.691%203.117%204.031.932%201.102%201.6%202.275%203.281%202.275%201.26%200%202.098-.844%202.098-2.025v-.834c-.68-1.683-1.518-3.529-2.871-4.794z%22%20fill-rule%3D%22evenodd%22/%3E%3C/svg%3E"
const pubcraftHederaSVG = "data:image/svg+xml,%3Csvg%20fill%3D%22%234a4237%22%20width%3D%2272%22%20height%3D%2264%22%20xmlns%3D%22http%3A//www.w3.org/2000/svg%22%3E%3Cpath%20d%3D%22M19.61%2021.36c.51-2.502%202.415-3.951%204.322-3.951%202.191%200%204.214%201.45%204.214%204.12%200%201.78-1.012%203.62-2.134%205.115a63.865%2063.865%200%2001-1.798%201.948c2.81%202.337%207.132%204.118%209.605%204.235-.729-1.394-.957-2.615-.957-3.733%200-5.507%205.223-8.29%209.608-8.29%204.273%200%206.733%201.886%207.86%203.786%201.238%202.054%201.574%204.62%201.574%208.241%200%203.833.622%206.174%201.12%207.177.564%201.233%201.693%202.008%202.976%202.008%201.519%200%203.04-1.116%203.04-2.732%200-2.049-.79-2.94-1.796-3.665-.514-.334-.514-.507-.226-.892a3.01%203.01%200%20012.243-.997c1.857%200%203.826%201.67%203.826%204.39%200%204.286-4.557%205.958-7.869%205.958-1.075%200-3.086.996-6.18%202.61-3.316%201.716-7.579%203.458-11.29%203.458-6.737%200-10.448-5.73-10.448-8.849%200-3.5%201.742-5.114%204.608-6.292v-.119c-2.299-.667-5.835-2.005-8.983-4.615-.617.66-1.797%201.84-2.808%202.95%201.067.326%202.135%201.554%202.135%202.947%200%201.782-1.013%203.003-2.135%204.557-1.63%202.286-2.245%203.665-2.245%205.795%200%201.384.445%202.776%201.514%203.66.62.34.51.955-.112.9-3.084-.335-6.738-2.111-6.738-7.19%200-1.276.393-2.614.619-3.34.113-.44%200-.724-.563-.724-.391%200-1.286.39-1.573.724-.223.168-.56.168-.56-.334%200-4.23%204.322-7.186%207.02-7.288%201.625-1.436%202.695-2.44%203.983-3.884-2.808-2.569-4.94-6.297-4.94-10.917%200-6.067%204.156-9.233%206.907-9.233%201.687%200%202.304.994%202.475%202.111.506.337.728.724.84.999.224.668-.45.838-.618%201.165-.284-.549-.844-.883-1.968-.883-1.626%200-4.943%201.718-4.943%206.568%200%20.948.056%201.835.278%202.503l.118.002zm3.765-1.833c-1.291%200-2.81%201.057-2.81%204.786.504%201.111%201.345%202.228%202.243%203.115%201.856-2.005%202.474-3.889%202.474-5.281%200-1.563-.562-2.62-1.907-2.62z%22%20fill-rule%3D%22evenodd%22/%3E%3C/svg%3E"
const pubcraftShirleySVG = "data:image/svg+xml,%3Csvg%20fill%3D%22%235a2e38%22%20width%3D%22240%22%20height%3D%2260%22%20xmlns%3D%22http%3A//www.w3.org/2000/svg%22%3E%3Cg%20fill-rule%3D%22evenodd%22%3E%3Cpath%20d%3D%22M37%2032c0-1.292%202.896-7.5%209.5-7.5%208.5%200%2014%207.5%2013%2015-.868%206.509-6.5%2013.5-21%2013.5C29%2053%2014%2046.5%2014%2032S24.5%206.5%2049.708%207.74c17.04.839%2054.349%2018.753%2072.41%2027.76%2018.062%209.007%2024.934%2011.047%2029.882%2012%2011.5%202.216%2020.717-4.487%2020.717-13%200-3.21-2.821-10-11.217-10-3.62%200-4.5%202.435-4.5%203.5%200%20.5-.5.5-1%200s-.5-1.5-.5-2c0-2%203.363-4%206-4%2011.217%200%2017.5%209%2016%2016.5s-9%2015-22%2013.5-27.782-11.22-40.781-17.5c-13-6.28-57.942-30.04-77.719-21.65C22.5%2019%2022.661%2028.881%2022.661%2034.5c0%208.5%207.352%2017.225%2015.839%2017.354C48%2052%2055.134%2044.532%2055%2038.5%2054.825%2030.621%2050.444%2027%2045%2027s-7.5%204.424-7.5%205c0%20.576-.5%201-.5%200z%22/%3E%3Cpath%20d%3D%22M204.533%2028.164c0%201.292-2.897%207.5-9.5%207.5-8.5%200-14-7.5-13-15%20.868-6.508%206.5-13.5%2021-13.5%209.5%200%2024.5%206.5%2024.5%2021s-10.5%2025.5-35.708%2024.26c-17.04-.838-54.349-18.753-72.41-27.76-18.062-9.006-24.934-11.046-29.882-12-11.5-2.216-20.717%204.487-20.717%2013%200%203.21%202.821%2010%2011.217%2010%203.62%200%204.5-2.435%204.5-3.5%200-.5.5-.5%201%200s.5%201.5.5%202c0%202-3.363%204-6%204-11.217%200-17.5-9-16-16.5s9-15%2022-13.5%2027.782%2011.22%2040.781%2017.5c13%206.28%2057.942%2030.04%2077.719%2021.651%2014.5-6.15%2014.339-16.033%2014.339-21.65%200-8.5-7.352-17.225-15.84-17.355-9.5-.146-16.633%207.322-16.5%2013.354.176%207.88%204.557%2011.5%2010%2011.5%205.445%200%207.5-4.424%207.5-5%200-.576.5-1%20.5%200z%22/%3E%3C/g%3E%3C/svg%3E"
const pubcraftGradientRuleSVG = "data:image/svg+xml,%3Csvg%20width%3D%22576%22%20height%3D%2265%22%20xmlns%3D%22http%3A//www.w3.org/2000/svg%22%3E%3Cdefs%3E%3ClinearGradient%20x1%3D%220%25%22%20y1%3D%2250%25%22%20x2%3D%22100%25%22%20y2%3D%2250%25%22%20id%3D%22a%22%3E%3Cstop%20stop-color%3D%22%23FFF%22%20offset%3D%220%25%22/%3E%3Cstop%20stop-color%3D%22%23999%22%20offset%3D%2220%25%22/%3E%3Cstop%20offset%3D%2250%25%22/%3E%3Cstop%20stop-color%3D%22%23999%22%20offset%3D%2280%25%22/%3E%3Cstop%20stop-color%3D%22%23FFF%22%20offset%3D%22100%25%22/%3E%3C/linearGradient%3E%3C/defs%3E%3Cpath%20d%3D%22M0%200h576v3H0z%22%20transform%3D%22translate%280%2031%29%22%20fill%3D%22url%28%23a%29%22%20fill-rule%3D%22evenodd%22/%3E%3C/svg%3E"

var fontFacesCache = make(map[string]string)
var fontFacesMu sync.Mutex

func getFontFace(family, filename string, weights ...string) string {
	fontFacesMu.Lock()
	defer fontFacesMu.Unlock()
	key := family + ":" + filename
	if ff, ok := fontFacesCache[key]; ok {
		return ff
	}
	data, err := webFS.ReadFile("web/static/fonts/" + filename)
	if err == nil && len(data) > 0 {
		b64 := base64.StdEncoding.EncodeToString(data)
		wList := weights
		if len(wList) == 0 {
			wList = []string{"normal"}
		}
		// formato e mime in base all'estensione (ttf o woff2)
		mime, format := "font/truetype", "truetype"
		if strings.HasSuffix(strings.ToLower(filename), ".woff2") {
			mime, format = "font/woff2", "woff2"
		}
		var b strings.Builder
		for _, w := range wList {
			b.WriteString(fmt.Sprintf("@font-face {\n  font-family: '%s';\n  src: url('data:%s;charset=utf-8;base64,%s') format('%s'),\n       url('/static/fonts/%s') format('%s');\n  font-weight: %s;\n  font-style: normal;\n}\n", family, mime, b64, format, filename, format, w))
		}
		ff := b.String()
		fontFacesCache[key] = ff
		return ff
	}
	return ""
}

// ---------------------------------------------------- font del corpo (body)

// Font per il testo del libro: solo open (Google Fonts, OFL/Apache),
// incorporati nel binario così la resa è identica ovunque (Atticus-style:
// lista curata, non 1500 font). "" = Georgia di sistema.
type BodyFont struct{ ID, Family, Label string }

var bodyFonts = []BodyFont{
	{"ebgaramond", "EB Garamond", "EB Garamond"},
	{"librebaskerville", "Libre Baskerville", "Libre Baskerville"},
	{"lora", "Lora", "Lora"},
	{"crimsonpro", "Crimson Pro", "Crimson Pro"},
	{"spectral", "Spectral", "Spectral"},
}

var bodyFontCache = map[string]string{}

// fontCSSFor: @font-face (400/700, normale+italic) + regola font-family.
func fontCSSFor(id string) (string, bool) {
	if id == "" || id == "georgia" {
		return "", true
	}
	var def *BodyFont
	for i := range bodyFonts {
		if bodyFonts[i].ID == id {
			def = &bodyFonts[i]
			break
		}
	}
	if def == nil {
		return "", false
	}
	fontFacesMu.Lock()
	if c, ok := bodyFontCache[id]; ok {
		fontFacesMu.Unlock()
		return c, true
	}
	fontFacesMu.Unlock()
	css := getFontFace(def.Family, id+"-400.woff2", "400") +
		getFontFace(def.Family, id+"-700.woff2", "700")
	// variante italic (file separato, peso 400)
	if data, err := webFS.ReadFile("web/static/fonts/" + id + "-i400.woff2"); err == nil && len(data) > 0 {
		b64 := base64.StdEncoding.EncodeToString(data)
		css += fmt.Sprintf("@font-face {\n  font-family: '%s';\n  src: url('data:font/woff2;charset=utf-8;base64,%s') format('woff2');\n  font-weight: 400;\n  font-style: italic;\n}\n", def.Family, b64)
	}
	css += fmt.Sprintf("body, .preview, .chapter, .chapter p, p, blockquote, li { font-family: '%s', Georgia, serif !important; }\n", def.Family)
	fontFacesMu.Lock()
	bodyFontCache[id] = css
	fontFacesMu.Unlock()
	return css, true
}

func apiFontCSS(w http.ResponseWriter, r *http.Request) {
	css, ok := fontCSSFor(strings.TrimSpace(r.URL.Query().Get("family")))
	if !ok {
		http.Error(w, "font sconosciuto", 404)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write([]byte(css))
}

func apiBodyFonts(w http.ResponseWriter, r *http.Request) {
	list := []map[string]string{{"id": "", "name": "Georgia (sistema)"}}
	for _, f := range bodyFonts {
		list = append(list, map[string]string{"id": f.ID, "name": f.Label})
	}
	writeJSON(w, map[string]any{"fonts": list})
}

// --------------------------------------------------- separatori di scena

// Ornamenti per le interruzioni di scena (*** / --- nel markdown → <hr>).
// "" = asterischi classici.
var sceneBreaks = []struct{ ID, Name, CSS string }{
	{"asterischi", "Asterischi (* * *)", `
hr { border: none; margin: 2em 0; text-align: center; }
hr::after { content: "* * *"; display: block; letter-spacing: .6em; color: #1c1a17; font-size: 1em; }
hr + p, hr.sb + p { text-indent: 0; }`},
	{"fleuron", "Fleuron (svirgolata)", `
hr { border: none; margin: 2em 0; }
hr::after { content: ""; display: block; width: 7em; height: 24px; margin: 0 auto; background: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 120 30'%3E%3Cpath d='M6 16 C 16 4 30 4 38 16 C 44 25 34 28 30 22 C 26 15 40 8 60 16 C 80 24 96 8 114 16' fill='none' stroke='%231c1a17' stroke-width='1.6' stroke-linecap='round'/%3E%3C/svg%3E") no-repeat center / contain; }
hr + p, hr.sb + p { text-indent: 0; }`},
	{"losanga", "Losanga", `
hr { border: none; margin: 2em 0; text-align: center; }
hr::after { content: "\25c7"; display: block; color: #1c1a17; font-size: 1.05em; }
hr + p, hr.sb + p { text-indent: 0; }`},
	{"linea", "Linea con rombo", `
hr { border: none; border-top: 1px solid #b9b2a4; margin: 2.2em 5em; }
hr::after { content: "\25c6"; display: block; width: 3em; margin: -0.72em auto 0; background: #fffdf8; text-align: center; color: #1c1a17; font-size: .75em; }
@media print { hr::after { background: #fff; } }
hr + p, hr.sb + p { text-indent: 0; }`},
}

func sceneBreakCSSFor(id string) (string, bool) {
	if id == "" {
		id = "asterischi"
	}
	for _, s := range sceneBreaks {
		if s.ID == id {
			return s.CSS, true
		}
	}
	return "", false
}

func apiSceneBreakCSS(w http.ResponseWriter, r *http.Request) {
	css, ok := sceneBreakCSSFor(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "ornamento sconosciuto", 404)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(css))
}

func apiSetSceneBreak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SceneBreak string `json:"scenebreak"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	if _, ok := sceneBreakCSSFor(req.SceneBreak); !ok {
		jsonErr(w, 400, "Ornamento sconosciuto: "+req.SceneBreak)
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b.SceneBreak = req.SceneBreak
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

// apiStats: parole totali del libro (per stimare le pagine di stampa).
func apiStats(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	words := 0
	for _, c := range b.Chapters {
		data, err := os.ReadFile(filepath.Join(vDir, c.File))
		if err != nil {
			continue
		}
		body := string(data)
		if i := strings.Index(body, "\n---"); i >= 0 && strings.HasPrefix(body, "---") {
			if j := strings.Index(body[i+1:], "\n---"); j >= 0 {
				body = body[i+1+j+len("\n---"):]
			}
		}
		words += len(strings.Fields(body))
	}
	// 6x9 @11pt ≈ 300 parole/pagina + ~5 pagine di apparato (front/indice)
	pages := words/300 + 5
	writeJSON(w, map[string]any{"words": words, "pages_est": pages})
}

func apiSetFont(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Font string `json:"font"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	if _, ok := fontCSSFor(req.Font); !ok {
		jsonErr(w, 400, "Font sconosciuto: "+req.Font)
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b.Font = req.Font
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

// Preset tipografici professionali. Ogni preset è un
// override CSS completo e armonico: vale sia per print.html (selettori .chapter)
// sia per l'anteprima dell'editor (selettori .preview).
type StylePreset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CSS  string `json:"-"`
}

var stylePresets = []StylePreset{
	{ID: "classic", Name: "Classic (Kindred / Ornato)", CSS: `
.chapter, .preview {
  color: #1a1815;
  line-height: 1.62;
}
.chapter h2, .preview .chtitle {
  font-family: "Palatino", "Palatino Linotype", "Book Antiqua", Georgia, serif;
  font-size: 1.35em;
  font-weight: normal;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .15em;
  font-variant: normal;
  margin: 1.8em auto .3em;
  line-height: 1.45;
  color: #1a1815;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 78px;
  height: 26px;
  margin: .85em auto 1.8em;
  background: url("` + pubcraftSwashSVG + `") no-repeat center / contain;
}
.chapter h3, .preview h2 {
  font-family: "Palatino", "Palatino Linotype", Georgia, serif;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .14em;
  font-weight: normal;
  font-size: .95em;
  margin: 2.5em auto 1.1em;
  color: #1a1815;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Great Vibes", "Apple Chancery", cursive, Georgia, serif;
  font-size: 3.9em;
  font-weight: normal;
  font-style: normal;
  float: left;
  line-height: .66;
  margin-top: .04em;
  margin-bottom: -.06em;
  margin-right: .13em;
  color: #1a1815;
}`},
	{ID: "oxford", Name: "Oxford (Classico Accademico)", CSS: `
.chapter, .preview {
  color: #1c1917;
  line-height: 1.68;
}
.chapter h2, .preview .chtitle {
  font-family: "Cormorant SC", "Cormorant", Garamond, serif;
  font-size: 1.48em;
  font-weight: 600;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .18em;
  margin: 1.9em auto .3em;
  line-height: 1.4;
  color: #1c1917;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 48px;
  height: 24px;
  margin: .8em auto 1.8em;
  background: url("` + pubcraftFellFlowerSVG + `") no-repeat center / contain;
}
.chapter h3, .preview h2 {
  font-family: "Cormorant", Garamond, serif;
  text-align: center;
  font-style: italic;
  font-weight: normal;
  font-size: 1.15em;
  margin: 2.2em auto 1em;
  letter-spacing: .03em;
  color: #3f3933;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Cormorant", Garamond, serif;
  font-size: 3.8em;
  font-weight: 600;
  font-style: normal;
  float: left;
  line-height: .74;
  margin-top: .06em;
  margin-bottom: -.04em;
  margin-right: .12em;
  color: #1c1917;
}`},
	{ID: "meridian", Name: "Meridian (Prestigio Storico)", CSS: `
.chapter, .preview {
  color: #171614;
  line-height: 1.65;
}
.chapter h2, .preview .chtitle {
  font-family: "Baskerville", "Times New Roman", serif;
  font-size: 1.4em;
  font-weight: normal;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .18em;
  margin: 2em auto .3em;
  color: #171614;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 76px;
  height: 8px;
  margin: .9em auto 1.9em;
  background: url("` + pubcraftGradientRuleSVG + `") no-repeat center / contain;
}
.chapter h3, .preview h2 {
  font-family: "Baskerville", "Times New Roman", serif;
  text-align: center;
  font-style: italic;
  font-weight: normal;
  font-size: 1.12em;
  margin: 2.2em auto .9em;
  color: #3b3731;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Cinzel Decorative", "Cinzel", "Baskerville", serif;
  font-size: 3.7em;
  font-weight: normal;
  font-style: normal;
  float: left;
  line-height: .72;
  margin-top: .06em;
  margin-bottom: -.04em;
  margin-right: .14em;
  color: #171614;
}`},
	{ID: "whisper", Name: "Whisper (Narrativa Intima)", CSS: `
.chapter, .preview {
  color: #211e1a;
  line-height: 1.74;
}
.chapter h2, .preview .chtitle {
  font-family: Georgia, serif;
  font-size: 1.35em;
  font-weight: normal;
  text-align: center;
  letter-spacing: .12em;
  text-transform: uppercase;
  margin: 2.2em auto .3em;
  color: #211e1a;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 34px;
  height: 30px;
  margin: .85em auto 1.8em;
  background: url("` + pubcraftHederaSVG + `") no-repeat center / contain;
}
.chapter h3, .preview h2 {
  font-family: Georgia, serif;
  font-style: italic;
  font-weight: normal;
  font-size: 1.05em;
  text-align: center;
  margin: 2.2em auto 1.1em;
  color: #554e44;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Mrs Saint Delafield", cursive, Georgia, serif;
  font-size: 4.8em;
  font-weight: normal;
  font-style: normal;
  float: left;
  line-height: .58;
  margin-top: .15em;
  margin-bottom: -.12em;
  margin-right: .15em;
  color: #211e1a;
}`},
	{ID: "decorum", Name: "Decorum (Art Déco / Noir)", CSS: `
.chapter, .preview {
  color: #151413;
  line-height: 1.66;
}
.chapter h2, .preview .chtitle {
  font-family: "Bodoni Moda", "Didot", serif;
  font-size: 1.55em;
  font-weight: 500;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .26em;
  margin: 2.2em auto .4em;
  color: #151413;
}
.chapter h2::after, .preview .chtitle::after {
  content: "◇   ◆   ◇";
  display: block;
  text-align: center;
  font-size: .8em;
  letter-spacing: .35em;
  color: #7a6850;
  margin: .8em auto 1.8em;
}
.chapter h3, .preview h2 {
  font-family: "Bodoni Moda", "Didot", serif;
  font-style: italic;
  font-weight: normal;
  font-size: 1.1em;
  text-align: center;
  margin: 2.2em auto 1em;
  color: #3d3832;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Bodoni Moda", "Didot", serif;
  font-size: 3.7em;
  font-weight: 500;
  font-style: normal;
  float: left;
  line-height: .75;
  margin-top: .05em;
  margin-bottom: -.05em;
  margin-right: .12em;
  color: #151413;
}`},
	{ID: "directive", Name: "Directive (Tech & Business)", CSS: `
.chapter, .preview {
  color: #181818;
  line-height: 1.62;
}
.chapter h2, .preview .chtitle {
  font-family: "Montserrat", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 1.45em;
  font-weight: 700;
  text-align: left;
  text-transform: uppercase;
  letter-spacing: .08em;
  margin: 2.4em 0 .3em;
  color: #111;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 44px;
  height: 3px;
  background: #111;
  margin: .7em 0 1.6em 0;
}
.chapter h3, .preview h2 {
  font-family: "Montserrat", sans-serif;
  font-weight: 600;
  font-size: 1.05em;
  text-align: left;
  text-transform: uppercase;
  letter-spacing: .06em;
  margin: 2em 0 .8em;
  color: #444;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Montserrat", -apple-system, sans-serif;
  font-size: 3.2em;
  font-weight: 700;
  font-style: normal;
  float: left;
  line-height: .74;
  margin-top: .08em;
  margin-bottom: -.04em;
  margin-right: .14em;
  color: #111;
}`},
	{ID: "romantico", Name: "Romantico (Flourish Calligrafico)", CSS: `
.chapter, .preview {
  color: #1c1819;
  line-height: 1.65;
}
.chapter h2, .preview .chtitle {
  font-family: "Palatino", "Book Antiqua", serif;
  font-size: 1.4em;
  font-weight: normal;
  text-align: center;
  font-variant: small-caps;
  letter-spacing: .12em;
  margin: 1.9em auto .3em;
  color: #1c1819;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 86px;
  height: 22px;
  margin: .85em auto 1.8em;
  background: url("` + pubcraftShirleySVG + `") no-repeat center / contain;
}
.chapter h3, .preview h2 {
  font-family: "Palatino", "Book Antiqua", serif;
  text-align: center;
  font-style: italic;
  font-weight: normal;
  font-size: 1.12em;
  margin: 2em auto .8em;
  color: #4a2832;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Great Vibes", cursive, Georgia, serif;
  font-size: 3.8em;
  font-weight: normal;
  font-style: normal;
  float: left;
  line-height: .68;
  margin-top: .04em;
  margin-bottom: -.06em;
  margin-right: .12em;
  color: #5a2e38;
}`},
	{ID: "editoriale", Name: "Editoriale (Grandi Romanzi)", CSS: `
.chapter, .preview {
  color: #1a1816;
  line-height: 1.68;
}
.chapter h2, .preview .chtitle {
  font-family: "Cormorant", Garamond, serif;
  font-size: 2.5em;
  font-style: italic;
  font-weight: normal;
  text-align: center;
  letter-spacing: -.01em;
  margin: 1.4em auto .2em;
  line-height: 1.15;
  color: #1a1816;
}
.chapter h2::after, .preview .chtitle::after {
  content: none;
}
.chapter h3, .preview h2 {
  text-align: center;
  font-variant: small-caps;
  text-transform: uppercase;
  letter-spacing: .16em;
  font-weight: normal;
  font-size: 1.02em;
  margin: 2.4em auto 1em;
  color: #443e38;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "Cormorant", Garamond, serif;
  font-size: 3.8em;
  font-style: italic;
  font-weight: normal;
  float: left;
  line-height: .78;
  margin-top: .04em;
  margin-right: .12em;
  color: #1a1816;
}`},
	{ID: "racconto", Name: "Racconto (Vintage / Macchina da Scrivere)", CSS: `
.chapter, .preview {
  color: #22201e;
  line-height: 1.65;
}
.chapter h2, .preview .chtitle {
  font-family: "American Typewriter", monospace;
  font-size: 1.45em;
  font-weight: 500;
  text-align: center;
  text-transform: uppercase;
  letter-spacing: .08em;
  margin: 2em auto .4em;
  color: #22201e;
}
.chapter h2::after, .preview .chtitle::after {
  content: "·   ·   ·";
  display: block;
  text-align: center;
  font-size: 1.1em;
  letter-spacing: .35em;
  color: #8a6d3b;
  margin: .6em auto 1.6em;
}
.chapter h3, .preview h2 {
  font-family: "American Typewriter", monospace;
  text-align: left;
  font-size: 1.05em;
  font-weight: 600;
  margin: 1.8em 0 .6em;
  color: #333;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: "American Typewriter", monospace;
  font-size: 3.1em;
  font-weight: 600;
  float: left;
  line-height: .8;
  margin-top: .08em;
  margin-right: .14em;
  color: #22201e;
}`},
	{ID: "classico", Name: "Classico (Aldino Rinascimentale)", CSS: `
.chapter, .preview {
  color: #1a1815;
  line-height: 1.66;
}
.chapter h2, .preview .chtitle {
  font-size: 1.4em;
  font-weight: normal;
  text-align: center;
  font-variant: small-caps;
  letter-spacing: .12em;
  margin: 2em auto .4em;
  color: #1a1815;
}
.chapter h2::after, .preview .chtitle::after {
  content: "⁂";
  display: block;
  text-align: center;
  font-size: 1em;
  color: #7a6850;
  margin: .6em auto 1.6em;
}
.chapter h3, .preview h2 {
  font-style: italic;
  font-weight: normal;
  font-size: 1.1em;
  text-align: center;
  margin: 2.2em auto .8em;
  color: #3a342c;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: Garamond, Georgia, serif;
  font-size: 3.5em;
  font-weight: normal;
  font-style: normal;
  float: left;
  line-height: .78;
  margin-top: .05em;
  margin-right: .11em;
  color: #1a1815;
}`},
	{ID: "moderno", Name: "Moderno (Swiss Clean)", CSS: `
.chapter, .preview {
  color: #111;
  line-height: 1.62;
}
.chapter h2, .preview .chtitle {
  font-family: -apple-system, "Helvetica Neue", sans-serif;
  font-size: 1.65em;
  font-weight: 600;
  text-align: left;
  letter-spacing: -.02em;
  margin: 2.4em 0 .6em;
  color: #111;
}
.chapter h2::after, .preview .chtitle::after { content: none; }
.chapter h3, .preview h2 {
  font-family: -apple-system, "Helvetica Neue", sans-serif;
  font-weight: 600;
  text-align: left;
  font-size: 1.12em;
  margin: 1.8em 0 .5em;
  color: #222;
}
.chapter p, .preview p {
  text-align: left;
  hyphens: none;
  margin: 0 0 .8em;
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-family: -apple-system, "Helvetica Neue", sans-serif;
  font-size: 3.3em;
  font-weight: 700;
  float: left;
  line-height: .75;
  margin-top: .06em;
  margin-right: .12em;
  color: #111;
}`},
	{ID: "essenziale", Name: "Essenziale (Nordic Minimal)", CSS: `
.chapter, .preview {
  color: #262421;
  line-height: 1.7;
}
.chapter h2, .preview .chtitle {
  font-size: 1.35em;
  font-weight: 400;
  text-align: center;
  letter-spacing: .08em;
  text-transform: uppercase;
  margin: 2.4em auto .5em;
  color: #262421;
}
.chapter h2::after, .preview .chtitle::after {
  content: "";
  display: block;
  width: 28px;
  height: 1px;
  background: #999;
  margin: .8em auto 1.8em;
}
.chapter h3, .preview h2 {
  font-weight: normal;
  font-style: italic;
  font-size: 1.05em;
  text-align: center;
  margin: 1.8em auto .6em;
  color: #666;
}
.chapter p, .preview p {
  text-align: justify;
  hyphens: auto;
  text-indent: 1.5em;
  margin: 0 0 .5em;
}
.chapter p.noindent, .chapter h2 + p, .chapter h3 + p,
.preview p.first-para, .preview p:first-of-type {
  text-indent: 0;
}
.chapter .dropcap, .preview .dropcap {
  font-size: 3.2em;
  font-weight: normal;
  float: left;
  line-height: .78;
  margin-top: .04em;
  margin-right: .1em;
  color: #262421;
}`},
}

const commonTypographyCSS = `
/* Incipit in Maiuscoletto d'apertura (Penguin Classics / Oxford / Adelphi) */
.chapter .first-phrase, .preview .first-phrase {
  font-variant: small-caps;
  font-feature-settings: "smcp", "c2sc";
  letter-spacing: .06em;
  font-weight: 500;
}

/* Stili Front/Back Matter condivisi */
.dedication, .preview.preview-dedication {
  text-align: center !important;
  font-style: italic;
  padding-top: 18vh !important;
}
.dedication h2, .preview.preview-dedication .chtitle { display: none !important; }
.dedication p, .preview.preview-dedication p {
  text-align: center !important;
  text-indent: 0 !important;
  margin-bottom: 1em !important;
  font-size: 1.05em;
  line-height: 1.8 !important;
}

.epigraph, .preview.preview-epigraph {
  padding-top: 15vh !important;
  max-width: 80% !important;
  margin-left: auto !important;
  margin-right: auto !important;
  font-style: italic;
}
.epigraph h2, .preview.preview-epigraph .chtitle { display: none !important; }
.epigraph p, .preview.preview-epigraph p {
  text-indent: 0 !important;
  text-align: justify !important;
  margin-bottom: 0.8em !important;
}
.epigraph p:last-of-type, .preview.preview-epigraph p:last-of-type {
  text-align: right !important;
  font-style: normal !important;
  margin-top: 1.2em !important;
}

.about-author, .preview.preview-about-author {
  margin-top: 2em;
}
.about-author figure img, .preview.preview-about-author img {
  max-width: 150px;
  max-height: 150px;
  border-radius: 50%;
  display: block;
  margin: 1.2em auto;
  object-fit: cover;
  box-shadow: 0 2px 6px rgba(0,0,0,0.15);
}
`

// cssFor: CSS di un preset. "custom" = file style.css nella cartella del vault.
func cssFor(id string) (string, bool) {
	if id == "pubcraft" {
		id = "classic"
	}
	if id == "custom" {
		vDir := getVaultDir()
		if vDir != "" {
			if data, err := os.ReadFile(filepath.Join(vDir, "style.css")); err == nil {
				return string(data) + "\n" + commonTypographyCSS, true
			}
		}
		return "", false
	}
	for _, s := range stylePresets {
		if s.ID == id {
			var fontsPrefix string
			switch id {
			case "classic":
				fontsPrefix = getFontFace("Great Vibes", "GreatVibes-Regular.ttf", "normal", "400")
			case "oxford":
				fontsPrefix = getFontFace("Cormorant", "Cormorant-SemiBold.ttf", "normal", "600", "700") +
					getFontFace("Cormorant SC", "CormorantSC-SemiBold.ttf", "normal", "600", "700")
			case "meridian":
				fontsPrefix = getFontFace("Cinzel Decorative", "CinzelDecorative-Regular.ttf", "normal", "400")
			case "whisper":
				fontsPrefix = getFontFace("Mrs Saint Delafield", "MrsSaintDelafield-Regular.ttf", "normal", "400")
			case "decorum":
				fontsPrefix = getFontFace("Bodoni Moda", "BodoniModa_18pt-Medium.ttf", "normal", "400", "500", "600")
			case "directive":
				fontsPrefix = getFontFace("Montserrat", "Montserrat-Bold.ttf", "normal", "600", "700")
			case "romantico":
				fontsPrefix = getFontFace("Great Vibes", "GreatVibes-Regular.ttf", "normal", "400")
			case "editoriale":
				fontsPrefix = getFontFace("Cormorant", "Cormorant-SemiBold.ttf", "normal", "600", "700")
			}
			return fontsPrefix + s.CSS + "\n" + commonTypographyCSS, true
		}
	}
	return "", false
}

func apiStyles(w http.ResponseWriter, r *http.Request) {
	list := make([]map[string]string, 0, len(stylePresets)+1)
	for _, s := range stylePresets {
		list = append(list, map[string]string{"id": s.ID, "name": s.Name})
	}
	if vDir := getVaultDir(); vDir != "" && fileExists(filepath.Join(vDir, "style.css")) {
		list = append(list, map[string]string{"id": "custom", "name": "Personalizzato (style.css nel vault)"})
	}
	writeJSON(w, map[string]any{"styles": list})
}

func apiStyleCSS(w http.ResponseWriter, r *http.Request) {
	css, ok := cssFor(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "stile sconosciuto", 404)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(css))
}

func apiSetStyle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Style string `json:"style"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	if req.Style != "" && req.Style != "classico" {
		if _, ok := cssFor(req.Style); !ok {
			jsonErr(w, 400, "Stile sconosciuto: "+req.Style)
			return
		}
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b.Style = req.Style
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

func apiSetOpening(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Opening string `json:"opening"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	switch req.Opening {
	case "", "dropcap", "smallcaps", "none":
		// ok
	default:
		jsonErr(w, 400, "Stile di apertura non valido: "+req.Opening)
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b.Opening = req.Opening
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Aggiornamento book.yaml fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

func apiSetTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	newTitle := strings.TrimSpace(req.Title)
	if newTitle == "" {
		jsonErr(w, 400, "Il titolo non può essere vuoto")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b := loadBook()
	if b == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	b.Title = newTitle
	if err := writeBookYAML(b); err != nil {
		jsonErr(w, 500, "Salvataggio book.yaml fallito: "+err.Error())
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()
	updateRecentTitle(vDir, newTitle)
	writeJSON(w, map[string]any{"ok": true, "book": b})
}

type NoteInfo struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Title     string `json:"title"`
	Preview   string `json:"preview"`
	UpdatedAt int64  `json:"updatedAt"`
}

func getNotesDir() (string, error) {
	vDir := getVaultDir()
	if vDir == "" {
		return "", fmt.Errorf("nessun libro aperto")
	}
	nd := filepath.Join(vDir, "notes")
	if err := os.MkdirAll(nd, 0o755); err != nil {
		return "", err
	}
	return nd, nil
}

func parseNoteContent(raw string, fallbackFileName string) (string, string) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Riconosciamo il blocco frontmatter solo se inizia con riga isolata "---"
	if strings.HasPrefix(text, "---\n") || text == "---" {
		lines := strings.Split(text, "\n")
		closingIdx := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], " \t") == "---" {
				closingIdx = i
				break
			}
		}

		if closingIdx != -1 {
			var title string
			for _, line := range lines[1:closingIdx] {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "title:") {
					val := strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
					title = cleanYAMLVal(val)
					break
				}
			}
			if title == "" {
				base := strings.TrimSuffix(fallbackFileName, filepath.Ext(fallbackFileName))
				title = strings.TrimSpace(base)
			}
			body := strings.Join(lines[closingIdx+1:], "\n")
			body = strings.TrimPrefix(body, "\n")
			body = strings.TrimRight(body, "\r\n")
			return title, body
		}
	}

	// Nessun frontmatter valido con riga di chiusura "---"
	base := strings.TrimSuffix(fallbackFileName, filepath.Ext(fallbackFileName))
	title := strings.TrimSpace(base)
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "# ") {
			t := strings.TrimSpace(strings.TrimPrefix(l, "# "))
			if t != "" {
				title = t
				break
			}
		}
	}
	return title, strings.TrimRight(text, "\r\n")
}

func formatNoteContent(title, body string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "Nota senza titolo"
	}
	titleYAML := yamlScalar(t)
	b := strings.TrimSpace(body)
	if b == "" {
		return fmt.Sprintf("---\ntitle: %s\n---\n\n", titleYAML)
	}
	return fmt.Sprintf("---\ntitle: %s\n---\n\n%s\n", titleYAML, b)
}

func apiListNotes(w http.ResponseWriter, r *http.Request) {
	nd, err := getNotesDir()
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	entries, err := os.ReadDir(nd)
	if err != nil {
		writeJSON(w, map[string]any{"notes": []NoteInfo{}})
		return
	}
	var notes []NoteInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		filePath := filepath.Join(nd, e.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		raw := string(data)
		noteTitle, previewText := parseNoteContent(raw, e.Name())
		previewText = strings.ReplaceAll(previewText, "\n", " ")
		if len(previewText) > 80 {
			previewText = previewText[:80] + "…"
		}
		notes = append(notes, NoteInfo{
			ID:        e.Name(),
			File:      e.Name(),
			Title:     noteTitle,
			Preview:   previewText,
			UpdatedAt: info.ModTime().Unix(),
		})
	}
	sort.Slice(notes, func(i, j int) bool {
		return notes[i].UpdatedAt > notes[j].UpdatedAt
	})
	writeJSON(w, map[string]any{"notes": notes})
}

func apiGetNote(w http.ResponseWriter, r *http.Request) {
	fileName := filepath.Base(strings.TrimSpace(r.URL.Query().Get("file")))
	if fileName == "" || !strings.HasSuffix(fileName, ".md") {
		jsonErr(w, 400, "Nome file nota non valido")
		return
	}
	nd, err := getNotesDir()
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	filePath := filepath.Join(nd, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		jsonErr(w, 404, "Nota non trovata: "+fileName)
		return
	}
	raw := string(data)
	title, body := parseNoteContent(raw, fileName)
	writeJSON(w, map[string]any{
		"file":    fileName,
		"title":   title,
		"content": body,
		"version": chapterVersion(data),
	})
}

func apiSaveNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File        string `json:"file"`
		Title       string `json:"title"`
		Content     string `json:"content"`
		BaseVersion string `json:"base_version"`
		Force       bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	nd, err := getNotesDir()
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Nota senza titolo"
	}
	fileName := filepath.Base(strings.TrimSpace(req.File))
	isNew := false
	if fileName == "" || fileName == "." || !strings.HasSuffix(fileName, ".md") {
		isNew = true
		slug := slugify(title)
		if slug == "" {
			slug = "nota"
		}
		fileName = slug + ".md"
		for i := 2; fileExists(filepath.Join(nd, fileName)); i++ {
			fileName = fmt.Sprintf("%s-%d.md", slug, i)
		}
	}
	filePath := filepath.Join(nd, fileName)

	// Controllo conflitti di concorrenza se il file esiste già
	if !isNew && fileExists(filePath) {
		current, err := os.ReadFile(filePath)
		if err == nil {
			curVer := chapterVersion(current)
			if !req.Force && req.BaseVersion != "" && curVer != req.BaseVersion {
				jsonErrWithDiag(w, http.StatusConflict, "La nota è stata modificata da un'altra scheda o da un editor esterno.", "note", "NOTE_CONFLICT", "apiSaveNote")
				return
			}
		}
	}

	fullText := formatNoteContent(title, req.Content)
	dataBytes := []byte(fullText)
	if err := atomicWriteFile(filePath, dataBytes, 0o644); err != nil {
		jsonErr(w, 500, "Salvataggio nota fallito: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"file":    fileName,
		"title":   title,
		"content": req.Content,
		"version": chapterVersion(dataBytes),
	})
}

func apiDeleteNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	fileName := filepath.Base(strings.TrimSpace(req.File))
	if fileName == "" || !strings.HasSuffix(fileName, ".md") {
		jsonErr(w, 400, "Nome file nota non valido")
		return
	}
	nd, err := getNotesDir()
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	filePath := filepath.Join(nd, fileName)
	_ = os.Remove(filePath)
	writeJSON(w, map[string]any{"ok": true})
}

func apiNoteToChapter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File     string `json:"file"`
		KeepNote bool   `json:"keepNote"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "Richiesta non valida")
		return
	}
	fileName := filepath.Base(strings.TrimSpace(req.File))
	if fileName == "" || !strings.HasSuffix(fileName, ".md") {
		jsonErr(w, 400, "Nome file nota non valido")
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	nd := filepath.Join(vDir, "notes")
	notePath := filepath.Join(nd, fileName)
	data, err := os.ReadFile(notePath)
	if err != nil {
		jsonErr(w, 404, "Nota non trovata: "+fileName)
		return
	}
	noteTitle, body := parseNoteContent(string(data), fileName)
	book := loadBook()
	if book == nil {
		jsonErr(w, 400, "Nessun libro aperto")
		return
	}
	chNum := len(book.Chapters) + 1
	slug := slugify(noteTitle)
	if slug == "" {
		slug = fmt.Sprintf("capitolo-%d", chNum)
	}
	chFile := fmt.Sprintf("%02d-%s.md", chNum, slug)
	for i := 2; fileExists(filepath.Join(vDir, chFile)); i++ {
		chFile = fmt.Sprintf("%02d-%s-%d.md", chNum, slug, i)
	}
	chUUID, _ := newUUID()
	chContent := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s\n", chUUID, yamlScalar(noteTitle), body)
	if err := atomicWriteFile(filepath.Join(vDir, chFile), []byte(chContent), 0o644); err != nil {
		jsonErr(w, 500, "Creazione capitolo fallita: "+err.Error())
		return
	}
	book.Chapters = append(book.Chapters, Chapter{
		File:  chFile,
		ID:    chUUID,
		Title: noteTitle,
		Type:  "chapter",
	})
	if err := writeBookYAML(book); err != nil {
		jsonErr(w, 500, "Salvataggio book.yaml fallito: "+err.Error())
		return
	}
	invalidateVaultFingerprint()
	broadcastWatchChange()

	if !req.KeepNote {
		_ = os.Remove(notePath)
	}
	writeJSON(w, map[string]any{
		"ok":           true,
		"book":         book,
		"chapterFile":  chFile,
		"chapterTitle": noteTitle,
	})
}

// injectStyle appende l'override CSS del preset dentro print.html (sostituendo stili precedenti).
// injectCSS: inserisce/aggiorna un blocco <style> marcato (id). Usato per
// stile del libro e font del corpo: a ogni build il blocco precedente con
// lo stesso marcatore viene rimosso e reinserito.
func injectCSS(htmlPath, markerID, css string) {
	data, err := os.ReadFile(htmlPath)
	if err != nil || strings.TrimSpace(css) == "" {
		return
	}
	s := string(data)
	tag := "<!-- pubcraft-css: " + markerID + " -->"
	if i := strings.Index(s, tag); i >= 0 {
		if end := strings.Index(s[i:], "</style>"); end >= 0 {
			s = s[:i] + strings.TrimLeft(s[i+end+len("</style>"):], "\r\n \t")
		}
	}
	block := tag + "\n<style>\n" + css + "\n</style>\n</head>"
	s = strings.Replace(s, "</head>", block, 1)
	_ = os.WriteFile(htmlPath, []byte(s), 0o644)
}

// stripInjectedCSS rimuove TUTTI i blocchi iniettati da build precedenti
// (qualunque stile/font): evita l'accumulo e i conflitti tra fogli.
func stripInjectedCSS(htmlPath string) {
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		return
	}
	s := string(data)
	for _, prefix := range []string{"<!-- pubcraft-css: ", "<!-- pubcraft-style: "} {
		for {
			i := strings.Index(s, prefix)
			if i < 0 {
				break
			}
			end := strings.Index(s[i:], "</style>")
			if end < 0 {
				// blocco malformato: taglio fino a fine commento
				if j := strings.Index(s[i:], "-->"); j >= 0 {
					s = s[:i] + s[i+j+3:]
					continue
				}
				break
			}
			s = s[:i] + strings.TrimLeft(s[i+end+len("</style>"):], "\r\n \t")
		}
	}
	_ = os.WriteFile(htmlPath, []byte(s), 0o644)
}

func injectStyle(htmlPath, id, css string) {
	injectCSS(htmlPath, "style-"+id, css)
}

func apiReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Path) == "" {
		jsonErr(w, 400, "Percorso mancante")
		return
	}
	revealPath(req.Path)
	writeJSON(w, map[string]bool{"ok": true})
}

func apiOpenPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Path) == "" {
		jsonErr(w, 400, "Percorso mancante")
		return
	}
	if err := openPath(req.Path); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func apiViewPrint(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		http.Error(w, "Nessun vault aperto", 404)
		return
	}
	lp := getLastPrintHTML(vDir)
	if lp == "" || !fileExists(lp) {
		cand := filepath.Join(loadPrintConfig(vDir, nil).OutDir, "print.html")
		if fileExists(cand) {
			lp = cand
		} else {
			http.Error(w, "Nessun file print.html generato per questo progetto. Compila prima da Stampa / PDF.", 404)
			return
		}
	}
	// lp is registered by this project's build (or its persisted manifest).
	// The output directory may legitimately be outside the vault.
	dir := filepath.Dir(lp)
	rel := strings.TrimPrefix(r.URL.Path, "/api/viewprint")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "index.html" || rel == "print.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, lp)
		return
	}
	target := filepath.Join(dir, filepath.Clean(rel))
	if tRel, err := filepath.Rel(dir, target); err != nil || strings.HasPrefix(tRel, "..") {
		http.Error(w, "Accesso negato", 403)
		return
	}
	http.ServeFile(w, r, target)
}

func apiObsidian(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, 400, "Apri prima una cartella libro")
		return
	}
	if err := obsidianCmd().Run(); err != nil {
		if err2 := openPath(vDir); err2 != nil {
			jsonErr(w, 500, "Non riesco ad aprire la cartella")
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ------------------------------------------------ apertura neutra per piattaforma

func sanitizeLinuxEnv(cmd *exec.Cmd) {
	if runtime.GOOS != "linux" {
		return
	}
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "LD_LIBRARY_PATH=") ||
			strings.HasPrefix(e, "LD_PRELOAD=") ||
			strings.HasPrefix(e, "PYTHONPATH=") ||
			strings.HasPrefix(e, "PERLLIB=") {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = filtered
}

// openPath apre un file con l'applicazione predefinita del sistema.
func openPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Run()
	default: // linux e altri unix
		cmd := exec.Command("xdg-open", path)
		sanitizeLinuxEnv(cmd)
		if err := cmd.Start(); err == nil {
			return nil
		}
		for _, alt := range []string{"gio", "gnome-open", "kde-open", "sensible-browser", "x-www-browser", "firefox", "chromium"} {
			var altCmd *exec.Cmd
			if alt == "gio" {
				altCmd = exec.Command("gio", "open", path)
			} else {
				altCmd = exec.Command(alt, path)
			}
			sanitizeLinuxEnv(altCmd)
			if err := altCmd.Start(); err == nil {
				return nil
			}
		}
		return errors.New("impossibile aprire il percorso (nessun browser o gestore file trovato)")
	}
}

// revealPath mostra il file nel file manager (Finder su macOS).
func revealPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	case "windows":
		return exec.Command("explorer", "/select,", path).Run()
	default:
		cmd := exec.Command("xdg-open", filepath.Dir(path))
		sanitizeLinuxEnv(cmd)
		return cmd.Start()
	}
}

func obsidianCmd() *exec.Cmd {
	vDir := getVaultDir()
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-a", "Obsidian", vDir)
	}
	// Linux/Windows: URI Obsidian
	u := "obsidian://open?path=" + strings.ReplaceAll(vDir, "\\", "/")
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	}
	cmd := exec.Command("xdg-open", u)
	sanitizeLinuxEnv(cmd)
	return cmd
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'à':
			b.WriteByte('a')
		case r == 'è', r == 'é':
			b.WriteByte('e')
		case r == 'ì':
			b.WriteByte('i')
		case r == 'ò':
			b.WriteByte('o')
		case r == 'ù':
			b.WriteByte('u')
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "capitolo"
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func newUUID() (string, error) {
	var b [16]byte
	randRead(&b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := fmt.Sprintf("id%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return strings.ToLower(h), nil
}

func randRead(b *[16]byte) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
		return
	}
	defer f.Close()
	f.Read(b[:])
}

func findTool() string {
	if p := os.Getenv("PUBCRAFT_BIN"); p != "" {
		return p
	}
	if p := os.Getenv("PUBCRAFTC"); p != "" {
		return p
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	binNames := []string{"pubcraft"}
	if runtime.GOOS == "windows" {
		binNames = []string{"pubcraft.exe", "pubcraft"}
	}
	for _, name := range binNames {
		for _, c := range []string{
			filepath.Join(dir, name),
			filepath.Join(dir, "build", name),
			filepath.Join(dir, "..", "build", name),
			filepath.Join(dir, "..", "..", "build", name),
			filepath.Join(dir, "..", "MacOS", name), // bundle macOS (.app/Contents/MacOS)
			"/usr/local/bin/" + name,
			"/usr/bin/" + name,
		} {
			if p, err := filepath.Abs(c); err == nil && fileExists(p) {
				return p
			}
		}
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// -------------------------------------------------------------------- triggerPrintBuild

// -------------------------------------------------------------------- main

func apiDiagStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "Metodo non consentito")
		return
	}
	writeJSON(w, GetDiagStatusSummary())
}

func apiDiagEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "Metodo non consentito")
		return
	}
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	comp := r.URL.Query().Get("component")
	events := GetRecentDiagEvents(limit, comp)
	writeJSON(w, map[string]interface{}{"events": events})
}

func apiDiagReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "Metodo non consentito")
		return
	}
	var req struct {
		ErrorIDs    []string `json:"error_ids"`
		Description string   `json:"description"`
		Expected    string   `json:"expected"`
		Observed    string   `json:"observed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "Richiesta non valida")
		return
	}
	rep, err := PrepareBugReport(req.ErrorIDs, req.Description, req.Expected, req.Observed)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, rep)
}

func apiDiagExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "Metodo non consentito")
		return
	}
	var req struct {
		ReportID    string `json:"report_id"`
		Destination string `json:"destination"`
		Overwrite   bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ReportID) == "" {
		jsonErr(w, http.StatusBadRequest, "Report ID mancante")
		return
	}
	path, err := ExportBugReport(req.ReportID, req.Destination, req.Overwrite)
	if err != nil {
		if strings.Contains(err.Error(), "OUTPUT_EXISTS") {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "path": path})
}

type clientDirtyEntry struct {
	Dirty       bool      `json:"dirty"`
	Description string    `json:"description"`
	Vault       string    `json:"vault"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const clientHeartbeatTimeout = 6 * time.Second

var (
	clientDirtyMu sync.Mutex
	dirtyClients  = make(map[string]clientDirtyEntry)
)

func getAnyClientDirty() (bool, string, string) {
	clientDirtyMu.Lock()
	defer clientDirtyMu.Unlock()
	now := time.Now()
	for id, st := range dirtyClients {
		if now.Sub(st.UpdatedAt) > clientHeartbeatTimeout {
			delete(dirtyClients, id)
		}
	}
	for _, st := range dirtyClients {
		if st.Dirty {
			return true, st.Description, st.Vault
		}
	}
	return false, "", ""
}

func apiClientDirty(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		dirty, desc, vault := getAnyClientDirty()
		writeJSON(w, map[string]interface{}{"dirty": dirty, "description": desc, "vault": vault})
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			ClientID    string `json:"client_id"`
			Dirty       bool   `json:"dirty"`
			Description string `json:"description"`
			Vault       string `json:"vault"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, http.StatusBadRequest, "Richiesta non valida")
			return
		}
		cid := strings.TrimSpace(req.ClientID)
		if cid == "" {
			cid = "default"
		}
		clientDirtyMu.Lock()
		if req.Dirty {
			dirtyClients[cid] = clientDirtyEntry{
				Dirty:       true,
				Description: req.Description,
				Vault:       req.Vault,
				UpdatedAt:   time.Now(),
			}
		} else {
			delete(dirtyClients, cid)
		}
		clientDirtyMu.Unlock()
		writeJSON(w, map[string]interface{}{"ok": true})
		return
	}
	jsonErr(w, http.StatusMethodNotAllowed, "Metodo non consentito")
}

func main() {
	args := os.Args[1:]
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "--mcp" {
			RunMCPServer()
			return
		}
		if a == "--watch" || a == "-w" {
			watchMode = true
			continue
		}
		// Freedesktop .desktop spec: %u, %U, %f, %F o stringhe vuote
		if a == "" || a == "%u" || a == "%U" || a == "%f" || a == "%F" {
			continue
		}
		clean := a
		if strings.HasPrefix(clean, "file://") {
			clean = strings.TrimPrefix(clean, "file://")
			if u, err := url.PathUnescape(clean); err == nil {
				clean = u
			}
		}
		if fileExists(clean) {
			setVault(clean)
		}
	}
	if vDir := getVaultDir(); vDir != "" {
		if abs, err := filepath.Abs(vDir); err == nil {
			setVault(abs)
		}
	}
	// Indirizzo di ascolto: di default solo localhost (App locale).
	// Per un server: PUBCRAFTGUI_LISTEN=0.0.0.0:8080 (vedi README, sezione server).
	listenAddr := os.Getenv("PUBCRAFTGUI_LISTEN")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	cleanOrphanedPreviewDirs()
	defer cleanAllPreviews()
	toolPath = findTool()

	startVaultWatcher()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", apiState)
	mux.HandleFunc("/api/vault-fingerprint", apiVaultFingerprint)
	mux.HandleFunc("/api/newproject", apiNewProject)
	mux.HandleFunc("/api/openat", apiOpenAt)
	mux.HandleFunc("/api/mkdir", apiMkdir)
	mux.HandleFunc("/api/fs", apiFS)
	mux.HandleFunc("/api/import", apiImport)
	mux.HandleFunc("/api/covers", apiCovers)
	mux.HandleFunc("/api/addcover", apiAddCover)
	mux.HandleFunc("/api/delcover", apiDelCover)
	mux.HandleFunc("/api/covermedia", apiCoverMedia)
	mux.HandleFunc("/api/opencover", apiOpenCover)
	mux.HandleFunc("/api/useasepubcover", apiUseAsEpubCover)
	mux.HandleFunc("/api/styles", apiStyles)
	mux.HandleFunc("/api/stylecss", apiStyleCSS)
	mux.HandleFunc("/api/setstyle", apiSetStyle)
	mux.HandleFunc("/api/fontcss", apiFontCSS)
	mux.HandleFunc("/api/bodyfonts", apiBodyFonts)
	mux.HandleFunc("/api/setfont", apiSetFont)
	mux.HandleFunc("/api/scenebreakcss", apiSceneBreakCSS)
	mux.HandleFunc("/api/setscenebreak", apiSetSceneBreak)
	mux.HandleFunc("/api/stats", apiStats)
	mux.HandleFunc("/api/importdocx", apiImportDocx)
	mux.HandleFunc("/api/import-manuscript", apiImportManuscript)
	mux.HandleFunc("/api/setopening", apiSetOpening)
	mux.HandleFunc("/api/uploadimage", apiUploadImage)
	mux.HandleFunc("/api/recents", apiRecents)
	mux.HandleFunc("/api/recents/remove", apiRemoveRecent)
	mux.HandleFunc("/api/recents/clear", apiClearRecents)
	mux.HandleFunc("/api/recents/prune", apiPruneRecents)
	mux.HandleFunc("/api/workspace", apiSetWorkspace)
	mux.HandleFunc("/api/workspace/books", apiWorkspaceBooks)
	mux.HandleFunc("/api/recents/cover", apiRecentCover)
	mux.HandleFunc("/api/settitle", apiSetTitle)
	mux.HandleFunc("/api/notes", apiListNotes)
	mux.HandleFunc("/api/note", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			apiSaveNote(w, r)
		} else {
			apiGetNote(w, r)
		}
	})
	mux.HandleFunc("/api/delnote", apiDeleteNote)
	mux.HandleFunc("/api/note-to-chapter", apiNoteToChapter)
	mux.HandleFunc("/api/closebook", apiCloseBook)
	mux.HandleFunc("/api/diagnostics/status", apiDiagStatus)
	mux.HandleFunc("/api/diagnostics/events", apiDiagEvents)
	mux.HandleFunc("/api/diagnostics/report", apiDiagReport)
	mux.HandleFunc("/api/diagnostics/export", apiDiagExport)
	mux.HandleFunc("/api/client-dirty", apiClientDirty)

	// Watch Mode endpoints
	mux.HandleFunc("/api/watch/events", apiWatchEvents)
	mux.HandleFunc("/api/watch/book", apiWatchBook)
	mux.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(webFS, "web/watch.html")
		if err != nil {
			http.Error(w, "watch.html non trovato", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';")
		w.Write(data)
	})

	// Immagini del vault (per l'anteprima dei capitoli): sempre dinamiche,
	// perché vaultDir cambia quando si apre un altro libro.
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		vDir := getVaultDir()
		if vDir == "" {
			http.NotFound(w, r)
			return
		}
		name := filepath.Base(r.URL.Path)
		http.ServeFile(w, r, filepath.Join(vDir, "images", name))
	})
	mux.HandleFunc("/api/chapter", apiChapter)
	mux.HandleFunc("/api/save", apiSave)
	mux.HandleFunc("/api/newchapter", apiNewChapter)
	mux.HandleFunc("/api/reorder", apiReorder)
	mux.HandleFunc("/api/deletechapter", apiDeleteChapter)
	mux.HandleFunc("/api/build", apiBuild)
	mux.HandleFunc("/api/publication-preview", apiPublicationPreview)
	mux.HandleFunc("/api/publication-preview/", apiPublicationPreviewRoute)
	mux.HandleFunc("/api/printconfig", apiPrintConfig)
	mux.HandleFunc("/api/reveal", apiReveal)
	mux.HandleFunc("/api/openpath", apiOpenPath)
	mux.HandleFunc("/api/viewprint", apiViewPrint)
	mux.HandleFunc("/api/viewprint/", apiViewPrint)
	mux.HandleFunc("/api/obsidian", apiObsidian)

	// Endpoint PDF per Live Watch e anteprima reale
	mux.HandleFunc("/api/watch/pdf", apiWatchPDF)

	// Statici incorporati: /static/* → web/static/*.
	static, _ := fs.Sub(webFS, "web/static")
	fileServer := http.FileServer(http.FS(static))
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		http.StripPrefix("/static/", fileServer).ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(webFS, "web/index.html")
		if err != nil {
			http.Error(w, "index missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(data)
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Impossibile avviare il server:", err)
		os.Exit(1)
	}
	port := 0
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	if port > 0 && (strings.HasPrefix(url, "http://0.0.0.0") || strings.HasPrefix(url, "http://[::]")) {
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	if err := writeInstanceDescriptor(port, url); err != nil {
		fmt.Fprintf(os.Stderr, "Avviso: impossibile salvare descrittore istanza: %v\n", err)
	}
	defer removeInstanceDescriptor()
	fmt.Println("PubCraft GUI:", url)
	vDir := getVaultDir()
	if vDir != "" {
		fmt.Println("Vault:", vDir)
		if watchMode {
			fmt.Printf("Modalità Live Watch attiva: %s/watch\n", url)
		}
	}
	if toolPath == "" {
		fmt.Println("Nota: pubcraft non trovato — le azioni di pubblicazione sono disattivate.")
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		// Apriamo il browser solo per ascolti locali (su un server remoto
		// non c'è nessuna interfaccia grafica: apri l'URL nel tuo browser).
		if isLocalListen(ln.Addr().String()) && os.Getenv("PUBCRAFTGUI_NO_BROWSER") != "1" {
			targetURL := url
			if watchMode && getVaultDir() != "" {
				targetURL = url + "/watch"
			}
			openPath(targetURL)
		}
	}()
	fmt.Fprintln(os.Stderr, "Ctrl-C per uscire.")
	srv := &http.Server{
		Handler: securityMiddleware(projectGuard(mux)),
	}
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		removeInstanceDescriptor()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
	removeInstanceDescriptor()
	cleanAllPreviews()
}

func isAllowedHost(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		if sh, _, err := net.SplitHostPort(h); err == nil {
			h = sh
		}
	}
	h = strings.TrimSpace(strings.ToLower(h))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" || h == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Controllo Host contro DNS Rebinding
		if !isAllowedHost(r.Host) {
			http.Error(w, "Accesso rifiutato: host non consentito", http.StatusForbidden)
			return
		}

		// 2. Controllo Origin contro CSRF
		origin := r.Header.Get("Origin")
		if origin != "" {
			if u, err := url.Parse(origin); err == nil {
				if !isAllowedHost(u.Host) || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "Accesso rifiutato: origine non consentita", http.StatusForbidden)
					return
				}
			} else {
				http.Error(w, "Origine non valida", http.StatusForbidden)
				return
			}
		}

		// 3. Controllo Metodo e Content-Type per endpoint di modifica
		mutatingPrefixes := []string{
			"/api/save", "/api/newproject", "/api/openat", "/api/mkdir",
			"/api/import", "/api/addcover", "/api/delcover", "/api/useasepubcover",
			"/api/setstyle", "/api/setfont", "/api/setscenebreak", "/api/setopening",
			"/api/importdocx", "/api/import-manuscript", "/api/newchapter", "/api/reorder", "/api/deletechapter",
			"/api/settitle", "/api/note", "/api/delnote", "/api/note-to-chapter",
			"/api/build", "/api/obsidian", "/api/openpath", "/api/opencover", "/api/reveal",
		}
		isMutating := false
		for _, p := range mutatingPrefixes {
			if r.URL.Path == p {
				if p == "/api/note" && r.Method != http.MethodPost {
					continue
				}
				isMutating = true
				break
			}
		}

		if isMutating {
			if r.Method != http.MethodPost {
				http.Error(w, "Metodo non consentito: richiesto POST", http.StatusMethodNotAllowed)
				return
			}
			rawCt := r.Header.Get("Content-Type")
			mediaType, _, _ := mime.ParseMediaType(rawCt)
			if mediaType != "application/json" && mediaType != "multipart/form-data" {
				http.Error(w, "Content-Type non valido: richiesto application/json", http.StatusUnsupportedMediaType)
				return
			}
		}

		// 4. Header protettivi
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")

		next.ServeHTTP(w, r)
	})
}

func isLocalListen(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	ip := net.ParseIP(host)
	return ip == nil || ip.IsLoopback()
}
