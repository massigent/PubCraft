package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RecentProject struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Style      string `json:"style"`
	HasCover   bool   `json:"has_cover"`
	CoverFile  string `json:"cover_file,omitempty"`
	LastOpened string `json:"last_opened"`
	Exists     bool   `json:"exists"`
	FolderName string `json:"folder_name"`
	ParentName string `json:"parent_name"`
}

type UserConfig struct {
	WorkspaceDir string          `json:"workspace_dir"`
	Recents      []RecentProject `json:"recents"`
}

var userConfigMu sync.Mutex

func getPubCraftConfigDir() string {
	if d := os.Getenv("PUBCRAFT_CONFIG_DIR"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	dir := filepath.Join(home, ".pubcraft")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func getUserConfigPath() string {
	return filepath.Join(getPubCraftConfigDir(), "config.json")
}

func loadUserConfig() UserConfig {
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	p := getUserConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return UserConfig{Recents: []RecentProject{}}
	}
	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{Recents: []RecentProject{}}
	}
	if cfg.Recents == nil {
		cfg.Recents = []RecentProject{}
	}
	return cfg
}

func saveUserConfigUnlocked(cfg UserConfig) error {
	p := getUserConfigPath()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func recordRecentProject(vaultPath string) {
	if vaultPath == "" {
		return
	}
	clean := filepath.Clean(vaultPath)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}

	// Ignora cartelle temporanee create durante i test automatici quando siamo nella cartella utente reale
	if os.Getenv("PUBCRAFT_CONFIG_DIR") == "" {
		if os.Getenv("PUBCRAFT_TESTING") == "1" ||
			strings.Contains(clean, "/var/folders/") ||
			strings.Contains(clean, "pubcraft-tests-") ||
			strings.Contains(clean, "pubcraft-focus-") ||
			(strings.Contains(clean, "Test") && strings.Contains(clean, "/T/")) {
			return
		}
	}

	yamlPath := filepath.Join(clean, "book.yaml")
	book, err := parseBookYAML(yamlPath)
	if err != nil || book == nil {
		return
	}

	title := book.Title
	if strings.TrimSpace(title) == "" {
		title = filepath.Base(clean)
	}
	author := book.Author
	style := book.Style
	if style == "" {
		style = "classic"
	}

	hasCover := false
	coverFile := ""
	coverDir := filepath.Join(clean, "cover")
	if entries, err := os.ReadDir(coverDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
				hasCover = true
				coverFile = e.Name()
				break
			}
		}
	}

	folderName := filepath.Base(clean)
	parentName := filepath.Base(filepath.Dir(clean))

	item := RecentProject{
		Path:       clean,
		Title:      title,
		Author:     author,
		Style:      style,
		HasCover:   hasCover,
		CoverFile:  coverFile,
		LastOpened: time.Now().UTC().Format(time.RFC3339),
		Exists:     true,
		FolderName: folderName,
		ParentName: parentName,
	}

	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	var updated []RecentProject
	updated = append(updated, item)
	for _, r := range cfg.Recents {
		if filepath.Clean(r.Path) == clean {
			continue
		}
		updated = append(updated, r)
		if len(updated) >= 30 {
			break
		}
	}
	cfg.Recents = updated
	_ = saveUserConfigUnlocked(cfg)
}

func updateRecentTitle(vaultPath, newTitle string) {
	if vaultPath == "" || newTitle == "" {
		return
	}
	clean := filepath.Clean(vaultPath)
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	for i, r := range cfg.Recents {
		if filepath.Clean(r.Path) == clean {
			cfg.Recents[i].Title = newTitle
			break
		}
	}
	_ = saveUserConfigUnlocked(cfg)
}

func removeRecentProject(vaultPath string) {
	clean := filepath.Clean(vaultPath)
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	var updated []RecentProject
	for _, r := range cfg.Recents {
		if filepath.Clean(r.Path) != clean {
			updated = append(updated, r)
		}
	}
	cfg.Recents = updated
	_ = saveUserConfigUnlocked(cfg)
}

func clearRecentProjects() {
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	cfg.Recents = []RecentProject{}
	_ = saveUserConfigUnlocked(cfg)
}

func setWorkspaceDirectory(dir string) error {
	clean := filepath.Clean(dir)
	st, err := os.Stat(clean)
	if err != nil || !st.IsDir() {
		return os.ErrNotExist
	}
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	cfg.WorkspaceDir = clean
	return saveUserConfigUnlocked(cfg)
}

func loadUserConfigInternal() UserConfig {
	p := getUserConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return UserConfig{Recents: []RecentProject{}}
	}
	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{Recents: []RecentProject{}}
	}
	if cfg.Recents == nil {
		cfg.Recents = []RecentProject{}
	}
	return cfg
}

func getQuickDirs(wsDir string) map[string]string {
	quick := make(map[string]string)
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		quick["home"] = home
		docs := filepath.Join(home, "Documents")
		if st, err := os.Stat(docs); err == nil && st.IsDir() {
			quick["documents"] = docs
		}
		desktop := filepath.Join(home, "Desktop")
		if st, err := os.Stat(desktop); err == nil && st.IsDir() {
			quick["desktop"] = desktop
		}
		downloads := filepath.Join(home, "Downloads")
		if st, err := os.Stat(downloads); err == nil && st.IsDir() {
			quick["downloads"] = downloads
		}
	}
	if wsDir != "" {
		if st, err := os.Stat(wsDir); err == nil && st.IsDir() {
			quick["workspace"] = wsDir
		}
	}
	return quick
}

