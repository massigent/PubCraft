package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestChapterVersionsReopenAndConflict(t *testing.T) {
	d := t.TempDir()
	setVault(d)
	p := filepath.Join(d, "01.md")
	os.WriteFile(p, []byte("initial"), 0600)
	read := func() string {
		w := httptest.NewRecorder()
		apiChapter(w, httptest.NewRequest("GET", "/api/chapter?file=01.md", nil))
		var r map[string]string
		json.Unmarshal(w.Body.Bytes(), &r)
		return r["version"]
	}
	save := func(base, text string) int {
		body, _ := json.Marshal(map[string]string{"file": "01.md", "vault": d, "base_version": base, "content": text})
		w := httptest.NewRecorder()
		apiSave(w, httptest.NewRequest("POST", "/api/save", bytes.NewReader(body)))
		return w.Code
	}
	initial := read()
	if initial == "" {
		t.Fatal("missing server version")
	}
	if save(initial, "one") != 200 || save(read(), "two") != 200 {
		t.Fatal("save failed")
	}
	// A fresh read, as on chapter reopen or page reload, needs no local counter.
	if save(read(), "after reopen") != 200 {
		t.Fatal("reopened chapter cannot be saved")
	}
	if save(initial, "obsolete") != 409 {
		t.Fatal("obsolete contents overwrote current file")
	}
	base := read()
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, text := range []string{"tab A", "tab B"} {
		wg.Add(1)
		go func(s string) { defer wg.Done(); codes <- save(base, s) }(text)
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		if c == 200 {
			ok++
		}
		if c == 409 {
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("CAS results success=%d conflict=%d", ok, conflict)
	}
}

func TestNewProjectRejectsPathTraversalInFolder(t *testing.T) {
	parent := t.TempDir()
	setVault("")
	for _, folder := range []string{"../escaped", `..\\escaped`, ".", ".."} {
		body, _ := json.Marshal(map[string]string{"parentDir": parent, "folder": folder, "title": "Synthetic"})
		w := httptest.NewRecorder()
		apiNewProject(w, httptest.NewRequest("POST", "/api/newproject", bytes.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("folder %q status=%d body=%s", folder, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "escaped")); !os.IsNotExist(err) {
		t.Fatal("path traversal created a directory outside selected parent")
	}
}

func TestAPIPrintConfigReflectsSavedExportSettings(t *testing.T) {
	v := t.TempDir()
	setVault(v)
	if err := os.WriteFile(filepath.Join(v, "book.yaml"), []byte("title: Print settings\nchapters:\n- file: 01.md\n  title: One\n"), 0600); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "export with #")
	cfg := printConfig{Trim: "5x8", Pages: 123, Bleed: true, OutDir: custom}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v, ".pubcraft-print.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	apiPrintConfig(w, httptest.NewRequest("GET", "/api/printconfig", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["trim"] != "5x8" || got["pages"] != float64(123) || got["bleed"] != true || got["outdir"] != custom || got["default_outdir"] != filepath.Join(v, "out-stampa") {
		t.Fatalf("unexpected print configuration: %#v", got)
	}
	w = httptest.NewRecorder()
	apiPrintConfig(w, httptest.NewRequest("POST", "/api/printconfig", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", w.Code)
	}
}

func TestPrintSettingsExternalPreviewAndWatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	os.Mkdir(v, 0755)
	setVault(v)
	yaml := "title: Regression\ntrim: 6x9\nchapters:\n- file: 01.md\n  title: One\n"
	os.WriteFile(filepath.Join(v, "book.yaml"), []byte(yaml), 0600)
	os.WriteFile(filepath.Join(v, "01.md"), []byte("original"), 0600)
	oldTool := toolPath
	t.Cleanup(func() { toolPath = oldTool })
	log := filepath.Join(root, "args.log")
	toolPath = filepath.Join(root, "tool")
	os.WriteFile(toolPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nmkdir -p \"$3\"\nprintf '<html><head></head><body>regression</body></html>' > \"$3/print.html\"\n"), 0700)
	fixture := filepath.Join(root, "fixture.pdf")
	os.WriteFile(fixture, generateTestPDF(1), 0600)
	browser := filepath.Join(root, "renderer")
	os.WriteFile(browser, []byte("#!/bin/sh\nfor a in \"$@\";do case \"$a\" in --print-to-pdf=*) cp '"+fixture+"' \"${a#--print-to-pdf=}\" ;; esac;done\n"), 0700)
	t.Setenv("CHROMIUM_BIN", browser)
	outside := filepath.Join(root, "export")
	body, _ := json.Marshal(map[string]any{"kind": "print", "trim": "5x8", "bleed": true, "outdir": outside})
	w := httptest.NewRecorder()
	apiBuild(w, httptest.NewRequest("POST", "/api/build", bytes.NewReader(body)))
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["ok"] != true {
		t.Fatalf("build failed: %s", w.Body.String())
	}
	cfg := loadPrintConfig(v, nil)
	if cfg.Trim != "5x8" || !cfg.Bleed || cfg.OutDir != outside {
		t.Fatalf("settings lost: %+v", cfg)
	}
	setLastPrintHTML(v, "") // Reload/restart: recover the registered external output.
	preview := httptest.NewRecorder()
	apiViewPrint(preview, httptest.NewRequest("GET", "/api/viewprint/", nil))
	if preview.Code != 200 {
		t.Fatal("external preview rejected", preview.Code)
	}
	before, _ := os.ReadFile(log)
	watch := httptest.NewRecorder()
	apiWatchPDF(watch, httptest.NewRequest("GET", "/api/watch/pdf", nil))
	after, _ := os.ReadFile(log)
	if watch.Code != 200 || !bytes.Equal(before, after) {
		t.Fatal("unchanged Watch rebuilt itself", watch.Code)
	}
	os.WriteFile(filepath.Join(v, "01.md"), []byte("modified content"), 0600)
	watch = httptest.NewRecorder()
	apiWatchPDF(watch, httptest.NewRequest("GET", "/api/watch/pdf", nil))
	after, _ = os.ReadFile(log)
	if watch.Code != 200 || bytes.Equal(before, after) {
		t.Fatal("Watch failed to rebuild")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(after)), "\n") {
		if !strings.Contains(line, "--trim 5x8") || !strings.Contains(line, "--bleed") {
			t.Fatalf("format drift: %s", line)
		}
	}
	got, _ := os.ReadFile(filepath.Join(v, "book.yaml"))
	if string(got) != yaml {
		// Unica modifica ammessa: l'aggiunta dell'uuid stabile (una riga).
		if !strings.HasPrefix(string(got), yaml) {
			t.Fatal("export modified user YAML")
		}
		added := strings.TrimPrefix(string(got), yaml)
		id := strings.TrimSpace(strings.TrimPrefix(added, "uuid: "))
		if !strings.HasPrefix(added, "uuid: ") || !isCanonicalUUID(id) || strings.Count(strings.TrimSpace(added), "\n") != 0 {
			t.Fatalf("unexpected YAML change: %q", added)
		}
		// Idempotenza: una seconda export non deve più toccare il file.
		before2, _ := os.ReadFile(filepath.Join(v, "book.yaml"))
		watch2 := httptest.NewRecorder()
		apiWatchPDF(watch2, httptest.NewRequest("GET", "/api/watch/pdf", nil))
		after2, _ := os.ReadFile(filepath.Join(v, "book.yaml"))
		if !bytes.Equal(before2, after2) {
			t.Fatal("second export modified YAML again")
		}
	}
	b := t.TempDir()
	setVault(b)
	preview = httptest.NewRecorder()
	apiViewPrint(preview, httptest.NewRequest("GET", "/api/viewprint/", nil))
	if preview.Code != 404 {
		t.Fatal("cross-project preview leak")
	}
}

