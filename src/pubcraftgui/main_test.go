package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCleanYAMLVal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"Mio Titolo"`, "Mio Titolo"},
		{`'Altro Titolo'`, "Altro Titolo"},
		{`  "Spazi attorno"  `, "Spazi attorno"},
		{`Senza virgolette`, "Senza virgolette"},
		{`""`, ""},
	}
	for _, tc := range tests {
		actual := cleanYAMLVal(tc.input)
		if actual != tc.expected {
			t.Errorf("cleanYAMLVal(%q) = %q; atteso %q", tc.input, actual, tc.expected)
		}
	}
}

func TestParseAndWriteBookYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pubcraft-yaml-test-*")
	if err != nil {
		t.Fatalf("errore creazione tmpDir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	yamlContent := `# Commento iniziale preservato
title: "Il Nome della Rosa"
author: 'Umberto Eco'
language: "it"
style: "oxford"
opening: "smallcaps"
font: "ebgaramond"
scenebreak: "fleuron"
trim: "5.5x8.5"
pages: 350
bleed: true
isbn: "978-88-0000-000-0"
custom_meta: "valore_personalizzato"
chapters:
  - file: "cap1.md"
    id: "uuid-1"
    title: "Primo Giorno"
  - file: "cap2.md"
    id: "uuid-2"
    title: "Secondo Giorno"
`
	yamlPath := filepath.Join(tmpDir, "book.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("errore scrittura yaml: %v", err)
	}

	bk, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("parseBookYAML ha fallito: %v", err)
	}

	if bk.Title != "Il Nome della Rosa" {
		t.Errorf("Title errato: %q", bk.Title)
	}
	if bk.Author != "Umberto Eco" {
		t.Errorf("Author errato: %q", bk.Author)
	}
	if bk.Trim != "5.5x8.5" {
		t.Errorf("Trim errato: %q", bk.Trim)
	}
	if bk.Pages != 350 {
		t.Errorf("Pages errato: %d", bk.Pages)
	}
	if !bk.Bleed {
		t.Errorf("Bleed errato: atteso true")
	}
	if len(bk.Chapters) != 2 {
		t.Fatalf("Numero capitoli errato: %d", len(bk.Chapters))
	}
	if bk.Chapters[0].Title != "Primo Giorno" || bk.Chapters[0].File != "cap1.md" {
		t.Errorf("Capitolo 0 errato: %+v", bk.Chapters[0])
	}

	// Verifica campi extra preservati
	if bk.ExtraFields == nil || bk.ExtraFields["isbn"] != "978-88-0000-000-0" {
		t.Errorf("ExtraField 'isbn' mancante o errato: %v", bk.ExtraFields)
	}
	if bk.ExtraFields["custom_meta"] != "valore_personalizzato" {
		t.Errorf("ExtraField 'custom_meta' mancante o errato: %v", bk.ExtraFields)
	}

	// Ora modifichiamo il titolo e salviamo con writeBookYAML
	setVault(tmpDir)
	bk.Title = "Il Nome della Rosa (Edizione Rivista)"
	if err := writeBookYAML(bk); err != nil {
		t.Fatalf("writeBookYAML fallito: %v", err)
	}

	// Rileggiamo il file su disco
	reloaded, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("rilettura fallita: %v", err)
	}
	if reloaded.Title != "Il Nome della Rosa (Edizione Rivista)" {
		t.Errorf("Titolo non aggiornato: %q", reloaded.Title)
	}
	// Verifica che i campi extra e i capitoli non siano stati persi durante il salvataggio
	if reloaded.ExtraFields["isbn"] != "978-88-0000-000-0" {
		t.Errorf("Campo 'isbn' perso durante il salvataggio: %v", reloaded.ExtraFields)
	}
	if reloaded.ExtraFields["custom_meta"] != "valore_personalizzato" {
		t.Errorf("Campo 'custom_meta' perso durante il salvataggio: %v", reloaded.ExtraFields)
	}
	if len(reloaded.Chapters) != 2 {
		t.Errorf("Capitoli persi durante il salvataggio: %d", len(reloaded.Chapters))
	}

	// Verifica che il commento di testa sia stato preservato
	data, _ := os.ReadFile(yamlPath)
	if !strings.Contains(string(data), "# Commento iniziale preservato") {
		t.Errorf("Commento di testa rimosso da writeBookYAML: %s", string(data))
	}
}

func TestSecurityHostAndOrigin(t *testing.T) {
	allowed := []string{
		"localhost",
		"localhost:8787",
		"127.0.0.1",
		"127.0.0.1:8787",
		"::1",
		"[::1]:8787",
	}
	for _, h := range allowed {
		if !isAllowedHost(h) {
			t.Errorf("isAllowedHost(%q) = false; atteso true", h)
		}
	}

	disallowed := []string{
		"evil.com",
		"attacker.org:8787",
		"192.168.1.50",
		"10.0.0.1:8080",
		"example.com",
	}
	for _, h := range disallowed {
		if isAllowedHost(h) {
			t.Errorf("isAllowedHost(%q) = true; atteso false", h)
		}
	}
}

func TestSecurityMiddleware(t *testing.T) {
	handler := securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// 1. Host estraneo deve essere bloccato
	req := httptest.NewRequest("GET", "http://evil.com/api/state", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Host evil.com ha restituito %d invece di 403 Forbidden", w.Code)
	}

	// 2. Richiesta di modifica con Origin estranea deve essere bloccata
	req = httptest.NewRequest("POST", "http://localhost:8787/api/save", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://attacker.com")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Origin malevola ha restituito %d invece di 403 Forbidden", w.Code)
	}

	// 3. Richiesta di modifica senza Content-Type consentito (es. attacco no-cors text/plain)
	req = httptest.NewRequest("POST", "http://localhost:8787/api/save", strings.NewReader("text"))
	req.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type text/plain ha restituito %d invece di 415", w.Code)
	}

	// 4. Richiesta lecita da loopback con Content-Type application/json
	req = httptest.NewRequest("POST", "http://127.0.0.1:8787/api/save", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Richiesta valida ha restituito %d invece di 200", w.Code)
	}
}

func TestReviewYAMLPrintSettings(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pubcraft-yaml-print-*")
	if err != nil {
		t.Fatalf("tmpDir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	initialYAML := `title: Audit
