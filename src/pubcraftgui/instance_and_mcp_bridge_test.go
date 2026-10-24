package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestInstanceDescriptorLifecycle(t *testing.T) {
	tmpConfig := t.TempDir()
	t.Setenv("PUBCRAFT_CONFIG_DIR", tmpConfig)

	port := 54321
	url := "http://127.0.0.1:54321"

	// 1. Scrittura del descrittore
	if err := writeInstanceDescriptor(port, url); err != nil {
		t.Fatalf("writeInstanceDescriptor fallito: %v", err)
	}

	descPath := filepath.Join(tmpConfig, "instance.json")
	st, err := os.Stat(descPath)
	if err != nil {
		t.Fatalf("instance.json non trovato: %v", err)
	}

	// Permessi 0600
	perm := st.Mode().Perm()
	if perm != 0600 {
		t.Errorf("permessi attesi 0600 (-rw-------), ottenuti %04o", perm)
	}

	// Contenuto valido
	data, err := os.ReadFile(descPath)
	if err != nil {
		t.Fatal(err)
	}
	var inst GUIInstance
	if err := json.Unmarshal(data, &inst); err != nil {
		t.Fatalf("JSON non valido in instance.json: %v", err)
	}
	if inst.PID != os.Getpid() {
		t.Errorf("PID errato: atteso %d, ottenuto %d", os.Getpid(), inst.PID)
	}
	if inst.Port != port {
		t.Errorf("Port errata: attesa %d, ottenuta %d", port, inst.Port)
	}
	if inst.URL != url {
		t.Errorf("URL errato: atteso %s, ottenuto %s", url, inst.URL)
	}
	if inst.StartedAt == "" {
		t.Errorf("started_at vuoto")
	}

	// 2. removeInstanceDescriptor con PID diverso NON deve rimuovere il file
	fakeInst := inst
	fakeInst.PID = os.Getpid() + 999999
	fakeData, _ := json.Marshal(fakeInst)
	_ = os.WriteFile(descPath, fakeData, 0600)

	removeInstanceDescriptor()
	if _, err := os.Stat(descPath); os.IsNotExist(err) {
		t.Errorf("il descrittore è stato rimosso anche se il PID non corrispondeva")
	}

	// 3. removeInstanceDescriptor con PID corrente deve rimuovere il file
	_ = os.WriteFile(descPath, data, 0600)
	removeInstanceDescriptor()
	if _, err := os.Stat(descPath); !os.IsNotExist(err) {
		t.Errorf("il descrittore non è stato rimosso dopo removeInstanceDescriptor")
	}
}