func compressedPDFFixture(predictor, badOffset bool) []byte {
	zip := func(b []byte) []byte {
		var out bytes.Buffer
		z := zlib.NewWriter(&out)
		z.Write(b)
		z.Close()
		return out.Bytes()
	}
	objects := []string{"<< /Type /Catalog /Pages 3 0 R >>", "<< /Type /Pages /Kids [4 0 R] /Count 1 >>", "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 432 648] >>"}
	header := fmt.Sprintf("2 0 3 %d 4 %d ", len(objects[0])+1, len(objects[0])+len(objects[1])+2)
	stream := zip([]byte(header + strings.Join(objects, "\n")))
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.5\n")
	offset := pdf.Len()
	fmt.Fprintf(&pdf, "1 0 obj\n<< /Type /ObjStm /N 3 /First %d /Length %d /Filter /FlateDecode >>\nstream\n", len(header), len(stream))
	pdf.Write(stream)
	pdf.WriteString("\nendstream\nendobj\n")
	xrefOffset := pdf.Len()
	if badOffset {
		offset = 999999
	}
	entries := [][3]uint32{{0, 0, 65535}, {1, uint32(offset), 0}, {2, 1, 0}, {2, 1, 1}, {2, 1, 2}, {1, uint32(xrefOffset), 0}}
	var raw []byte
	prev := make([]byte, 7)
	for _, e := range entries {
		row := make([]byte, 7)
		row[0] = byte(e[0])
		binary.BigEndian.PutUint32(row[1:5], e[1])
		binary.BigEndian.PutUint16(row[5:7], uint16(e[2]))
		if predictor {
			raw = append(raw, 2)
			for i, x := range row {
				raw = append(raw, x-prev[i])
			}
			prev = row
		} else {
			raw = append(raw, row...)
		}
	}
	xref := zip(raw)
	dp := ""
	if predictor {
		dp = " /DecodeParms << /Predictor 12 /Columns 7 /Colors 1 /BitsPerComponent 8 >>"
	}
	fmt.Fprintf(&pdf, "5 0 obj\n<< /Type /XRef /Size 6 /Root 2 0 R /W [1 4 2] /Length %d /Filter /FlateDecode%s >>\nstream\n", len(xref), dp)
	pdf.Write(xref)
	fmt.Fprintf(&pdf, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xrefOffset)
	return pdf.Bytes()
}

