package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestImportSelectedFiles(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "SelezioneCapitoli")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Creiamo 4 file di capitolo
	files := map[string]string{
		"01-intro.md":  "# Introduzione\nTesto intro.",
		"02-cap1.md":   "# Capitolo 1\nTesto cap 1.",
		"03-cap2.md":   "# Capitolo 2\nTesto cap 2.",
		"04-bozza.md":  "# Bozza Scartata\nNon includere.",
	}
	for fn, c := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, fn), []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// L'utente seleziona solo 01-intro.md e 03-cap2.md (usando Cmd/Ctrl)
	selected := []string{"03-cap2.md", "01-intro.md"}
	book, err := importSelectedFiles(tmpDir, selected)
	if err != nil {
		t.Fatalf("importSelectedFiles fallito: %v", err)
	}

	if len(book.Chapters) != 2 {
		t.Fatalf("attesi esattamente 2 capitoli selezionati, trovati %d", len(book.Chapters))
	}

	// Verifica ordinamento naturale
	if book.Chapters[0].File != "01-intro.md" || book.Chapters[1].File != "03-cap2.md" {
		t.Errorf("ordine o file inattesi: %+v", book.Chapters)
	}

	// Verifica che 04-bozza.md NON sia presente nel libro
	for _, ch := range book.Chapters {
		if ch.File == "04-bozza.md" {
			t.Errorf("il file escluso 04-bozza.md non doveva comparire nel libro")
		}
	}
}

func TestDiagnosticsRegistryAndDeduplication(t *testing.T) {
	// Registra due errori identici
	ev1 := RecordDiagError("save", "SAVE_FAILED", "apiSave", "Errore disco su /Users/test/Documents/libro", "Dettagli 1", "req-1")
	if ev1.ErrorID == "" {
		t.Fatal("error_id vuoto")
	}
	if strings.Contains(ev1.Message, "/Users/test") {
		t.Errorf("il messaggio non è stato sanitizzato: %s", ev1.Message)
	}

	ev2 := RecordDiagError("save", "SAVE_FAILED", "apiSave", "Errore disco su /Users/test/Documents/libro", "Dettagli 1", "req-2")
	if ev2.ErrorID != ev1.ErrorID {
		t.Errorf("attesa deduplicazione stesso error_id, ottenuti %s e %s", ev1.ErrorID, ev2.ErrorID)
	}
	if ev2.Count < 2 {
		t.Errorf("atteso count >= 2, ottenuto %d", ev2.Count)
	}

	// Verifica riassunto di stato
	summary := GetDiagStatusSummary()
	if summary["recent_errors_count"].(int) < 1 {
		t.Errorf("atteso recent_errors_count >= 1")
	}
	ids := summary["recent_error_ids"].([]string)
	if len(ids) == 0 {
		t.Errorf("atteso almeno un error_id recente")
	}
}

func TestPrepareAndExportBugReport(t *testing.T) {
	ev := RecordDiagError("export", "PRINT_BUILD_FAILED", "buildPrint", "Impossibile completare PDF", "Dettagli errore", "req-build-99")

	rep, err := PrepareBugReport([]string{ev.ErrorID}, "La compilazione PDF si blocca", "Generazione print.pdf", "Errore restituito")
	if err != nil {
		t.Fatalf("PrepareBugReport fallito: %v", err)
	}

	if rep.ReportID == "" {
		t.Fatal("report_id vuoto")
	}
	if !strings.Contains(rep.MarkdownContent, rep.ReportID) {
		t.Errorf("markdown non contiene report_id: %s", rep.MarkdownContent)
	}
	if !strings.Contains(rep.MarkdownContent, ev.ErrorID) {
		t.Errorf("markdown non contiene l'ID errore: %s", rep.MarkdownContent)
	}
	if !strings.Contains(rep.MarkdownContent, "La compilazione PDF si blocca") {
		t.Errorf("markdown non contiene la descrizione utente: %s", rep.MarkdownContent)
	}

	// Esportazione su file
	tmpExport := filepath.Join(t.TempDir(), "mio-report.md")
	exportedPath, err := ExportBugReport(rep.ReportID, tmpExport, false)
	if err != nil {
		t.Fatalf("ExportBugReport fallito: %v", err)
	}
	if exportedPath != tmpExport {
		t.Errorf("percorso esportato atteso %s, ottenuto %s", tmpExport, exportedPath)
	}
	content, err := os.ReadFile(tmpExport)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != rep.MarkdownContent {
		t.Errorf("il contenuto del file esportato non coincide con il rapporto")
	}

	// Protezione OUTPUT_EXISTS senza overwrite
	_, err = ExportBugReport(rep.ReportID, tmpExport, false)
	if err == nil || !strings.Contains(err.Error(), "OUTPUT_EXISTS") {
		t.Fatalf("atteso errore OUTPUT_EXISTS su file già esistente, ottenuto: %v", err)
	}

	// Sovrascrittura consentita con overwrite=true
	_, err = ExportBugReport(rep.ReportID, tmpExport, true)
	if err != nil {
		t.Fatalf("ExportBugReport con overwrite=true fallito: %v", err)
	}
}

func TestDiagnosticsHTTPEndpoints(t *testing.T) {
	// 1. GET /api/diagnostics/status
	req := httptest.NewRequest("GET", "/api/diagnostics/status", nil)
	w := httptest.NewRecorder()
	apiDiagStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiDiagStatus status %d: %s", w.Code, w.Body.String())
	}
	var st map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st["recent_errors_count"]; !ok {
		t.Errorf("recent_errors_count mancante nello status")
	}

	// 2. POST /api/diagnostics/report
	body := strings.NewReader(`{"description":"Problema di test","expected":"Ok","observed":"Errore"}`)
	req = httptest.NewRequest("POST", "/api/diagnostics/report", body)
	w = httptest.NewRecorder()
	apiDiagReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiDiagReport status %d: %s", w.Code, w.Body.String())
	}
	var rep BugReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.ReportID == "" {
		t.Fatal("report_id vuoto da endpoint HTTP")
	}
}

