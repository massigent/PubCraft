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