func TestPDFParserRegressionFixtures(t *testing.T) {
	for _, pred := range []bool{false, true} {
		n, e := ParsePDFData(compressedPDFFixture(pred, false))
		if e != nil || n != 1 {
			t.Fatalf("predictor=%v n=%d err=%v", pred, n, e)
		}
	}
	if _, e := ParsePDFData(compressedPDFFixture(false, true)); e == nil {
		t.Fatal("out-of-range ObjStm offset accepted")
	}
	for _, bad := range [][]byte{
		bytes.Replace(generateTestPDF(1), []byte("/Count 1"), []byte("/Count 7"), 1),
		bytes.Replace(generateTestPDF(1), []byte("/Kids [3 0 R]"), []byte("/Kids [2 0 R]"), 1),
	} {
		if _, e := ParsePDFData(bad); e == nil {
			t.Fatal("invalid page tree accepted")
		}
	}
}

func FuzzParsePDFData(f *testing.F) {
	f.Add(generateTestPDF(1))
	f.Add(compressedPDFFixture(true, false))
	f.Add(compressedPDFFixture(false, true))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		ParsePDFData(data)
	})
}

// Helper per creare un DOCX minimale valido per i test di importazione.
func createTestDocx(t *testing.T, docxPath, chapterTitle, chapterBody string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	docXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>%s</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t>%s</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`, chapterTitle, chapterBody)

	f, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("errore creazione word/document.xml nel docx: %v", err)
	}
	if _, err := f.Write([]byte(docXML)); err != nil {
		t.Fatalf("errore scrittura word/document.xml: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("errore chiusura zip writer docx: %v", err)
	}
	if err := os.WriteFile(docxPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("errore salvataggio file docx: %v", err)
	}
}

func TestDocxImportDoesNotOverwriteExistingFolderWithoutBookYaml(t *testing.T) {
	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "il-mio-libro.docx")
	createTestDocx(t, docxPath, "Capitolo Uno", "Testo importato dal DOCX.")

	// Creiamo intenzionalmente la cartella preesistente di destinazione "il-mio-libro-md" SENZA book.yaml
	targetDir := filepath.Join(tmpDir, "il-mio-libro-md")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	existingFile := filepath.Join(targetDir, "01-capitolo-1.md")
	existingContent := "TESTO PREESISTENTE CHE NON DEVE ESSERE SOVRASCRITTO"
	if err := os.WriteFile(existingFile, []byte(existingContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Eseguiamo l'importazione DOCX
	book, err := importDocxVault(docxPath)
	if err != nil {
		t.Fatalf("importDocxVault fallito: %v", err)
	}
	if book == nil {
		t.Fatal("libro importato nullo")
	}

	// 1. Verifichiamo che il file preesistente nella cartella target originale NON sia stato toccato
	data, err := os.ReadFile(existingFile)
	if err != nil {
		t.Fatalf("il file preesistente è scomparso: %v", err)
	}
	if string(data) != existingContent {
		t.Fatalf("il file preesistente è stato sovrascritto! Atteso %q, ottenuto %q", existingContent, string(data))
	}
	if fileExists(filepath.Join(targetDir, "book.yaml")) {
		t.Fatalf("la cartella preesistente senza book.yaml è stata inquinata con un nuovo book.yaml!")
	}

	// 2. Verifichiamo che l'importazione sia avvenuta in una nuova cartella non in conflitto (il-mio-libro-md-2)
	newVault := getVaultDir()
	expectedVault := filepath.Join(tmpDir, "il-mio-libro-md-2")
	if newVault != expectedVault {
		t.Fatalf("destinazione errata: attesa %s, ottenuta %s", expectedVault, newVault)
	}
	if !fileExists(filepath.Join(newVault, "book.yaml")) {
		t.Fatalf("book.yaml assente nella nuova cartella vault: %s", newVault)
	}
	importedCh := filepath.Join(newVault, "01-capitolo-uno.md")
	if !fileExists(importedCh) {
		t.Fatalf("capitolo importato assente in %s", importedCh)
	}
}

func TestTrashDoesNotOverwritePreviouslyDeletedChaptersAndCovers(t *testing.T) {
	vDir := t.TempDir()
	setVault(vDir)

	chFile := "01-capitolo-1.md"
	chPath := filepath.Join(vDir, chFile)
	v1Content := "Prima stesura del capitolo 1"
	if err := os.WriteFile(chPath, []byte(v1Content), 0644); err != nil {
		t.Fatal(err)
	}
	book := &Book{
		Title:    "Libro Test Cestino",
		Language: "it",
		Chapters: []Chapter{{File: chFile, Title: "Capitolo 1"}},
	}
	if err := writeBookYAML(book); err != nil {
		t.Fatal(err)
	}

	// 1. Prima eliminazione del capitolo
	delReq, _ := json.Marshal(map[string]string{"file": chFile})
	w1 := httptest.NewRecorder()
	apiDeleteChapter(w1, httptest.NewRequest("POST", "/api/deletechapter", bytes.NewReader(delReq)))
	if w1.Code != http.StatusOK {
		t.Fatalf("prima apiDeleteChapter fallita: status=%d body=%s", w1.Code, w1.Body.String())
	}

	trashV1 := filepath.Join(vDir, ".cestina", chFile)
	if !fileExists(trashV1) {
		t.Fatalf("file non trovato nel cestino dopo prima eliminazione: %s", trashV1)
	}
	data1, _ := os.ReadFile(trashV1)
	if string(data1) != v1Content {
		t.Fatalf("contenuto cestino v1 errato: %q", string(data1))
	}

	// 2. Ricreazione del capitolo con lo stesso nome ma contenuto diverso (v2)
	v2Content := "Seconda stesura riscritta del capitolo 1"
	if err := os.WriteFile(chPath, []byte(v2Content), 0644); err != nil {
		t.Fatal(err)
	}
	book.Chapters = []Chapter{{File: chFile, Title: "Capitolo 1 (V2)"}}
	if err := writeBookYAML(book); err != nil {
		t.Fatal(err)
	}

	// 3. Seconda eliminazione dello stesso capitolo omonimo
	delReq2, _ := json.Marshal(map[string]string{"file": chFile})
	w2 := httptest.NewRecorder()
	apiDeleteChapter(w2, httptest.NewRequest("POST", "/api/deletechapter", bytes.NewReader(delReq2)))
	if w2.Code != http.StatusOK {
		t.Fatalf("seconda apiDeleteChapter fallita: status=%d body=%s", w2.Code, w2.Body.String())
	}

	// Verifica: la prima copia nel cestino DEVE essere intatta!
	data1After, _ := os.ReadFile(trashV1)
	if string(data1After) != v1Content {
		t.Fatalf("la prima copia nel cestino è stata sovrascritta! atteso %q, ottenuto %q", v1Content, string(data1After))
	}

	// Verifica: la seconda copia nel cestino è stata salvata come 01-capitolo-1-2.md
	trashV2 := filepath.Join(vDir, ".cestina", "01-capitolo-1-2.md")
	if !fileExists(trashV2) {
		t.Fatalf("seconda copia non trovata nel cestino come %s", trashV2)
	}
	data2, _ := os.ReadFile(trashV2)
	if string(data2) != v2Content {
		t.Fatalf("contenuto cestino v2 errato: atteso %q, ottenuto %q", v2Content, string(data2))
	}

	// 4. Test di protezione cestino anche per le copertine (apiDelCover)
	coverDir := filepath.Join(vDir, "cover")
	_ = os.MkdirAll(coverDir, 0755)
	coverFile := filepath.Join(coverDir, "cover.jpg")
	_ = os.WriteFile(coverFile, []byte("COVER_V1"), 0644)
	cReq1, _ := json.Marshal(map[string]string{"name": "cover.jpg"})
	wC1 := httptest.NewRecorder()
	apiDelCover(wC1, httptest.NewRequest("POST", "/api/delcover", bytes.NewReader(cReq1)))
	if wC1.Code != http.StatusOK {
		t.Fatalf("prima apiDelCover fallita: %d", wC1.Code)
	}

	_ = os.WriteFile(coverFile, []byte("COVER_V2"), 0644)
	wC2 := httptest.NewRecorder()
	apiDelCover(wC2, httptest.NewRequest("POST", "/api/delcover", bytes.NewReader(cReq1)))
	if wC2.Code != http.StatusOK {
		t.Fatalf("seconda apiDelCover fallita: %d", wC2.Code)
	}

	coverTrash1 := filepath.Join(vDir, ".cestina", "cover.jpg")
	coverTrash2 := filepath.Join(vDir, ".cestina", "cover-2.jpg")
	if !fileExists(coverTrash1) || !fileExists(coverTrash2) {
		t.Fatalf("copertine cestinate non preservate entrambe!")
	}
	c1Data, _ := os.ReadFile(coverTrash1)
	c2Data, _ := os.ReadFile(coverTrash2)
	if string(c1Data) != "COVER_V1" || string(c2Data) != "COVER_V2" {
		t.Fatalf("contenuti copertine cestinate sovrascritti: c1=%q c2=%q", string(c1Data), string(c2Data))
	}
}

func TestMarkdownExtensionChapterSupport(t *testing.T) {
	vDir := t.TempDir()
	setVault(vDir)

	chFile := "01-capitolo-1.markdown"
	initialContent := "---\nid: id-md-1\ntitle: Introduzione\n---\n\nTesto in formato markdown originario."
	if err := os.WriteFile(filepath.Join(vDir, chFile), []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	book := &Book{
		Title:    "Libro Markdown Extension",
		Language: "it",
		Chapters: []Chapter{{File: chFile, Title: "Introduzione", ID: "id-md-1"}},
	}
	if err := writeBookYAML(book); err != nil {
		t.Fatal(err)
	}

	// 1. Lettura capitolo via apiChapter
	wRead := httptest.NewRecorder()
	apiChapter(wRead, httptest.NewRequest("GET", "/api/chapter?file="+chFile, nil))
	if wRead.Code != http.StatusOK {
		t.Fatalf("apiChapter per .markdown ha fallito con status %d: %s", wRead.Code, wRead.Body.String())
	}
	var readResp map[string]string
	if err := json.Unmarshal(wRead.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("unmarshal risposta apiChapter fallito: %v", err)
	}
	if readResp["file"] != chFile || !strings.Contains(readResp["content"], "Testo in formato markdown originario.") {
		t.Fatalf("contenuto apiChapter inatteso: %v", readResp)
	}
	baseVer := readResp["version"]

	// 2. Salvataggio capitolo via apiSave
	updatedContent := "---\nid: id-md-1\ntitle: Introduzione\n---\n\nTesto modificato salvato con successo."
	saveReq, _ := json.Marshal(map[string]string{
		"file":         chFile,
		"content":      updatedContent,
		"base_version": baseVer,
		"vault":        vDir,
	})
	wSave := httptest.NewRecorder()
	apiSave(wSave, httptest.NewRequest("POST", "/api/save", bytes.NewReader(saveReq)))
	if wSave.Code != http.StatusOK {
		t.Fatalf("apiSave per .markdown ha fallito con status %d: %s", wSave.Code, wSave.Body.String())
	}
	savedDisk, _ := os.ReadFile(filepath.Join(vDir, chFile))
	if string(savedDisk) != updatedContent {
		t.Fatalf("contenuto salvato su disco difforme: %q", string(savedDisk))
	}

	// 3. Rinominazione titolo capitolo via apiSetChapterTitle
	titleReq, _ := json.Marshal(map[string]string{
		"file":  chFile,
		"title": "Nuova Introduzione Riveduta",
	})
	wTitle := httptest.NewRecorder()
	apiSetChapterTitle(wTitle, httptest.NewRequest("POST", "/api/setchaptertitle", bytes.NewReader(titleReq)))
	if wTitle.Code != http.StatusOK {
		t.Fatalf("apiSetChapterTitle per .markdown ha fallito con status %d: %s", wTitle.Code, wTitle.Body.String())
	}
	bReloaded := loadBook()
	if bReloaded == nil || len(bReloaded.Chapters) == 0 || bReloaded.Chapters[0].Title != "Nuova Introduzione Riveduta" {
		t.Fatalf("titolo non aggiornato in book.yaml per capitolo .markdown")
	}

	// 4. Eliminazione capitolo via apiDeleteChapter
	delReq, _ := json.Marshal(map[string]string{"file": chFile})
	wDel := httptest.NewRecorder()
	apiDeleteChapter(wDel, httptest.NewRequest("POST", "/api/deletechapter", bytes.NewReader(delReq)))
	if wDel.Code != http.StatusOK {
		t.Fatalf("apiDeleteChapter per .markdown ha fallito con status %d: %s", wDel.Code, wDel.Body.String())
	}
	if fileExists(filepath.Join(vDir, chFile)) {
		t.Fatalf("file .markdown ancora presente nel vault dopo eliminazione!")
	}
	if !fileExists(filepath.Join(vDir, ".cestina", chFile)) {
		t.Fatalf("file .markdown non presente nel cestino dopo eliminazione!")
	}
}

func TestPubcraftInitColonQuotingAndYAMLParity(t *testing.T) {
	cBin := "../../build/pubcraft"
	if _, err := os.Stat(cBin); err != nil {
		t.Skipf("binario CLI %s assente: compila prima con make", cBin)
	}

	testCases := []struct {
		title         string
		expectedTitle string
	}{
		{"Libro: sottotitolo", "Libro: sottotitolo"},
		{"Guida completa: Parte 1: Inizio", "Guida completa: Parte 1: Inizio"},
		{"Il \"Miglior\" Libro: 2026", "Il \"Miglior\" Libro: 2026"},
		{"Special #1: Note & Details", "Special #1: Note & Details"},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("case_%d", i+1), func(t *testing.T) {
			targetDir := filepath.Join(t.TempDir(), fmt.Sprintf("project_%d", i+1))
			cmd := exec.Command(cBin, "init", targetDir, tc.title)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("pubcraft init fallito con titolo %q: %v\nOutput: %s", tc.title, err, string(out))
			}

			yamlPath := filepath.Join(targetDir, "book.yaml")
			book, err := parseBookYAML(yamlPath)
			if err != nil {
				raw, _ := os.ReadFile(yamlPath)
				t.Fatalf("decodeBookYAML fallito su progetto inizializzato dalla CLI per titolo %q: %v\nbook.yaml:\n%s", tc.title, err, string(raw))
			}
			if book.Title != tc.expectedTitle {
				t.Errorf("titolo decodificato errato: atteso %q, ottenuto %q", tc.expectedTitle, book.Title)
			}

			// Verifica anche la lettura da parte del parser CLI (pubcraft info)
			infoCmd := exec.Command(cBin, "info", targetDir)
			infoOut, err := infoCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("pubcraft info fallito: %v\nOutput: %s", err, string(infoOut))
			}
			cParsed := parseCInfoOutput(string(infoOut))
			if cParsed.Title != tc.expectedTitle {
				t.Errorf("pubcraft info ha estratto titolo errato: atteso %q, ottenuto %q\nRaw info:\n%s", tc.expectedTitle, cParsed.Title, string(infoOut))
			}
		})
	}
}

func TestOpenMarkdownFolderDoesNotCorruptPreviousActiveVault(t *testing.T) {
	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "libro-a")
	if err := os.MkdirAll(dirA, 0755); err != nil {
		t.Fatal(err)
	}
	bookA := &Book{
		Title:    "Libro A Originale",
		Author:   "Autore A",
		Language: "it",
		Chapters: []Chapter{{File: "01-a.md", Title: "Capitolo A"}},
	}
	if err := os.WriteFile(filepath.Join(dirA, "01-a.md"), []byte("# Capitolo A\n\nTesto libro A."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeBookYAMLAt(filepath.Join(dirA, "book.yaml"), bookA); err != nil {
		t.Fatal(err)
	}
	setVault(dirA)

	// Creiamo la cartella B con file markdown ma SENZA book.yaml
	dirB := filepath.Join(tmp, "libro-b")
	if err := os.MkdirAll(dirB, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "01-b.md"), []byte("# Capitolo B\n\nTesto libro B."), 0644); err != nil {
		t.Fatal(err)
	}

	// Apriamo dirB tramite apiOpenAt
	body, _ := json.Marshal(map[string]string{"path": dirB})
	w := httptest.NewRecorder()
	apiOpenAt(w, httptest.NewRequest("POST", "/api/openat", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("apiOpenAt status=%d body=%s", w.Code, w.Body.String())
	}

	// Verifichiamo che il book.yaml di libro A NON sia stato sovrascritto con i metadati di B!
	savedA, err := parseBookYAML(filepath.Join(dirA, "book.yaml"))
	if err != nil {
		t.Fatalf("lettura book.yaml di A fallita: %v", err)
	}
	if savedA.Title != "Libro A Originale" || savedA.Author != "Autore A" {
		t.Fatalf("CORRUZIONE RILEVATA: il book.yaml di A è stato sovrascritto! Title=%q Author=%q", savedA.Title, savedA.Author)
	}
	if len(savedA.Chapters) != 1 || savedA.Chapters[0].File != "01-a.md" {
		t.Fatalf("CORRUZIONE RILEVATA nei capitoli di A: %+v", savedA.Chapters)
	}

	// Verifichiamo che dirB abbia il suo book.yaml
	if !fileExists(filepath.Join(dirB, "book.yaml")) {
		t.Fatalf("book.yaml non creato in dirB")
	}
}

func TestChapterRenameUpdatesVersionAndAllowsSubsequentSave(t *testing.T) {
	vDir := t.TempDir()
	setVault(vDir)

	chFile := "01-intro.md"
	origContent := "---\nid: id-intro\ntitle: Titolo Iniziale\n---\n\nTesto iniziale."
	if err := os.WriteFile(filepath.Join(vDir, chFile), []byte(origContent), 0644); err != nil {
		t.Fatal(err)
	}
	b := &Book{
		Title:    "Libro Test Rinomina",
		Language: "it",
		Chapters: []Chapter{{File: chFile, Title: "Titolo Iniziale", ID: "id-intro"}},
	}
	if err := writeBookYAML(b); err != nil {
		t.Fatal(err)
	}

	// 1. Chiamata a apiSetChapterTitle
	renameBody, _ := json.Marshal(map[string]string{
		"file":  chFile,
		"title": "Nuovo Titolo Capitolo",
	})
	wRename := httptest.NewRecorder()
	apiSetChapterTitle(wRename, httptest.NewRequest("POST", "/api/setchaptertitle", bytes.NewReader(renameBody)))
	if wRename.Code != http.StatusOK {
		t.Fatalf("apiSetChapterTitle fallito: status=%d body=%s", wRename.Code, wRename.Body.String())
	}
	var renameResp map[string]any
	if err := json.Unmarshal(wRename.Body.Bytes(), &renameResp); err != nil {
		t.Fatalf("unmarshal fallito: %v", err)
	}
	newVer, ok := renameResp["version"].(string)
	if !ok || newVer == "" {
		t.Fatalf("apiSetChapterTitle non ha restituito il nuovo 'version': %+v", renameResp)
	}

	// 2. Salvataggio successivo usando newVer come base_version (simula autosalvataggio)
	updatedContent := "---\nid: id-intro\ntitle: Nuovo Titolo Capitolo\n---\n\nTesto iniziale con modifiche dell'autore."
	saveBody, _ := json.Marshal(map[string]string{
		"file":         chFile,
		"content":      updatedContent,
		"base_version": newVer,
		"vault":        vDir,
	})
	wSave := httptest.NewRecorder()
	apiSave(wSave, httptest.NewRequest("POST", "/api/save", bytes.NewReader(saveBody)))
	if wSave.Code != http.StatusOK {
		t.Fatalf("apiSave successivo alla rinomina ha fallito con status=%d (conflitto 409 non risolto): %s", wSave.Code, wSave.Body.String())
	}
}

func TestPubcraftInitDoesNotOverwriteExistingFirstChapter(t *testing.T) {
	cBin := "../../build/pubcraft"
	if _, err := os.Stat(cBin); err != nil {
		t.Skipf("binario CLI %s assente: compila prima con make", cBin)
	}

	targetDir := filepath.Join(t.TempDir(), "progetto-esistente")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	ch1Path := filepath.Join(targetDir, "01-capitolo-1.md")
	manuscriptContent := "MANUSCRITTO PREESISTENTE DA NON DISTRUGGERE"
	if err := os.WriteFile(ch1Path, []byte(manuscriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Eseguiamo pubcraft init su cartella con capitolo già presente
	cmd := exec.Command(cBin, "init", targetDir, "Nuovo Titolo")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pubcraft init doveva fallire su capitolo preesistente, invece è uscito con 0! Output: %s", string(out))
	}

	// Verifichiamo che il file non sia stato toccato
	data, _ := os.ReadFile(ch1Path)
	if string(data) != manuscriptContent {
		t.Fatalf("01-capitolo-1.md è stato sovrascritto! Atteso %q, ottenuto %q", manuscriptContent, string(data))
	}
	if fileExists(filepath.Join(targetDir, "book.yaml")) {
		t.Fatalf("book.yaml è stato creato nonostante il fallimento!")
	}
}

func TestChapterFileNameWithColonIsQuotedInBookYAML(t *testing.T) {
	vDir := t.TempDir()
	setVault(vDir)

	chFile := "01: apertura.md"
	book := &Book{
		Title:    "Libro Colon File",
		Language: "it",
		Chapters: []Chapter{{File: chFile, Title: "Apertura"}},
	}
	if err := writeBookYAML(book); err != nil {
		t.Fatalf("writeBookYAML fallito: %v", err)
	}

	yamlData, err := os.ReadFile(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yamlData), "- file: \"01: apertura.md\"") {
		t.Fatalf("il nome file con due punti non è stato quotato! book.yaml:\n%s", string(yamlData))
	}

	// Riapertura del libro: non deve fallire con syntax error
	reloaded, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatalf("parseBookYAML ha fallito la decodifica di book.yaml: %v", err)
	}
	if len(reloaded.Chapters) != 1 || reloaded.Chapters[0].File != chFile {
		t.Fatalf("capitolo non recuperato correttamente: %+v", reloaded.Chapters)
	}
}

func TestSingleMarkdownImportCopiesImagesAndRewritesReferences(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "sorgente")
	if err := os.MkdirAll(filepath.Join(srcDir, "images"), 0755); err != nil {
		t.Fatal(err)
	}

	imgData1 := []byte("PNG_DUMMY_IMAGE_DATA_1")
	imgData2 := []byte("PNG_DUMMY_IMAGE_DATA_2")
	if err := os.WriteFile(filepath.Join(srcDir, "images", "photo.png"), imgData1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "portrait.png"), imgData2, 0644); err != nil {
		t.Fatal(err)
	}

	mdContent := `# Titolo Libro

Ecco la prima foto:
![Foto](images/photo.png)

Ed ecco il ritratto dell'autore:
![Ritratto](portrait.png)
`
	mdPath := filepath.Join(srcDir, "manoscritto.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Importa il markdown singolo
	b, err := importMarkdownVault(mdPath)
	if err != nil {
		t.Fatalf("importMarkdownVault fallito: %v", err)
	}
	if b == nil {
		t.Fatal("libro importato nullo")
	}

	vDir := getVaultDir()
	// 1. Verifichiamo che images/photo.png e images/portrait.png esistano nella nuova cartella vault
	dstImg1 := filepath.Join(vDir, "images", "photo.png")
	dstImg2 := filepath.Join(vDir, "images", "portrait.png")
	if !fileExists(dstImg1) {
		t.Fatalf("images/photo.png assente nella cartella di destinazione: %s", dstImg1)
	}
	if !fileExists(dstImg2) {
		t.Fatalf("images/portrait.png assente nella cartella di destinazione: %s", dstImg2)
	}

	d1, _ := os.ReadFile(dstImg1)
	d2, _ := os.ReadFile(dstImg2)
	if string(d1) != string(imgData1) || string(d2) != string(imgData2) {
		t.Fatalf("dati immagini corrotti o non identici all'originale")
	}

	// 2. Verifichiamo che i riferimenti nel capitolo generato puntino entrambi a images/
	chPath := filepath.Join(vDir, b.Chapters[0].File)
	chTextBytes, err := os.ReadFile(chPath)
	if err != nil {
		t.Fatalf("lettura capitolo importato fallita: %v", err)
	}
	chText := string(chTextBytes)
	if !strings.Contains(chText, "![Foto](images/photo.png)") {
		t.Fatalf("riferimento images/photo.png mancante in:\n%s", chText)
	}
	if !strings.Contains(chText, "![Ritratto](images/portrait.png)") {
		t.Fatalf("riferimento portrait.png non aggiornato a images/portrait.png in:\n%s", chText)
	}

	// 3. Verifichiamo che i file originali siano rimasti intatti
	origMd, _ := os.ReadFile(mdPath)
	if string(origMd) != mdContent {
		t.Fatalf("il file markdown originale è stato modificato!")
	}
}

func TestUpdateMarkdownTitlePreservesSceneBreaksWithoutFrontmatter(t *testing.T) {
	content := `# Vecchio Titolo

C'era una volta un regno lontano.

---

Passarono molti anni e il regno crebbe.

---

E vissero tutti felici e contenti.
`
	updated := updateMarkdownTitleInContent(content, "Nuovo Titolo")
	if !strings.HasPrefix(strings.TrimSpace(updated), "# Nuovo Titolo") {
		t.Fatalf("intestazione H1 non aggiornata a # Nuovo Titolo:\n%s", updated)
	}
	if strings.Contains(updated, "title:") {
		t.Fatalf("title: è stato erroneamente inserito nel corpo scambiando i separatori di scena per frontmatter:\n%s", updated)
	}
	if strings.Count(updated, "---") != 2 {
		t.Fatalf("i separatori di scena --- sono stati modificati:\n%s", updated)
	}
}