func TestMCPServerHandlers(t *testing.T) {
	// Test handleMCPRequest per protocollo JSON-RPC 2.0 stdio
	var outBuf bytes.Buffer
	writer := bufio.NewWriter(&outBuf)

	// 1. Initialize
	initReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	handleMCPRequest(writer, initReq)

	var initResp mcpResponse
	line, err := outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(line), &initResp); err != nil {
		t.Fatal(err)
	}
	if initResp.Error != nil {
		t.Fatalf("errore in initialize: %v", initResp.Error)
	}

	// 2. tools/list
	outBuf.Reset()
	listReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	handleMCPRequest(writer, listReq)
	line, err = outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &listResp); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range listResp.Result.Tools {
		names[tool.Name] = true
	}
	for _, expectedTool := range []string{
		"pubcraft_get_status",
		"pubcraft_get_diagnostics",
		"pubcraft_prepare_bug_report",
		"pubcraft_export_bug_report",
		"pubcraft_open_book",
	} {
		if !names[expectedTool] {
			t.Errorf("tool atteso mancante in tools/list: %s", expectedTool)
		}
	}

	// 3. tools/call: pubcraft_get_status
	outBuf.Reset()
	callReq := &mcpRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"pubcraft_get_status","arguments":{}}`),
	}
	handleMCPRequest(writer, callReq)
	line, err = outBuf.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var callResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &callResp); err != nil {
		t.Fatal(err)
	}
	if callResp.Result.IsError {
		t.Errorf("pubcraft_get_status ha restituito errore")
	}
	if len(callResp.Result.Content) == 0 || !strings.Contains(callResp.Result.Content[0].Text, "app_version") {
		t.Errorf("contenuto inatteso da pubcraft_get_status: %+v", callResp.Result.Content)
	}
}

func TestFolderAndMultiFileImportEndpoints(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "TestImportBookFolder")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"01-intro.md": "# Introduzione\nTesto introduttivo.",
		"02-cap1.md":  "# Capitolo Uno\nPrimo capitolo.",
		"03-cap2.md":  "# Capitolo Due\nSecondo capitolo.",
	}
	for fn, c := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, fn), []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Importa l'intera cartella (senza specificare singoli file)
	reqBody := strings.NewReader(`{"dir":"` + filepath.ToSlash(tmpDir) + `"}`)
	req := httptest.NewRequest("POST", "/api/import-manuscript", reqBody)
	w := httptest.NewRecorder()
	apiImportManuscript(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiImportManuscript cartella fallito status %d: %s", w.Code, w.Body.String())
	}
	var st State
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Book == nil || len(st.Book.Chapters) != 3 {
		t.Fatalf("attesi tutti e 3 i capitoli dall'intera cartella, trovati: %+v", st.Book)
	}

	// 2. Seleziona solo alcuni file con Cmd/Ctrl (selezione parziale).
	// La cartella è già un libro: la selezione NON deve ridurre la lista
	// capitoli né alterare ordine, ID, titoli o tipi esistenti.
	beforeIDs := make([]string, len(st.Book.Chapters))
	for i, c := range st.Book.Chapters {
		beforeIDs[i] = c.ID
	}
	reqBody = strings.NewReader(`{"dir":"` + filepath.ToSlash(tmpDir) + `","files":["01-intro.md","03-cap2.md"]}`)
	req = httptest.NewRequest("POST", "/api/import-manuscript", reqBody)
	w = httptest.NewRecorder()
	apiImportManuscript(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiImportManuscript selezione multipla fallito status %d: %s", w.Code, w.Body.String())
	}
	var stMulti State
	if err := json.Unmarshal(w.Body.Bytes(), &stMulti); err != nil {
		t.Fatal(err)
	}
	if stMulti.Book == nil || len(stMulti.Book.Chapters) != 3 {
		t.Fatalf("la selezione parziale non deve eliminare capitoli: trovati: %+v", stMulti.Book)
	}
	for i, want := range []string{"01-intro.md", "02-cap1.md", "03-cap2.md"} {
		if stMulti.Book.Chapters[i].File != want {
			t.Fatalf("ordine capitoli alterato in posizione %d: %+v", i, stMulti.Book.Chapters)
		}
		if stMulti.Book.Chapters[i].ID != beforeIDs[i] {
			t.Fatalf("ID capitolo alterato per %s: %q -> %q", want, beforeIDs[i], stMulti.Book.Chapters[i].ID)
		}
	}
}

func TestDiagnosticsConcurrentRace(t *testing.T) {
	const goroutines = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Goroutine di scrittura / deduplicazione eventi
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				code := fmt.Sprintf("CODE_%d", i%3)
				msg := fmt.Sprintf("Errore concorrente g=%d i=%d", id, i)
				RecordDiagError("test", code, "opTest", msg, "dettagli", fmt.Sprintf("req-%d", id))
			}
		}(g)
	}

	// Goroutine di lettura / serializzazione JSON e generazione report
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				events := GetRecentDiagEvents(20, "")
				_ = events
				summary := GetDiagStatusSummary()
				_ = summary
				if i%10 == 0 {
					_, _ = PrepareBugReport(nil, "Descrizione concorrente", "Atteso", "Osservato")
				}
			}
		}(g)
	}

	wg.Wait()
}
