package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecentsPersistenceAndEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PUBCRAFT_CONFIG_DIR", tmpDir)

	// Inizialmente configurazione vuota
	cfg := loadUserConfig()
	if len(cfg.Recents) != 0 {
		t.Fatalf("Atteso 0 recenti, trovato %d", len(cfg.Recents))
	}

	// Crea un libro fittizio A
	bookADir := filepath.Join(tmpDir, "Libro A")
	_ = os.MkdirAll(filepath.Join(bookADir, "cover"), 0o755)
	_ = os.WriteFile(filepath.Join(bookADir, "book.yaml"), []byte("title: \"Il metabolismo tradito\"\nauthor: \"Dott. Rossi\"\nstyle: \"classic\"\nchapters:\n- file: 01.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(bookADir, "01.md"), []byte("# Cap 1\nTesto"), 0o644)
	_ = os.WriteFile(filepath.Join(bookADir, "cover", "copertina.png"), []byte("FAKEPNGDATA"), 0o644)

	// Registra libro A
	recordRecentProject(bookADir)

	cfg = loadUserConfig()
	if len(cfg.Recents) != 1 {
		t.Fatalf("Atteso 1 recente, trovato %d", len(cfg.Recents))
	}
	if cfg.Recents[0].Title != "Il metabolismo tradito" {
		t.Errorf("Titolo non corrispondente: %s", cfg.Recents[0].Title)
	}
	if cfg.Recents[0].Author != "Dott. Rossi" {
		t.Errorf("Autore non corrispondente: %s", cfg.Recents[0].Author)
	}
	if !cfg.Recents[0].HasCover {
		t.Errorf("HasCover dovrebbe essere true")
	}

	// Crea libro B
	bookBDir := filepath.Join(tmpDir, "Libro B")
	_ = os.MkdirAll(bookBDir, 0o755)
	_ = os.WriteFile(filepath.Join(bookBDir, "book.yaml"), []byte("title: \"Zuccheri ribelli\"\nauthor: \"Autore B\"\nstyle: \"modern\"\n"), 0o644)

	recordRecentProject(bookBDir)
	cfg = loadUserConfig()
	if len(cfg.Recents) != 2 {
		t.Fatalf("Atteso 2 recenti, trovato %d", len(cfg.Recents))
	}
	// Il più recente deve essere in posizione 0
	if cfg.Recents[0].Title != "Zuccheri ribelli" {
		t.Errorf("Libro B dovrebbe essere in cima, trovato: %s", cfg.Recents[0].Title)
	}

	// Riapertura di libro A -> deve risalire in cima
	recordRecentProject(bookADir)
	cfg = loadUserConfig()
	if len(cfg.Recents) != 2 {
		t.Fatalf("Atteso ancora 2 recenti, trovato %d", len(cfg.Recents))
	}
	if cfg.Recents[0].Title != "Il metabolismo tradito" {
		t.Errorf("Libro A dovrebbe essere risalito in cima, trovato: %s", cfg.Recents[0].Title)
	}

	// Test API GET /api/recents
	reqRecents := httptest.NewRequest(http.MethodGet, "/api/recents", nil)
	rrRecents := httptest.NewRecorder()
	apiRecents(rrRecents, reqRecents)
	if rrRecents.Code != http.StatusOK {
		t.Fatalf("apiRecents status atteso 200, trovato %d", rrRecents.Code)
	}
	var resRecents struct {
		Recents []RecentProject `json:"recents"`
	}
	if err := json.Unmarshal(rrRecents.Body.Bytes(), &resRecents); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if len(resRecents.Recents) != 2 {
		t.Fatalf("Attesi 2 recenti da API, trovati %d", len(resRecents.Recents))
	}

	// Test API GET /api/recents/cover
	reqCover := httptest.NewRequest(http.MethodGet, "/api/recents/cover?path="+url.QueryEscape(bookADir), nil)
	rrCover := httptest.NewRecorder()
	apiRecentCover(rrCover, reqCover)
	if rrCover.Code != http.StatusOK {
		t.Fatalf("apiRecentCover status atteso 200, trovato %d", rrCover.Code)
	}
	if rrCover.Body.String() != "FAKEPNGDATA" {
		t.Fatalf("Dati copertina non corrispondenti: %s", rrCover.Body.String())
	}

	// Test API POST /api/workspace
	reqWs := httptest.NewRequest(http.MethodPost, "/api/workspace", bytes.NewBufferString(`{"path":"`+tmpDir+`"}`))
	rrWs := httptest.NewRecorder()
	apiSetWorkspace(rrWs, reqWs)
	if rrWs.Code != http.StatusOK {
		t.Fatalf("apiSetWorkspace status atteso 200, trovato %d", rrWs.Code)
	}
	cfg = loadUserConfig()
	if cfg.WorkspaceDir != tmpDir {
		t.Errorf("WorkspaceDir atteso %s, trovato %s", tmpDir, cfg.WorkspaceDir)
	}

	// Test API GET /api/workspace/books
	reqWsBooks := httptest.NewRequest(http.MethodGet, "/api/workspace/books?path="+tmpDir, nil)
	rrWsBooks := httptest.NewRecorder()
	apiWorkspaceBooks(rrWsBooks, reqWsBooks)
	if rrWsBooks.Code != http.StatusOK {
		t.Fatalf("apiWorkspaceBooks status atteso 200, trovato %d", rrWsBooks.Code)
	}
	var resWsBooks struct {
		Books []RecentProject `json:"books"`
	}
	if err := json.Unmarshal(rrWsBooks.Body.Bytes(), &resWsBooks); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if len(resWsBooks.Books) < 2 {
		t.Fatalf("Attesi almeno 2 libri nella cartella di lavoro, trovati %d", len(resWsBooks.Books))
	}

	// Test API POST /api/recents/remove
	reqRemove := httptest.NewRequest(http.MethodPost, "/api/recents/remove", bytes.NewBufferString(`{"path":"`+bookBDir+`"}`))
	rrRemove := httptest.NewRecorder()
	apiRemoveRecent(rrRemove, reqRemove)
	if rrRemove.Code != http.StatusOK {
		t.Fatalf("apiRemoveRecent status atteso 200, trovato %d", rrRemove.Code)
	}
	cfg = loadUserConfig()
	if len(cfg.Recents) != 1 {
		t.Fatalf("Atteso 1 recente dopo rimozione, trovati %d", len(cfg.Recents))
	}
	if cfg.Recents[0].Path != bookADir {
		t.Errorf("Rimosso elemento errato, rimasto: %s", cfg.Recents[0].Path)
	}

	// Test API POST /api/recents/prune
	missingDir := filepath.Join(tmpDir, "Libro Inesistente")
	cfgPre := loadUserConfig()
	cfgPre.Recents = append(cfgPre.Recents, RecentProject{
		Path:  missingDir,
		Title: "Non Esiste",
	})
	userConfigMu.Lock()
	_ = saveUserConfigUnlocked(cfgPre)
	userConfigMu.Unlock()

	reqPrune := httptest.NewRequest(http.MethodPost, "/api/recents/prune", nil)
	rrPrune := httptest.NewRecorder()
	apiPruneRecents(rrPrune, reqPrune)
	if rrPrune.Code != http.StatusOK {
		t.Fatalf("apiPruneRecents status atteso 200, trovato %d", rrPrune.Code)
	}
	cfg = loadUserConfig()
	if len(cfg.Recents) != 1 || cfg.Recents[0].Path != bookADir {
		t.Fatalf("Atteso solo bookADir dopo prune, trovati %d: %+v", len(cfg.Recents), cfg.Recents)
	}

	// Test API POST /api/closebook
	setVault(bookADir)
	if getVaultDir() != bookADir {
		t.Fatalf("Vault non impostato")
	}
	reqClose := httptest.NewRequest(http.MethodPost, "/api/closebook", nil)
	rrClose := httptest.NewRecorder()
	apiCloseBook(rrClose, reqClose)
	if rrClose.Code != http.StatusOK {
		t.Fatalf("apiCloseBook status atteso 200, trovato %d", rrClose.Code)
	}
	if getVaultDir() != "" {
		t.Fatalf("Vault deve essere vuoto dopo closebook, trovato: %s", getVaultDir())
	}
}

func TestRecentsRaceDetector(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PUBCRAFT_CONFIG_DIR", tmpDir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bDir := filepath.Join(tmpDir, "book")
			_ = os.MkdirAll(bDir, 0o755)
			_ = os.WriteFile(filepath.Join(bDir, "book.yaml"), []byte("title: T\n"), 0o644)
			recordRecentProject(bDir)
			_ = loadUserConfig()
			if id%2 == 0 {
				_ = setWorkspaceDirectory(tmpDir)
			}
		}(i)
	}
	wg.Wait()
}
