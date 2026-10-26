package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportMarkdownManuscript(t *testing.T) {
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "manoscritto-test.md")
	content := `---
title: Il Segreto della Foresta
author: Mario Rossi
---

# Prologo

Nel profondo della selva oscura...

# Capitolo 1

Il viaggio ebbe inizio in una mattina di primavera.

I cavalieri avanzarono senza timore.
`
	if err := os.WriteFile(mdFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	book, err := importMarkdownVault(mdFile)
	if err != nil {
		t.Fatalf("importMarkdownVault fallito: %v", err)
	}

	if book.Title != "Il Segreto della Foresta" {
		t.Errorf("titolo atteso 'Il Segreto della Foresta', ottenuto %q", book.Title)
	}
	if book.Author != "Mario Rossi" {
		t.Errorf("autore atteso 'Mario Rossi', ottenuto %q", book.Author)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("attesi 2 capitoli, trovati %d", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Prologo" {
		t.Errorf("titolo cap 1 atteso 'Prologo', ottenuto %q", book.Chapters[0].Title)
	}
	if book.Chapters[1].Title != "Capitolo 1" {
		t.Errorf("titolo cap 2 atteso 'Capitolo 1', ottenuto %q", book.Chapters[1].Title)
	}

	// Verifica che il vault contenga book.yaml valido
	vDir := getVaultDir()
	if !fileExists(filepath.Join(vDir, "book.yaml")) {
		t.Errorf("book.yaml assente nel vault generato: %s", vDir)
	}
}

func TestImportODTManuscript(t *testing.T) {
	tmpDir := t.TempDir()
	odtFile := filepath.Join(tmpDir, "test.odt")

	// Creiamo un archivio ZIP con content.xml minimale compatibile con LibreOffice/OpenOffice
	contentXML := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
 xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:h text:outline-level="1">Introduzione</text:h>
      <text:p>Questo è il testo del primo paragrafo del libro.</text:p>
      <text:h text:outline-level="1">Primo Capitolo</text:h>
      <text:p>Secondo paragrafo con spiegazioni dettagliate.</text:p>
    </office:text>
  </office:body>
</office:document-content>`

	metaXML := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-meta xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
 xmlns:dc="http://purl.org/dc/elements/1.1/">
  <office:meta>
    <dc:title>Libro OpenOffice</dc:title>
    <dc:creator>Autore OpenOffice</dc:creator>
  </office:meta>
</office:document-meta>`

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	fMeta, err := zw.Create("meta.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fMeta.Write([]byte(metaXML))

	fContent, err := zw.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fContent.Write([]byte(contentXML))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(odtFile, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	book, err := importOdtVault(odtFile)
	if err != nil {
		t.Fatalf("importOdtVault fallito: %v", err)
	}

	if book.Title != "Libro OpenOffice" {
		t.Errorf("titolo atteso 'Libro OpenOffice', ottenuto %q", book.Title)
	}
	if book.Author != "Autore OpenOffice" {
		t.Errorf("autore atteso 'Autore OpenOffice', ottenuto %q", book.Author)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("attesi 2 capitoli, trovati %d", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Introduzione" {
		t.Errorf("titolo cap 1 atteso 'Introduzione', ottenuto %q", book.Chapters[0].Title)
	}
	if book.Chapters[1].Title != "Primo Capitolo" {
		t.Errorf("titolo cap 2 atteso 'Primo Capitolo', ottenuto %q", book.Chapters[1].Title)
	}
}

func TestApiOpenAtWithMarkdownFile(t *testing.T) {
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "capitolo-singolo.md")
	content := `# Capitolo Uno\nTesto del capitolo unico.`
	if err := os.WriteFile(mdFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"path":"` + mdFile + `"}`)
	req := httptest.NewRequest("POST", "/api/openat", body)
	w := httptest.NewRecorder()

	apiOpenAt(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status atteso 200, ottenuto %d: %s", w.Code, w.Body.String())
	}

	vDir := getVaultDir()
	if !fileExists(filepath.Join(vDir, "book.yaml")) {
		t.Errorf("book.yaml assente nel vault generato da apiOpenAt: %s", vDir)
	}
}

func TestImportMarkdownMultiFileFolderOpensWholeVault(t *testing.T) {
	// Se l'utente ha una cartella con tutti i file di capitolo, aprendo uno qualsiasi dei file
	// o la cartella stessa deve aprirsi l'intera cartella con tutti i capitoli, non solo il singolo file.
	tmpDir := filepath.Join(t.TempDir(), "MioRomanzoCompleto")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"01-intro.md":     "# Introduzione\nTesto introduzione.",
		"02-capitolo1.md": "# Capitolo Primo\nTesto capitolo primo.",
		"03-capitolo2.md": "# Capitolo Secondo\nTesto capitolo secondo.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Apertura selezionando un singolo file interno alla cartella
	book, err := importMarkdownVault(filepath.Join(tmpDir, "01-intro.md"))
	if err != nil {
		t.Fatalf("apertura fallita: %v", err)
	}
	if len(book.Chapters) != 3 {
		t.Fatalf("attesi tutti e 3 i capitoli della cartella, trovati %d", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Introduzione" || book.Chapters[1].Title != "Capitolo Primo" || book.Chapters[2].Title != "Capitolo Secondo" {
		t.Errorf("titoli capitoli inattesi: %+v", book.Chapters)
	}
	if getVaultDir() != tmpDir {
		t.Errorf("vault atteso %s, ottenuto %s", tmpDir, getVaultDir())
	}

	// 2. Apertura via apiOpenAt con percorso cartella
	body := strings.NewReader(`{"path":"` + tmpDir + `"}`)
	req := httptest.NewRequest("POST", "/api/openat", body)
	w := httptest.NewRecorder()
	apiOpenAt(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("apiOpenAt su cartella fallito: %d - %s", w.Code, w.Body.String())
	}
	if getVaultDir() != tmpDir {
		t.Errorf("vault atteso %s, ottenuto %s", tmpDir, getVaultDir())
	}
}

func TestAutoInitNaturalSortAndSectionTypes(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "LibroOrdinamento")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"1-prologo.md":      "# Prologo\nInizio.",
		"2-capitolo.md":     "# Capitolo Due\nCorpo.",
		"10-capitolo.md":    "# Capitolo Dieci\nFine.",
		"readme.md":         "Note non di capitolo",
		"epilogo.markdown":  "# Epilogo\nChiusura.",
		"ringraziamenti.md": "# Ringraziamenti\nGrazie a tutti.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	book := autoInitVaultFromMarkdown(tmpDir)
	if book == nil {
		t.Fatal("autoInitVaultFromMarkdown ha restituito nil")
	}

	// readme.md deve essere escluso perché ci sono altri file di capitolo
	for _, ch := range book.Chapters {
		if strings.EqualFold(ch.File, "readme.md") {
			t.Errorf("readme.md non doveva essere incluso nei capitoli")
		}
	}

	// Verifica ordinamento naturale: 1-prologo, 2-capitolo, 10-capitolo (non 1, 10, 2)
	var filenames []string
	for _, ch := range book.Chapters {
		filenames = append(filenames, ch.File)
	}

	idx1 := -1
	idx2 := -1
	idx10 := -1
	for i, fn := range filenames {
		if fn == "1-prologo.md" {
			idx1 = i
		} else if fn == "2-capitolo.md" {
			idx2 = i
		} else if fn == "10-capitolo.md" {
			idx10 = i
		}
	}
	if !(idx1 < idx2 && idx2 < idx10) {
		t.Errorf("ordinamento naturale fallito per capitoli: %v (indici 1=%d, 2=%d, 10=%d)", filenames, idx1, idx2, idx10)
	}
}

func TestImportScrivenerProject(t *testing.T) {
	tmpDir := t.TempDir()
	scrivDir := filepath.Join(tmpDir, "MioRomanzo.scriv")
	filesDataDir := filepath.Join(scrivDir, "Files", "Data")
	if err := os.MkdirAll(filesDataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	ch1Dir := filepath.Join(filesDataDir, uuid1)
	ch2Dir := filepath.Join(filesDataDir, uuid2)
	_ = os.MkdirAll(ch1Dir, 0o755)
	_ = os.MkdirAll(ch2Dir, 0o755)

	rtf1 := `{\rtf1\ansi\deff0 {\fonttbl{\f0 Arial;}} \f0\fs24 Nel \b cuore \b0 della notte, il silenzio regnava sovrano.\par}`
	rtf2 := `{\rtf1\ansi\deff0 {\fonttbl{\f0 Arial;}} \f0\fs24 All'alba il sole \i illumin\'f2 \i0 le montagne.\par}`

	_ = os.WriteFile(filepath.Join(ch1Dir, "content.rtf"), []byte(rtf1), 0o644)
	_ = os.WriteFile(filepath.Join(ch2Dir, "content.rtf"), []byte(rtf2), 0o644)

	scrivxContent := `<?xml version="1.0" encoding="UTF-8"?>
<ScrivenerProject Version="2.0" Author="Autore Di Test" Title="Il Mio Grande Romanzo">
    <Binder>
        <BinderItem UUID="DRAFT-0000" Type="DraftFolder">
            <Title>Manuscript</Title>
            <Children>
                <BinderItem UUID="` + uuid1 + `" Type="Text">
                    <Title>Capitolo 1: La Notte</Title>
                    <MetaData><IncludeInCompile>Yes</IncludeInCompile></MetaData>
                </BinderItem>
                <BinderItem UUID="` + uuid2 + `" Type="Text">
                    <Title>Capitolo 2: Il Risveglio</Title>
                    <MetaData><IncludeInCompile>Yes</IncludeInCompile></MetaData>
                </BinderItem>
            </Children>
        </BinderItem>
    </Binder>
</ScrivenerProject>`

	if err := os.WriteFile(filepath.Join(scrivDir, "MioRomanzo.scrivx"), []byte(scrivxContent), 0o644); err != nil {
		t.Fatal(err)
	}

	book, err := importAnyManuscript(scrivDir)
	if err != nil {
		t.Fatalf("importAnyManuscript per .scriv fallito: %v", err)
	}

	if book.Title != "Il Mio Grande Romanzo" {
		t.Errorf("titolo atteso 'Il Mio Grande Romanzo', ottenuto %q", book.Title)
	}
	if book.Author != "Autore Di Test" {
		t.Errorf("autore atteso 'Autore Di Test', ottenuto %q", book.Author)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("attesi 2 capitoli, trovati %d", len(book.Chapters))
	}
	if book.Chapters[0].Title != "Capitolo 1: La Notte" {
		t.Errorf("titolo cap 1 inatteso: %q", book.Chapters[0].Title)
	}

	vDir := getVaultDir()
	ch1Data, err := os.ReadFile(filepath.Join(vDir, book.Chapters[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ch1Data), "**cuore**") {
		t.Errorf("conversione RTF grassetto fallita: %s", string(ch1Data))
	}

	ch2Data, err := os.ReadFile(filepath.Join(vDir, book.Chapters[1].File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ch2Data), "*illuminò*") {
		t.Errorf("conversione RTF corsivo o carattere accentato fallita: %s", string(ch2Data))
	}
}

// Optional external fixtures are never stored in the repository or imported in
// place: importing may create a sibling vault, so use a disposable copy.
func optionalScrivenerTemplate(t *testing.T) string {
	t.Helper()
	src := os.Getenv("PUBCRAFT_SCRIVENER_TEMPLATE_FIXTURE")
	if src == "" {
		t.Skip("set PUBCRAFT_SCRIVENER_TEMPLATE_FIXTURE to test an external template")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "sample.scrivtemplate")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestImportScrivenerRealTemplate(t *testing.T) {
	tmplPath := optionalScrivenerTemplate(t)

	book, err := importScrivenerVault(tmplPath)
	if err != nil {
		t.Fatalf("importScrivenerVault con template reale fallito: %v", err)
	}

	if len(book.Chapters) == 0 {
		t.Errorf("nessun capitolo estratto dal template Scrivener reale")
	}
	vaultMu.Lock()
	v := vaultDir
	vaultMu.Unlock()
	for i, ch := range book.Chapters {
		filePath := filepath.Join(v, ch.File)
		data, err := os.ReadFile(filePath)
		if err == nil {
			content := string(data)
			if strings.Contains(content, "<$Scr") || strings.Contains(content, "<!$Scr") {
				t.Logf("Capitolo %d (%s):\n%s\n---", i, ch.Title, content[:min(len(content), 400)])
			}
		}
	}
}

func TestCleanScrivenerMarkdown(t *testing.T) {
	sample := `<$ScrKeepWithNext><$Scr_H::1><$Scr_Ps::0>
**ACT ONE:

**<!$Scr_H::1><!$Scr_Ps::0>

<$ScrKeepWithNext><$Scr_H::1><$Scr_Ps::0>
**Opening Image (0 to 1%): 

**<!$Scr_H::1><!$Scr_Ps::0>
.

<$ScrKeepWithNext><$Scr_H::1><$Scr_Ps::1>
**Demographic Information:

**<$ScrKeepWithNext><!$Scr_H::1><$Scr_H::2><!$Scr_Ps::1><$Scr_Ps::0>
**Name:

**<!$Scr_H::2><!$Scr_Ps::0>
The name you select will help you start thinking about this persona like a real person.
`

	cleaned := cleanScrivenerMarkdown(sample)
	t.Logf("Cleaned result:\n%s", cleaned)

	if strings.Contains(cleaned, "<$Scr") || strings.Contains(cleaned, "<!$Scr") {
		t.Errorf("cleaned contains Scrivener tags:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "# ACT ONE:") {
		t.Errorf("atteso H1 '# ACT ONE:', ottenuto:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "## Name:") {
		t.Errorf("atteso H2 '## Name:', ottenuto:\n%s", cleaned)
	}
}

func TestInspectTemplateClean(t *testing.T) {
	tmplPath := optionalScrivenerTemplate(t)
	book, err := importScrivenerVault(tmplPath)
	if err != nil {
		t.Fatal(err)
	}
	vaultMu.Lock()
	v := vaultDir
	vaultMu.Unlock()

	for i, ch := range book.Chapters {
		if i == 13 {
			data, err := os.ReadFile(filepath.Join(v, ch.File))
			if err != nil {
				t.Fatalf("lettura fallita per capitolo 13: %v", err)
			}
			content := string(data)
			if strings.Contains(content, "<$Scr") || strings.Contains(content, "<!$Scr") {
				t.Errorf("Capitolo 13 contiene tag Scrivener non puliti:\n%s", content[:min(len(content), 500)])
			}
			if !strings.Contains(content, "# ACT 1: SEPARATION") {
				t.Errorf("atteso '# ACT 1: SEPARATION' nel capitolo 13, ottenuto:\n%s", content[:min(len(content), 500)])
			}
		}
	}
}

func TestChapterStatsAndSummary(t *testing.T) {
	content := []byte(`---
id: 1234
title: Capitolo Segreto
summary: "Marco scopre la lettera in soffitta."
type: chapter
---

Nel silenzio della notte, la pioggia batteva contro i vetri.
Un rumore improvviso ruppe la quiete.
`)

	words, summary, st := parseChapterWordsAndSummary(content)
	if summary != "Marco scopre la lettera in soffitta." {
		t.Errorf("summary inatteso: %q", summary)
	}
	if st != "" {
		t.Errorf("status inatteso: %q", st)
	}
	expectedWords := 16 // "Nel silenzio della notte, la pioggia batteva contro i vetri. Un rumore improvviso ruppe la quiete."
	if words != expectedWords {
		t.Errorf("conteggio parole inatteso: %d (atteso %d)", words, expectedWords)
	}

	// Test con synopsis invece di summary
	content2 := []byte(`---
id: 5678
synopsis: Fuga attraverso i boschi
---
Correvano senza voltarsi indietro.`)
	words2, summary2, _ := parseChapterWordsAndSummary(content2)
	if summary2 != "Fuga attraverso i boschi" {
		t.Errorf("synopsis inatteso: %q", summary2)
	}
	if words2 != 4 {
		t.Errorf("conteggio parole inatteso: %d (atteso 4)", words2)
	}

	// Test populateChapterStats
	tmpDir := t.TempDir()
	ch1Path := filepath.Join(tmpDir, "01-cap1.md")
	ch2Path := filepath.Join(tmpDir, "02-cap2.md")
	if err := os.WriteFile(ch1Path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ch2Path, content2, 0o644); err != nil {
		t.Fatal(err)
	}

	bk := &Book{
		Title: "Test Book",
		Chapters: []Chapter{
			{File: "01-cap1.md", Title: "Capitolo 1"},
			{File: "02-cap2.md", Title: "Capitolo 2"},
		},
	}
	populateChapterStats(bk, tmpDir)
	if bk.TotalWords != expectedWords+4 {
		t.Errorf("totale parole libro errato: %d (atteso %d)", bk.TotalWords, expectedWords+4)
	}
	if bk.Chapters[0].Words != expectedWords || bk.Chapters[0].Summary != "Marco scopre la lettera in soffitta." {
		t.Errorf("stats capitolo 1 errate: %+v", bk.Chapters[0])
	}
	if bk.Chapters[1].Words != 4 || bk.Chapters[1].Summary != "Fuga attraverso i boschi" {
		t.Errorf("stats capitolo 2 errate: %+v", bk.Chapters[1])
	}
}