language: it
trim: 5x8
pages: 750
bleed: true
chapters:
- file: 01.md
  title: Uno
`
	yamlPath := filepath.Join(tmpDir, "book.yaml")
	if err := os.WriteFile(yamlPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("write error: %v", err)
	}

	setVault(tmpDir)
	bk, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Richiedi nuove impostazioni
	bk.Trim = "6x9"
	bk.Pages = 24
	bk.Bleed = false

	if err := writeBookYAML(bk); err != nil {
		t.Fatalf("writeBookYAML error: %v", err)
	}

	raw, _ := os.ReadFile(yamlPath)
	rawStr := string(raw)

	// Controlla che le chiavi non siano duplicate
	for _, k := range []string{"trim:", "pages:", "bleed:"} {
		count := strings.Count(rawStr, k)
		if count > 1 {
			t.Errorf("Chiave %s duplicata (%d volte) nel file:\n%s", k, count, rawStr)
		}
	}

	// Rileggi il file e verifica che non torni alle vecchie impostazioni
	reloaded, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if reloaded.Trim != "6x9" {
		t.Errorf("Trim errato dopo salvataggio: %q (atteso 6x9)", reloaded.Trim)
	}
	if reloaded.Pages != 24 {
		t.Errorf("Pages errato dopo salvataggio: %d (atteso 24)", reloaded.Pages)
	}
	if reloaded.Bleed != false {
		t.Errorf("Bleed errato dopo salvataggio: %v (atteso false)", reloaded.Bleed)
	}
}

func TestBuildLocking(t *testing.T) {
	// Verifica che buildMu impedisca accessi concorrenti
	if !buildMu.TryLock() {
		t.Fatalf("Impossibile acquisire buildMu iniziale")
	}

	// Un secondo TryLock deve fallire
	if buildMu.TryLock() {
		t.Errorf("buildMu acquisito due volte contemporaneamente!")
		buildMu.Unlock()
	}

	buildMu.Unlock()

	// Dopo lo sblocco deve essere nuovamente disponibile
	if !buildMu.TryLock() {
		t.Errorf("Impossibile riacquisire buildMu dopo unlock")
	} else {
		buildMu.Unlock()
	}
}

func TestReviewStalePDF(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip()
	}
	d := t.TempDir()
	browser := filepath.Join(d, "fake-browser")
	// Il browser finto termina con successo (exit 0) ma non scrive il file temporaneo
	os.WriteFile(browser, []byte("#!/bin/sh\nexit 0\n"), 0700)
	h := filepath.Join(d, "x.html")
	p := filepath.Join(d, "x.pdf")
	os.WriteFile(h, []byte("<p>new</p>"), 0600)
	os.WriteFile(p, []byte(strings.Repeat("INVALID-OLD-FILE", 100)), 0600)

	err := htmlToPDF(browser, h, p)
	if err == nil {
		t.Errorf("htmlToPDF doveva fallire perche' il browser non ha prodotto un PDF valido, ma err == nil")
	}
}

func TestReviewLocalOrigin(t *testing.T) {
	h := securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	tests := []struct {
		method         string
		path           string
		origin         string
		ct             string
		expectedStatus int
	}{
		{"POST", "/api/save", "http://localhost:9999", "multipart/form-data; boundary=foo", http.StatusForbidden},
		{"GET", "/api/openpath", "", "", http.StatusMethodNotAllowed},
		{"POST", "/api/openpath", "", "text/plain", http.StatusUnsupportedMediaType},
		{"POST", "/api/save", "http://127.0.0.1:8787", "application/json", http.StatusOK},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:8787"+tc.path, strings.NewReader(`{"path":"x"}`))
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.ct != "" {
			r.Header.Set("Content-Type", tc.ct)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.expectedStatus {
			t.Errorf("Richiesta %+v: atteso status %d, ottenuto %d", tc, tc.expectedStatus, w.Code)
		}
	}
}

func TestReviewRaceChapter(t *testing.T) {
	d := t.TempDir()
	setVault(d)
	os.WriteFile(filepath.Join(d, "01.md"), []byte("chapter content"), 0600)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			setVault(d)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			apiChapter(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/chapter?file=01.md", nil))
		}
	}()
	wg.Wait()
}

func generateTestPDF(numPages int) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make(map[int]int)

	recordObj := func(num int) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", num)
	}

	recordObj(1)
	buf.WriteString("<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	recordObj(2)
	var kids []string
	for i := 1; i <= numPages; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", i+2))
	}
	fmt.Fprintf(&buf, "<< /Type /Pages /Count %d /Kids [%s] >>\nendobj\n", numPages, strings.Join(kids, " "))

	for i := 1; i <= numPages; i++ {
		recordObj(i + 2)
		buf.WriteString("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 432 648] >>\nendobj\n")
	}

	startXRef := buf.Len()
	totalObjs := numPages + 3
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", totalObjs)
	for i := 1; i < totalObjs; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjs, startXRef)
	return buf.Bytes()
}

func TestAuditLongPDFCount(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "long.pdf")
	if err := os.WriteFile(p, generateTestPDF(165), 0644); err != nil {
		t.Fatalf("scrittura long.pdf fallita: %v", err)
	}
	pages := countPDFPages(p)
	t.Logf("generato long.pdf a 165 pagine: countPDFPages=%d", pages)
	if pages != 165 {
		t.Errorf("wrong page count: atteso 165, ottenuto %d", pages)
	}
	if g := kdpGutter(pages); g != 0.500 {
		t.Errorf("gutter errato per 165 pagine: atteso 0.500, ottenuto %.3f", g)
	}
}

func TestAuditMalformedPDF(t *testing.T) {
	d := t.TempDir()
	browser := filepath.Join(d, "fake-browser")
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in --print-to-pdf=*) p=${a#--print-to-pdf=} ;; esac; done\nprintf '%PDF-1.4\\n/Type /Pages /Count 1\\n' > \"$p\"\nhead -c 700 /dev/zero >> \"$p\"\n"
	os.WriteFile(browser, []byte(script), 0700)
	html := filepath.Join(d, "x.html")
	os.WriteFile(html, []byte("html"), 0600)
	err := htmlToPDF(browser, html, filepath.Join(d, "bad.pdf"))
	if err == nil {
		t.Errorf("malformed non-PDF accepted erroneously")
	}
}

func TestAuditRaceLastPrintHTML(t *testing.T) {
	d := t.TempDir()
	setVault(d)
	os.WriteFile(filepath.Join(d, "book.yaml"), []byte("title: Audit\nchapters:\n- file: 01.md\n  title: One\n"), 0600)
	os.WriteFile(filepath.Join(d, "01.md"), []byte("chapter"), 0600)
	toolPath = filepath.Join(d, "fake-tool")
	os.WriteFile(toolPath, []byte("#!/bin/sh\nmkdir -p \"$3\"\nprintf '<html><head></head><body>audit</body></html>' > \"$3/print.html\"\n"), 0700)
	browser := filepath.Join(d, "fake-browser")
	os.WriteFile(browser, []byte("#!/bin/sh\nexit 1\n"), 0700)
	t.Setenv("CHROMIUM_BIN", browser)
	setLastPrintHTML(d, "")
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 4; i++ {
			body := fmt.Sprintf(`{"kind":"print","outdir":%q}`, filepath.Join(d, "out"))
			apiBuild(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/build", strings.NewReader(body)))
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				apiViewPrint(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/viewprint/", nil))
			}
		}
	}()
	wg.Wait()
}

func TestIntensiveLastPrintMuRace(t *testing.T) {
	var wg sync.WaitGroup
	const writers = 5
	const readers = 10
	const iters = 500

	wg.Add(writers + readers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				v := fmt.Sprintf("/path/to/vault_%d", id)
				setLastPrintHTML(v, fmt.Sprintf("%s/file_%d.html", v, i))
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				v := fmt.Sprintf("/path/to/vault_%d", id%writers)
				val := getLastPrintHTML(v)
				_ = val
			}
		}(r)
	}

	wg.Wait()
}

func TestValidatePDFStructureVariants(t *testing.T) {
	d := t.TempDir()
	pad1000 := strings.Repeat("% commento di padding strutturale per superare la soglia minima\n", 15)

	// 1. File senza header %PDF- (> 500 byte)
	f1 := filepath.Join(d, "noheader.pdf")
	os.WriteFile(f1, []byte("NOT_A_PDF_DOCUMENT\n"+pad1000+"startxref\n100\n%%EOF\n"), 0600)
	if _, err := validatePDFStructure(f1); err == nil || !strings.Contains(err.Error(), "magic header %PDF- mancante") {
		t.Errorf("noheader.pdf doveva fallire specificamente per magic header mancante, errore: %v", err)
	}

	// 2. File senza %%EOF (> 500 byte)
	f2 := filepath.Join(d, "noeoff.pdf")
	os.WriteFile(f2, []byte("%PDF-1.4\n1 0 obj\n<</Type /Catalog>>\nendobj\n"+pad1000), 0600)
	if _, err := validatePDFStructure(f2); err == nil || !strings.Contains(err.Error(), "%%EOF mancante o corrotto") {
		t.Errorf("noeoff.pdf doveva fallire specificamente per %%EOF mancante, errore: %v", err)
	}

	// 3. File con header ed EOF ma senza startxref (> 500 byte)
	f3 := filepath.Join(d, "nostartxref.pdf")
	os.WriteFile(f3, []byte("%PDF-1.4\n1 0 obj\n<</Type /Catalog>>\nendobj\n"+pad1000+"%%EOF\n"), 0600)
	if _, err := validatePDFStructure(f3); err == nil || !strings.Contains(err.Error(), "startxref mancante prima di %%EOF") {
		t.Errorf("nostartxref.pdf doveva fallire per startxref mancante, errore: %v", err)
	}

	// 4. File sintetico valido a 5 pagine generato al volo
	f4 := filepath.Join(d, "valid5.pdf")
	os.WriteFile(f4, generateTestPDF(5), 0600)
	pages, err := validatePDFStructure(f4)
	if err != nil || pages != 5 {
		t.Errorf("valid5.pdf doveva passare con 5 pagine, ottenuto: pages=%d, err=%v", pages, err)
	}
}

func TestYAMLCommentsAndUnknownFieldsPreservation(t *testing.T) {
	d := t.TempDir()
	yamlPath := filepath.Join(d, "book.yaml")
	rawYAML := `# Intestazione con commento iniziale
