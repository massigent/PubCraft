package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBookTitleInPlaceUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	setVault(tmpDir)

	bk := &Book{
		Title:    "Vecchio Titolo",
		Language: "it",
		Chapters: []Chapter{
			{File: "01.md", ID: "id1", Title: "Cap 1"},
		},
	}
	if err := writeBookYAML(bk); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"title":"Nuovo Titolo del Libro"}`)
	req := httptest.NewRequest("POST", "/api/settitle", body)
	w := httptest.NewRecorder()

	apiSetTitle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status atteso 200, ottenuto %d: %s", w.Code, w.Body.String())
	}

	updated := loadBook()
	if updated.Title != "Nuovo Titolo del Libro" {
		t.Errorf("titolo atteso 'Nuovo Titolo del Libro', ottenuto %q", updated.Title)
	}

	// Test rifiuto titolo vuoto
	bodyEmpty := strings.NewReader(`{"title":"   "}`)
	reqEmpty := httptest.NewRequest("POST", "/api/settitle", bodyEmpty)
	wEmpty := httptest.NewRecorder()
	apiSetTitle(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusBadRequest {
		t.Errorf("atteso 400 per titolo vuoto, ottenuto %d", wEmpty.Code)
	}
}

func TestChapterTitleInPlaceUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	setVault(tmpDir)

	bk := &Book{
		Title:    "Mio Libro",
		Language: "it",
		Chapters: []Chapter{
			{File: "01.md", ID: "id1", Title: "Vecchio Capitolo"},
		},
	}
	if err := writeBookYAML(bk); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmpDir, "01.md"), []byte("---\ntitle: \"Vecchio Capitolo\"\n---\n\nTesto qui"), 0644)

	body := strings.NewReader(`{"file":"01.md","title":"Nuovo Capitolo Riscritto"}`)
	req := httptest.NewRequest("POST", "/api/setchaptertitle", body)
	w := httptest.NewRecorder()

	apiSetChapterTitle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status atteso 200, ottenuto %d: %s", w.Code, w.Body.String())
	}

	updated := loadBook()
	if updated.Chapters[0].Title != "Nuovo Capitolo Riscritto" {
		t.Errorf("titolo capitolo atteso 'Nuovo Capitolo Riscritto', ottenuto %q", updated.Chapters[0].Title)
	}

	// Verifica aggiornamento frontmatter nel file markdown
	content, _ := os.ReadFile(filepath.Join(tmpDir, "01.md"))
	if !strings.Contains(string(content), `title: "Nuovo Capitolo Riscritto"`) {
		t.Errorf("frontmatter non aggiornato nel file: %s", string(content))
	}
}

func TestNotebookLifecycleAndChapterConversion(t *testing.T) {
	tmpDir := t.TempDir()
	setVault(tmpDir)

	bk := &Book{
		Title:    "Libro con Taccuino",
		Language: "it",
		Chapters: []Chapter{
			{File: "01-cap.md", ID: "id1", Title: "Capitolo 1"},
		},
	}
	if err := writeBookYAML(bk); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmpDir, "01-cap.md"), []byte("Testo cap 1"), 0644)

	// 1. Salva nuova nota nel taccuino
	noteBody := strings.NewReader(`{"title":"Idee per il capitolo 2","content":"Appunto segreto da ricordare."}`)
	reqNote := httptest.NewRequest("POST", "/api/note", noteBody)
	wNote := httptest.NewRecorder()
	apiSaveNote(wNote, reqNote)
	if wNote.Code != http.StatusOK {
		t.Fatalf("apiSaveNote status %d: %s", wNote.Code, wNote.Body.String())
	}

	var saveRes struct {
		Ok   bool   `json:"ok"`
		File string `json:"file"`
	}
	_ = json.Unmarshal(wNote.Body.Bytes(), &saveRes)
	if !saveRes.Ok || saveRes.File == "" {
		t.Fatalf("risposta apiSaveNote inattesa: %+v", saveRes)
	}

	// 2. Verifica che le note siano in notes/ e NON nei capitoli di book.yaml
	loaded := loadBook()
	if len(loaded.Chapters) != 1 {
		t.Fatalf("i capitoli del libro non devono includere le note! Trovati %d capitoli", len(loaded.Chapters))
	}

	// 3. Elenca le note
	reqList := httptest.NewRequest("GET", "/api/notes", nil)
	wList := httptest.NewRecorder()
	apiListNotes(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("apiListNotes status %d: %s", wList.Code, wList.Body.String())
	}
	var listRes struct {
		Notes []NoteInfo `json:"notes"`
	}
	_ = json.Unmarshal(wList.Body.Bytes(), &listRes)
	if len(listRes.Notes) != 1 || listRes.Notes[0].Title != "Idee per il capitolo 2" {
		t.Fatalf("elenco note errato: %+v", listRes)
	}

	// 4. Trasforma la nota in capitolo (azione esplicita richiesta)
	convBody := strings.NewReader(`{"file":"` + saveRes.File + `","keepNote":true}`)
	reqConv := httptest.NewRequest("POST", "/api/note-to-chapter", convBody)
	wConv := httptest.NewRecorder()
	apiNoteToChapter(wConv, reqConv)
	if wConv.Code != http.StatusOK {
		t.Fatalf("apiNoteToChapter status %d: %s", wConv.Code, wConv.Body.String())
	}

	// 5. Verifica che ora il libro abbia 2 capitoli
	bookAfter := loadBook()
	if len(bookAfter.Chapters) != 2 {
		t.Fatalf("dopo la conversione attesi 2 capitoli, trovati %d", len(bookAfter.Chapters))
	}
	ch2 := bookAfter.Chapters[1]
	if ch2.Title != "Idee per il capitolo 2" {
		t.Errorf("titolo capitolo 2 atteso 'Idee per il capitolo 2', ottenuto %q", ch2.Title)
	}

	// Contenuto del file capitolo generato
	chData, err := os.ReadFile(filepath.Join(tmpDir, ch2.File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chData), "Appunto segreto da ricordare.") {
		t.Errorf("il testo della nota non è stato trasferito nel capitolo: %s", string(chData))
	}

	// 6. Elimina la nota originale
	delBody := strings.NewReader(`{"file":"` + saveRes.File + `"}`)
	reqDel := httptest.NewRequest("POST", "/api/delnote", delBody)
	wDel := httptest.NewRecorder()
	apiDeleteNote(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("apiDeleteNote status %d: %s", wDel.Code, wDel.Body.String())
	}

	// Verifica che la nota sia stata rimossa
	reqListAfter := httptest.NewRequest("GET", "/api/notes", nil)
	wListAfter := httptest.NewRecorder()
	apiListNotes(wListAfter, reqListAfter)
	var listAfter struct {
		Notes []NoteInfo `json:"notes"`
	}
	_ = json.Unmarshal(wListAfter.Body.Bytes(), &listAfter)
	if len(listAfter.Notes) != 0 {
		t.Errorf("dopo cancellazione attese 0 note, trovate %d", len(listAfter.Notes))
	}
}

func TestNoteTitleWithDashesAndSpecialCharsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	setVault(tmpDir)

	titleSpecial := `Piano --- finale: seconda bozza con "virgolette"`
	bodyContent := "BODY_SENTINEL_DASHES\n\nRiga due con dettagli importanti."

	// 1. Salva la nota
	savePayload, _ := json.Marshal(map[string]string{
		"title":   titleSpecial,
		"content": bodyContent,
	})
	reqSave := httptest.NewRequest("POST", "/api/note", strings.NewReader(string(savePayload)))
	wSave := httptest.NewRecorder()
	apiSaveNote(wSave, reqSave)
	if wSave.Code != http.StatusOK {
		t.Fatalf("apiSaveNote status %d: %s", wSave.Code, wSave.Body.String())
	}
	var saveRes struct {
		Ok      bool   `json:"ok"`
		File    string `json:"file"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(wSave.Body.Bytes(), &saveRes)
	if !saveRes.Ok || saveRes.File == "" {
		t.Fatalf("salvataggio fallito: %+v", saveRes)
	}

	// 2. Leggi la nota con apiGetNote
	reqGet := httptest.NewRequest("GET", "/api/note?file="+saveRes.File, nil)
	wGet := httptest.NewRecorder()
	apiGetNote(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("apiGetNote status %d: %s", wGet.Code, wGet.Body.String())
	}
	var getRes struct {
		File    string `json:"file"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(wGet.Body.Bytes(), &getRes)
	if getRes.Title != titleSpecial {
		t.Errorf("titolo alterato: atteso %q, ottenuto %q", titleSpecial, getRes.Title)
	}
	if getRes.Content != bodyContent {
		t.Errorf("contenuto alterato dal frontmatter con dashes: atteso %q, ottenuto %q", bodyContent, getRes.Content)
	}
	if getRes.Version == "" {
		t.Errorf("versione mancante nella risposta di apiGetNote")
	}

	// 3. Verifica apiListNotes
	reqList := httptest.NewRequest("GET", "/api/notes", nil)
	wList := httptest.NewRecorder()
	apiListNotes(wList, reqList)
	var listRes struct {
		Notes []NoteInfo `json:"notes"`
	}
	_ = json.Unmarshal(wList.Body.Bytes(), &listRes)
	if len(listRes.Notes) != 1 || listRes.Notes[0].Title != titleSpecial {
		t.Errorf("apiListNotes titolo errato: %+v", listRes)
	}
	if !strings.Contains(listRes.Notes[0].Preview, "BODY_SENTINEL_DASHES") {
		t.Errorf("preview corrotto in apiListNotes: %q", listRes.Notes[0].Preview)
	}
}

func TestNoteConcurrencyAndConflictDetection(t *testing.T) {
	tmpDir := t.TempDir()
	setVault(tmpDir)

	// 1. Salva nota iniziale
	bodyInit := `{"file":"concurrency.md","title":"Nota Concorrenza","content":"Contenuto iniziale"}`
	reqInit := httptest.NewRequest("POST", "/api/note", strings.NewReader(bodyInit))
	wInit := httptest.NewRecorder()
	apiSaveNote(wInit, reqInit)
	if wInit.Code != http.StatusOK {
		t.Fatalf("apiSaveNote init fallito: %d", wInit.Code)
	}
	var initRes struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(wInit.Body.Bytes(), &initRes)
	baseVer := initRes.Version
	if baseVer == "" {
		t.Fatalf("versione iniziale vuota")
	}

	// 2. Tab 1 salva con successo partendo da baseVer
	bodyTab1 := fmt.Sprintf(`{"file":"concurrency.md","title":"Nota Concorrenza","content":"Modifica Tab 1","base_version":%q}`, baseVer)
	reqTab1 := httptest.NewRequest("POST", "/api/note", strings.NewReader(bodyTab1))
	wTab1 := httptest.NewRecorder()
	apiSaveNote(wTab1, reqTab1)
	if wTab1.Code != http.StatusOK {
		t.Fatalf("Tab 1 doveva salvare con successo: %d, body=%s", wTab1.Code, wTab1.Body.String())
	}
	var tab1Res struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(wTab1.Body.Bytes(), &tab1Res)
	newVer := tab1Res.Version
	if newVer == baseVer {
		t.Fatalf("la nuova versione dopo salvataggio Tab 1 non deve coincidere con la base")
	}

	// 3. Tab 2 tenta di salvare partendo ancora dalla vecchia baseVer obsoleta
	bodyTab2 := fmt.Sprintf(`{"file":"concurrency.md","title":"Nota Concorrenza","content":"Modifica Obsoleta Tab 2","base_version":%q}`, baseVer)
	reqTab2 := httptest.NewRequest("POST", "/api/note", strings.NewReader(bodyTab2))
	wTab2 := httptest.NewRecorder()
	apiSaveNote(wTab2, reqTab2)
	if wTab2.Code != http.StatusConflict {
		t.Fatalf("salvataggio obsoleto di Tab 2 doveva restituire 409 Conflict, ottenuto: %d, body: %s", wTab2.Code, wTab2.Body.String())
	}

	// 4. Verifica che su disco il file non sia stato sovrascritto
	reqGet := httptest.NewRequest("GET", "/api/note?file=concurrency.md", nil)
	wGet := httptest.NewRecorder()
	apiGetNote(wGet, reqGet)
	var curRes struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(wGet.Body.Bytes(), &curRes)
	if curRes.Content != "Modifica Tab 1" {
		t.Errorf("il file su disco è stato corrotto da Tab 2! Trovato: %q", curRes.Content)
	}

	// 5. Sovrascrittura forzata (force: true)
	bodyForce := `{"file":"concurrency.md","title":"Nota Concorrenza","content":"Modifica Forzata","force":true}`
	reqForce := httptest.NewRequest("POST", "/api/note", strings.NewReader(bodyForce))
	wForce := httptest.NewRecorder()
	apiSaveNote(wForce, reqForce)
	if wForce.Code != http.StatusOK {
		t.Fatalf("salvataggio forzato doveva avere successo: %d", wForce.Code)
	}
}

func TestDirtyClientHeartbeatExpiry(t *testing.T) {
	clientDirtyMu.Lock()
	dirtyClients = make(map[string]clientDirtyEntry)
	// Aggiungi un client attivo
	dirtyClients["active-client"] = clientDirtyEntry{
		Dirty:       true,
		Description: "modifica attiva",
		Vault:       "/vault/test",
		UpdatedAt:   time.Now(),
	}
	// Aggiungi un client fantasma/morto più di 10 secondi fa
	dirtyClients["ghost-client"] = clientDirtyEntry{
		Dirty:       true,
		Description: "client abbandonato",
		Vault:       "/vault/test",
		UpdatedAt:   time.Now().Add(-10 * time.Second),
	}
	clientDirtyMu.Unlock()

	dirty, desc, _ := getAnyClientDirty()
	if !dirty || desc != "modifica attiva" {
		t.Errorf("getAnyClientDirty errato: dirty=%v, desc=%s", dirty, desc)
	}

	clientDirtyMu.Lock()
	if _, exists := dirtyClients["ghost-client"]; exists {
		t.Errorf("ghost-client scaduto non è stato rimosso dalla mappa dirtyClients!")
	}
	// Ora scadiamo anche active-client
	entry := dirtyClients["active-client"]
	entry.UpdatedAt = time.Now().Add(-10 * time.Second)
	dirtyClients["active-client"] = entry
	clientDirtyMu.Unlock()

	dirty2, _, _ := getAnyClientDirty()
	if dirty2 {
		t.Errorf("dopo timeout di tutti i client getAnyClientDirty doveva restituire false, ottenuto true")
	}
}