func scanWorkspaceBooks(wsDir string) []RecentProject {
	books := []RecentProject{}
	if wsDir == "" {
		return books
	}
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return books
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		subPath := filepath.Join(wsDir, e.Name())
		yamlPath := filepath.Join(subPath, "book.yaml")
		b, err := parseBookYAML(yamlPath)
		if err != nil || b == nil {
			continue
		}
		title := b.Title
		if strings.TrimSpace(title) == "" {
			title = e.Name()
		}
		author := b.Author
		style := b.Style
		if style == "" {
			style = "classic"
		}
		hasCover := false
		coverFile := ""
		coverDir := filepath.Join(subPath, "cover")
		if cEntries, err := os.ReadDir(coverDir); err == nil {
			for _, ce := range cEntries {
				if ce.IsDir() {
					continue
				}
				ext := strings.ToLower(filepath.Ext(ce.Name()))
				if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
					hasCover = true
					coverFile = ce.Name()
					break
				}
			}
		}
		books = append(books, RecentProject{
			Path:       subPath,
			Title:      title,
			Author:     author,
			Style:      style,
			HasCover:   hasCover,
			CoverFile:  coverFile,
			Exists:     true,
			FolderName: e.Name(),
			ParentName: filepath.Base(wsDir),
		})
	}
	return books
}

// HTTP API Handlers

func apiRecents(w http.ResponseWriter, r *http.Request) {
	cfg := loadUserConfig()
	recents := []RecentProject{}
	for _, item := range cfg.Recents {
		item.Exists = fileExists(filepath.Join(item.Path, "book.yaml"))
		recents = append(recents, item)
	}

	ws := cfg.WorkspaceDir
	if ws == "" {
		if home, err := os.UserHomeDir(); err == nil {
			docs := filepath.Join(home, "Documents")
			if st, err := os.Stat(docs); err == nil && st.IsDir() {
				ws = docs
			} else {
				ws = home
			}
		}
	}

	writeJSON(w, map[string]any{
		"recents":       recents,
		"workspace_dir": cfg.WorkspaceDir,
		"effective_ws":  ws,
		"quick_dirs":    getQuickDirs(cfg.WorkspaceDir),
	})
}

func apiRemoveRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		jsonErr(w, 400, "Percorso mancante")
		return
	}
	removeRecentProject(req.Path)
	writeJSON(w, map[string]any{"ok": true})
}

func apiClearRecents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	clearRecentProjects()
	writeJSON(w, map[string]any{"ok": true})
}

func pruneMissingRecentProjects() int {
	userConfigMu.Lock()
	defer userConfigMu.Unlock()

	cfg := loadUserConfigInternal()
	var updated []RecentProject
	removed := 0
	for _, r := range cfg.Recents {
		clean := filepath.Clean(r.Path)
		if fileExists(filepath.Join(clean, "book.yaml")) || fileExists(clean) {
			updated = append(updated, r)
		} else {
			removed++
		}
	}
	cfg.Recents = updated
	_ = saveUserConfigUnlocked(cfg)
	return removed
}

func apiPruneRecents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	removed := pruneMissingRecentProjects()
	writeJSON(w, map[string]any{"ok": true, "removed": removed})
}

func apiSetWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		jsonErr(w, 400, "Percorso cartella mancante")
		return
	}
	p := strings.TrimSpace(req.Path)
	if err := setWorkspaceDirectory(p); err != nil {
		jsonErr(w, 400, "Cartella di lavoro non valida o non accessibile: "+p)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "workspace_dir": p})
}

func apiWorkspaceBooks(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		cfg := loadUserConfig()
		p = cfg.WorkspaceDir
	}
	if p == "" {
		writeJSON(w, map[string]any{"books": []RecentProject{}})
		return
	}
	books := scanWorkspaceBooks(p)
	writeJSON(w, map[string]any{"books": books, "workspace_dir": p})
}

func apiRecentCover(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		http.Error(w, "Percorso mancante", http.StatusBadRequest)
		return
	}
	clean := filepath.Clean(p)

	// Verifica di sicurezza: il percorso deve esistere ed essere un vault con book.yaml
	if !fileExists(filepath.Join(clean, "book.yaml")) {
		http.Error(w, "Progetto non valido", http.StatusForbidden)
		return
	}

	coverDir := filepath.Join(clean, "cover")
	entries, err := os.ReadDir(coverDir)
	if err != nil {
		http.Error(w, "Nessuna copertina", http.StatusNotFound)
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
			targetFile := filepath.Join(coverDir, e.Name())
			http.ServeFile(w, r, targetFile)
			return
		}
	}
	http.Error(w, "Nessuna copertina trovata", http.StatusNotFound)
}

func apiCloseBook(w http.ResponseWriter, r *http.Request) {
	if !requireNoDirtyClient(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	setVault("")
	st := State{Vault: ""}
	if home, err := os.UserHomeDir(); err == nil {
		st.HomeDir = home
		docs := filepath.Join(home, "Documents")
		if fileExists(docs) {
			st.DocsDir = docs
		} else {
			st.DocsDir = home
		}
	}
	writeJSON(w, st)
}