func TestMCPBridgeToActiveGUI(t *testing.T) {
	tmpConfig := t.TempDir()
	t.Setenv("PUBCRAFT_CONFIG_DIR", tmpConfig)

	var mu sync.Mutex
	currentVault := "/tmp/gui-test-vault"
	currentTitle := "Il Mio Libro Nella GUI"
	currentChapters := []string{"Capitolo 1", "Capitolo 2", "Capitolo 3", "Capitolo 4", "Capitolo 5", "Capitolo 6", "Capitolo 7"}

	// Server mock che simula la GUI HTTP attiva
	guiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/state":
			var chs []map[string]interface{}
			for i, ch := range currentChapters {
				chs = append(chs, map[string]interface{}{
					"id":    strconv.Itoa(i + 1),
					"title": ch,
					"file":  ch + ".md",
				})
			}
			st := map[string]interface{}{
				"vault": currentVault,
				"book": map[string]interface{}{
					"title":    currentTitle,
					"chapters": chs,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(st)

		case "/api/diagnostics/status":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"session_errors":       0,
				"has_critical_issue":   false,
				"recent_errors_count":  0,
			})

		case "/api/diagnostics/events":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"events": []map[string]interface{}{
					{
						"error_id":  "test-event-1",
						"severity":  "info",
						"component": "gui",
						"message":   "GUI attiva in esecuzione",
					},
				},
			})

		case "/api/openat":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "richiesta non valida"})
				return
			}
			currentVault = req.Path
			currentTitle = filepath.Base(req.Path)
			currentChapters = []string{"Capitolo Nuovo 1", "Capitolo Nuovo 2"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"vault": currentVault,
				"book": map[string]interface{}{
					"title": currentTitle,
					"chapters": []map[string]string{
						{"title": "Capitolo Nuovo 1"},
						{"title": "Capitolo Nuovo 2"},
					},
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer guiServer.Close()

	// 1. Scrivi descrittore istanza per la GUI mock (PID = os.Getpid())
	inst := GUIInstance{
		PID:       os.Getpid(),
		Port:      12345,
		URL:       guiServer.URL,
		StartedAt: "2026-10-23T12:00:00Z",
	}
	instData, _ := json.Marshal(inst)
	_ = os.WriteFile(filepath.Join(tmpConfig, "instance.json"), instData, 0600)

	// 2. Chiamata MCP: pubcraft_get_status
	var outBuf bytes.Buffer
	writer := bufio.NewWriter(&outBuf)

	statusReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"pubcraft_get_status","arguments":{}}`),
	}
	handleMCPToolCall(writer, statusReq)

	var statusResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	line, err := outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(line), &statusResp); err != nil {
		t.Fatalf("errore unmarshal risposta MCP: %v", err)
	}
	if statusResp.Result.IsError {
		t.Fatalf("pubcraft_get_status ha restituito errore: %s", statusResp.Result.Content[0].Text)
	}

	var statusMap map[string]interface{}
	if err := json.Unmarshal([]byte(statusResp.Result.Content[0].Text), &statusMap); err != nil {
		t.Fatalf("testo non è JSON valido: %v", err)
	}

	// Verifica che i dati provengano dalla GUI attiva!
	if statusMap["vault"] != "/tmp/gui-test-vault" {
		t.Errorf("vault errato: atteso /tmp/gui-test-vault, ottenuto %v", statusMap["vault"])
	}
	if statusMap["book_title"] != "Il Mio Libro Nella GUI" {
		t.Errorf("book_title errato: atteso 'Il Mio Libro Nella GUI', ottenuto %v", statusMap["book_title"])
	}
	if count, ok := statusMap["chapters_count"].(float64); !ok || int(count) != 7 {
		t.Errorf("chapters_count errato: atteso 7, ottenuto %v", statusMap["chapters_count"])
	}
	if connected, ok := statusMap["gui_connected"].(bool); !ok || !connected {
		t.Errorf("gui_connected atteso true, ottenuto %v", statusMap["gui_connected"])
	}

	// 3. Chiamata MCP: pubcraft_get_diagnostics
	outBuf.Reset()
	diagReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"pubcraft_get_diagnostics","arguments":{"limit":10}}`),
	}
	handleMCPToolCall(writer, diagReq)
	line, err = outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var diagResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(line), &diagResp)
	if !strings.Contains(diagResp.Result.Content[0].Text, "test-event-1") {
		t.Errorf("diagnostica inoltrata non contiene evento atteso: %s", diagResp.Result.Content[0].Text)
	}

	// 4. Chiamata MCP: pubcraft_open_book
	outBuf.Reset()
	openReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"pubcraft_open_book","arguments":{"path":"/tmp/nuovo-manoscritto"}}`),
	}
	handleMCPToolCall(writer, openReq)
	line, err = outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var openResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(line), &openResp)
	if openResp.Result.IsError {
		t.Fatalf("pubcraft_open_book ha restituito errore: %s", openResp.Result.Content[0].Text)
	}
	if !strings.Contains(openResp.Result.Content[0].Text, "Libro aperto con successo nella GUI") {
		t.Errorf("messaggio inatteso da pubcraft_open_book: %s", openResp.Result.Content[0].Text)
	}

	// 5. Verifica che pubcraft_get_status ora veda il nuovo libro
	outBuf.Reset()
	handleMCPToolCall(writer, statusReq)
	line, _ = outBuf.ReadString('\n')
	_ = json.Unmarshal([]byte(line), &statusResp)
	_ = json.Unmarshal([]byte(statusResp.Result.Content[0].Text), &statusMap)
	if statusMap["vault"] != "/tmp/nuovo-manoscritto" {
		t.Errorf("vault dopo apertura errato: atteso /tmp/nuovo-manoscritto, ottenuto %v", statusMap["vault"])
	}
	if count, ok := statusMap["chapters_count"].(float64); !ok || int(count) != 2 {
		t.Errorf("chapters_count dopo apertura errato: atteso 2, ottenuto %v", statusMap["chapters_count"])
	}

	// 6. Rimuovi descrittore e verifica fallback headless
	_ = os.Remove(filepath.Join(tmpConfig, "instance.json"))
	outBuf.Reset()
	handleMCPToolCall(writer, statusReq)
	line, _ = outBuf.ReadString('\n')
	statusResp.Result.Content = nil
	_ = json.Unmarshal([]byte(line), &statusResp)
	statusMap = nil
	_ = json.Unmarshal([]byte(statusResp.Result.Content[0].Text), &statusMap)
	if _, ok := statusMap["gui_connected"]; ok {
		t.Errorf("gui_connected non dovrebbe essere presente in fallback headless: %v", statusMap["gui_connected"])
	}
}

func TestMultiTabClientDirtyIsolationAndMCPBlock(t *testing.T) {
	// Pulisci lo stato dirty prima del test
	clientDirtyMu.Lock()
	dirtyClients = make(map[string]clientDirtyEntry)
	clientDirtyMu.Unlock()

	// 1. Tab 1 ha modifiche sporche nel vault A
	body1 := `{"client_id": "tab-1", "dirty": true, "vault": "/vault/a", "description": "capitolo non salvato"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/client-dirty", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	apiClientDirty(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("apiClientDirty tab-1 fallito: %d", w1.Code)
	}

	dirty, desc, vault := getAnyClientDirty()
	if !dirty || vault != "/vault/a" || !strings.Contains(desc, "capitolo non salvato") {
		t.Fatalf("getAnyClientDirty dopo tab-1 errato: dirty=%v, vault=%s, desc=%s", dirty, vault, desc)
	}

	// 2. Tab 2 apre la pagina ed è pulita (dirty: false)
	body2 := `{"client_id": "tab-2", "dirty": false, "vault": "/vault/a", "description": ""}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/client-dirty", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	apiClientDirty(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("apiClientDirty tab-2 fallito: %d", w2.Code)
	}

	// 3. Verifica isolamento: Tab 2 NON deve aver annullato il blocco di Tab 1!
	dirty, desc, vault = getAnyClientDirty()
	if !dirty || vault != "/vault/a" {
		t.Fatalf("FALLITO: Tab 2 ha annullato il blocco di Tab 1! dirty=%v, vault=%s", dirty, vault)
	}

	// 4. Tentativo di aprire un altro libro tramite apiOpenAt deve essere bloccato con 409
	openReq := httptest.NewRequest(http.MethodPost, "/api/openat", strings.NewReader(`{"path": "/vault/b"}`))
	openReq.Header.Set("Content-Type", "application/json")
	wOpen := httptest.NewRecorder()
	apiOpenAt(wOpen, openReq)
	if wOpen.Code != http.StatusConflict {
		t.Fatalf("apiOpenAt non ha bloccato l'apertura con modifiche pendenti di Tab 1! Codice: %d, body: %s", wOpen.Code, wOpen.Body.String())
	}

	// 5. Tab 1 salva e notifica dirty: false
	body1Clean := `{"client_id": "tab-1", "dirty": false, "vault": "/vault/a", "description": ""}`
	req1Clean := httptest.NewRequest(http.MethodPost, "/api/client-dirty", strings.NewReader(body1Clean))
	req1Clean.Header.Set("Content-Type", "application/json")
	w1Clean := httptest.NewRecorder()
	apiClientDirty(w1Clean, req1Clean)

	dirty, _, _ = getAnyClientDirty()
	if dirty {
		t.Fatalf("getAnyClientDirty dopo pulizia tab-1 deve essere false, ottenuto %v", dirty)
	}
}