title: "Titolo Custom"
author: "Autore Prova"
# Commento relativo al layout
custom_key_x: "valore_x"
trim: 5x8
pages: 120
bleed: false
# Commento prima dei capitoli
chapters:
- file: 01.md
  title: Uno
`
	os.WriteFile(yamlPath, []byte(rawYAML), 0644)
	setVault(d)

	bk, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("parseBookYAML fallito: %v", err)
	}

	bk.Trim = "6x9"
	bk.Bleed = true
	bk.Pages = 350

	if err := writeBookYAML(bk); err != nil {
		t.Fatalf("writeBookYAML fallito: %v", err)
	}

	content, _ := os.ReadFile(yamlPath)
	s := string(content)

	if !strings.Contains(s, "# Intestazione con commento iniziale") {
		t.Errorf("Commento iniziale perso!")
	}
	if !strings.Contains(s, "custom_key_x:") || !strings.Contains(s, "valore_x") {
		t.Errorf("Campo sconosciuto custom_key_x perso! Output:\n%s", s)
	}
	if !strings.Contains(s, "trim: 6x9") {
		t.Errorf("trim non aggiornato a 6x9")
	}
	if !strings.Contains(s, "bleed: true") {
		t.Errorf("bleed non aggiornato a true")
	}
	if !strings.Contains(s, "pages: 350") {
		t.Errorf("pages non aggiornato a 350")
	}
}

func TestProjectIsolationCrossVaultBuild(t *testing.T) {
	d := t.TempDir()
	vaultA := filepath.Join(d, "vaultA")
	vaultB := filepath.Join(d, "vaultB")
	os.MkdirAll(filepath.Join(vaultA, "out-stampa"), 0755)
	os.MkdirAll(filepath.Join(vaultB, "out-stampa"), 0755)

	os.WriteFile(filepath.Join(vaultA, "book.yaml"), []byte("title: Progetto A\nchapters:\n- file: 01.md\n  title: A1\n"), 0644)
	os.WriteFile(filepath.Join(vaultA, "01.md"), []byte("# Progetto A\nContenuto A"), 0644)
	os.WriteFile(filepath.Join(vaultA, "out-stampa", "print.html"), []byte("<html><body>CONTENUTO_PROGETTO_A</body></html>"), 0644)

	os.WriteFile(filepath.Join(vaultB, "book.yaml"), []byte("title: Progetto B\nchapters:\n- file: 01.md\n  title: B1\n"), 0644)
	os.WriteFile(filepath.Join(vaultB, "01.md"), []byte("# Progetto B\nContenuto B"), 0644)

	// 1. Apri vault A
	setVault(vaultA)
	// Simula che la build di A si conclude e salva il risultato per vaultA
	setLastPrintHTML(vaultA, filepath.Join(vaultA, "out-stampa", "print.html"))

	// 2. Apri vault B
	setVault(vaultB)

	// 3. Simula che un thread in ritardo della build di A imposta l'artefatto di A
	setLastPrintHTML(vaultA, filepath.Join(vaultA, "out-stampa", "print.html"))

	// 4. Richiedi /api/viewprint/ mentre il vault attivo è B
	req := httptest.NewRequest("GET", "/api/viewprint/", nil)
	w := httptest.NewRecorder()
	apiViewPrint(w, req)

	// In nessun caso deve restituire il contenuto del progetto A!
	if w.Code == 200 && strings.Contains(w.Body.String(), "CONTENUTO_PROGETTO_A") {
		t.Fatalf("Fuga di dati cross-vault! /api/viewprint/ ha servito il contenuto di vaultA mentre vaultB era attivo!")
	}
	if w.Code != 404 {
		t.Logf("Risposta corretta: non 200 con leak, codice ricevuto=%d", w.Code)
	}
}

func TestApiSaveRevisionAndVaultProtection(t *testing.T) {
	d := t.TempDir()
	vault1 := filepath.Join(d, "v1")
	vault2 := filepath.Join(d, "v2")
	os.MkdirAll(vault1, 0755)
	os.MkdirAll(vault2, 0755)
	os.WriteFile(filepath.Join(vault1, "01.md"), []byte("initial"), 0644)
	os.WriteFile(filepath.Join(vault2, "01.md"), []byte("initial v2"), 0644)

	setVault(vault1)

	// 1. Salvataggio revisione 10
	body10 := fmt.Sprintf(`{"file":"01.md","content":"versione 10","base_version":%q,"vault":%q}`, chapterVersion([]byte("initial")), vault1)
	w10 := httptest.NewRecorder()
	apiSave(w10, httptest.NewRequest("POST", "/api/save", strings.NewReader(body10)))
	if w10.Code != 200 {
		t.Fatalf("salvataggio rev 10 fallito: code=%d, body=%s", w10.Code, w10.Body.String())
	}

	// 2. Salvataggio concorrente tardivo revisione 5 (deve essere rifiutato con 409)
	body5 := fmt.Sprintf(`{"file":"01.md","content":"versione 5 obsoleta","base_version":%q,"vault":%q}`, chapterVersion([]byte("initial")), vault1)
	w5 := httptest.NewRecorder()
	apiSave(w5, httptest.NewRequest("POST", "/api/save", strings.NewReader(body5)))
	if w5.Code != 409 {
		t.Errorf("revisione obsoleta rev 5 doveva essere rifiutata con 409, ottenuto: %d, body=%s", w5.Code, w5.Body.String())
	}

	// Verifica che su disco ci sia ancora versione 10
	data, _ := os.ReadFile(filepath.Join(vault1, "01.md"))
	if string(data) != "versione 10" {
		t.Errorf("il contenuto su disco e' stato corrotto da rev 5! Trovato: %s", string(data))
	}

	// 3. Salvataggio con vault disallineato (vault2 mentre e' attivo vault1)
	bodyWrongVault := fmt.Sprintf(`{"file":"01.md","content":"disallineato","base_version":%q,"vault":%q}`, chapterVersion([]byte("versione 10")), vault2)
	wWrong := httptest.NewRecorder()
	apiSave(wWrong, httptest.NewRequest("POST", "/api/save", strings.NewReader(bodyWrongVault)))
	if wWrong.Code != 409 {
		t.Errorf("salvataggio su vault non attivo doveva restituire 409, ottenuto: %d", wWrong.Code)
	}
}

func TestAudit4PDFParserDirect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compressed.pdf")
	if err := os.WriteFile(path, compressedPDFFixture(false, false), 0600); err != nil {
		t.Fatal(err)
	}
	pages, err := validatePDFStructure(path)
	if err != nil || pages != 1 {
		t.Fatalf("compressed PDF: pages=%d err=%v", pages, err)
	}
	if err := os.WriteFile(path, []byte("%PDF-1.4\n/Type /Catalog /Pages 2 0 R\n/Count 7\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePDFStructure(path); err == nil {
		t.Fatal("fake PDF accepted")
	}
}
