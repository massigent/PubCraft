package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeVaultFile scrive un capitolo di test con front-matter minimale.
func writeVaultFile(t *testing.T, dir, name, id, title string) {
	t.Helper()
	fm := "---\n"
	if id != "" {
		fm += "id: " + id + "\n"
	}
	fm += "title: " + title + "\n---\n\n# " + title + "\n\nTesto del capitolo " + title + ".\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(fm), 0o644); err != nil {
		t.Fatalf("scrittura %s: %v", name, err)
	}
}

func writeTestBookYAML(t *testing.T, dir string, chapters [][2]string) {
	t.Helper()
	var s strings.Builder
	s.WriteString("title: \"Libro di prova\"\nauthor: \"Autore\"\nlanguage: \"it\"\nstyle: \"classico\"\nchapters:\n")
	for _, c := range chapters {
		id := "id-" + strings.TrimSuffix(c[0], filepath.Ext(c[0]))
		s.WriteString("  - file: \"" + c[0] + "\"\n    id: \"" + id + "\"\n    title: \"" + c[1] + "\"\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "book.yaml"), []byte(s.String()), 0o644); err != nil {
		t.Fatalf("scrittura book.yaml: %v", err)
	}
}

func bookFiles(b *Book) []string {
	out := make([]string, 0, len(b.Chapters))
	for _, c := range b.Chapters {
		out = append(out, c.File)
	}
	return out
}

func TestReconcileAppendsNewMarkdownFilesInNaturalOrder(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}, {"02.md", "Secondo"}})

	// File aggiunti a mano, in ordine non alfabetico di creazione.
	writeVaultFile(t, dir, "10.md", "id-10", "Decimo")
	writeVaultFile(t, dir, "3-nuovo.md", "id-03", "Terzo nuovo")

	b := loadBook()
	if b == nil {
		t.Fatal("loadBook ha restituito nil")
	}
	got := strings.Join(bookFiles(b), ",")
	want := "01.md,02.md,3-nuovo.md,10.md"
	if got != want {
		t.Fatalf("ordine capitoli = %s, atteso %s", got, want)
	}
	// L'ordine esistente non deve cambiare e i nuovi vanno in fondo.
	if b.Chapters[0].Title != "Primo" || b.Chapters[1].Title != "Secondo" {
		t.Fatalf("titoli esistenti alterati: %+v", b.Chapters[:2])
	}
	if b.Chapters[2].Title != "Terzo nuovo" || b.Chapters[3].Title != "Decimo" {
		t.Fatalf("titoli dei nuovi capitoli = %q / %q", b.Chapters[2].Title, b.Chapters[3].Title)
	}
	// Il file su disco deve contenere i nuovi capitoli.
	raw, err := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"3-nuovo.md", "10.md"} {
		if !strings.Contains(string(raw), f) {
			t.Fatalf("book.yaml non contiene %s:\n%s", f, raw)
		}
	}
}

func TestReconcileDetectsRenameByFrontMatterID(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")
	writeVaultFile(t, dir, "03.md", "id-03", "Terzo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}, {"02.md", "Secondo"}, {"03.md", "Terzo"}})

	// Rinomina fuori da PubCraft: stesso id, nome diverso.
	if err := os.Rename(filepath.Join(dir, "02.md"), filepath.Join(dir, "02-capitolo-secondo.md")); err != nil {
		t.Fatal(err)
	}

	b := loadBook()
	got := strings.Join(bookFiles(b), ",")
	want := "01.md,02-capitolo-secondo.md,03.md"
	if got != want {
		t.Fatalf("dopo la rinomina l'ordine = %s, atteso %s", got, want)
	}
	if b.Chapters[1].ID != "id-02" {
		t.Fatalf("id del capitolo rinominato = %q", b.Chapters[1].ID)
	}
	if b.Chapters[1].Missing {
		t.Fatal("il capitolo rinominato non deve risultare mancante")
	}
	if b.Chapters[1].Words == 0 {
		t.Fatal("il capitolo rinominato deve essere riletto da disco")
	}
}

func TestReconcileMarksMissingFileWithoutDeletingEntry(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}, {"02.md", "Secondo"}})

	if err := os.Remove(filepath.Join(dir, "02.md")); err != nil {
		t.Fatal(err)
	}

	b := loadBook()
	if len(b.Chapters) != 2 {
		t.Fatalf("capitoli = %d, atteso 2 (nessuna rimozione automatica)", len(b.Chapters))
	}
	if !b.Chapters[1].Missing {
		t.Fatal("il capitolo con file cancellato deve essere marcato come mancante")
	}
	if b.Chapters[0].Missing {
		t.Fatal("il capitolo con file presente non deve risultare mancante")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if !strings.Contains(string(raw), "02.md") {
		t.Fatalf("book.yaml non deve perdere la voce mancante:\n%s", raw)
	}
}

func TestReconcileIgnoresReservedHiddenAndNestedFiles(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}})

	// File che non devono mai diventare capitoli.
	os.WriteFile(filepath.Join(dir, "book.md"), []byte("# note\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".nascosto.md"), []byte("# hidden\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".cestina"), 0o755)
	os.WriteFile(filepath.Join(dir, ".cestina", "vecchio.md"), []byte("# cestinato\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sottocartella"), 0o755)
	os.WriteFile(filepath.Join(dir, "sottocartella", "altro.md"), []byte("# altro\n"), 0o644)

	b := loadBook()
	got := strings.Join(bookFiles(b), ",")
	if got != "01.md" {
		t.Fatalf("capitoli = %s, atteso solo 01.md", got)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}})
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")

	if b := loadBook(); b == nil || len(b.Chapters) != 2 {
		t.Fatalf("prima riconciliazione fallita: %+v", b)
	}
	first, err := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if b := loadBook(); b == nil || len(b.Chapters) != 2 {
		t.Fatal("seconda riconciliazione ha alterato l'elenco")
	}
	second, err := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("book.yaml riscritto senza modifiche:\n--- primo ---\n%s\n--- secondo ---\n%s", first, second)
	}
}

func TestForgetMissingChapterRemovesOnlyTheEntry(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}, {"02.md", "Secondo"}})
	os.Remove(filepath.Join(dir, "02.md"))

	body, _ := json.Marshal(map[string]string{"file": "02.md"})
	w := httptest.NewRecorder()
	apiForgetChapter(w, httptest.NewRequest("POST", "/api/forget-chapter", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("forget-chapter: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Book *Book `json:"book"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Book.Chapters) != 1 || resp.Book.Chapters[0].File != "01.md" {
		t.Fatalf("capitoli dopo la rimozione = %v", bookFiles(resp.Book))
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if strings.Contains(string(raw), "02.md") {
		t.Fatalf("book.yaml contiene ancora la voce rimossa:\n%s", raw)
	}
	// Il file 01.md non deve essere toccato.
	if _, err := os.Stat(filepath.Join(dir, "01.md")); err != nil {
		t.Fatal("il capitolo superstite è sparito dal disco")
	}
}

func TestForgetChapterRefusesWhenFileStillExists(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}})

	body, _ := json.Marshal(map[string]string{"file": "01.md"})
	w := httptest.NewRecorder()
	apiForgetChapter(w, httptest.NewRequest("POST", "/api/forget-chapter", bytes.NewReader(body)))
	if w.Code != 409 {
		t.Fatalf("atteso 409 con file presente, ottenuto %d", w.Code)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if !strings.Contains(string(raw), "01.md") {
		t.Fatal("la voce non deve essere rimossa quando il file esiste")
	}
}

func TestReconcileDoesNotResurrectTrashedChapter(t *testing.T) {
	dir := t.TempDir()
	setVault(dir)
	writeVaultFile(t, dir, "01.md", "id-01", "Primo")
	writeVaultFile(t, dir, "02.md", "id-02", "Secondo")
	writeTestBookYAML(t, dir, [][2]string{{"01.md", "Primo"}, {"02.md", "Secondo"}})

	if err := moveToTrash(dir, "02.md", filepath.Join(dir, "02.md")); err != nil {
		t.Fatalf("spostamento in cestina: %v", err)
	}
	b := loadBook()
	if len(b.Chapters) != 2 {
		t.Fatalf("capitoli = %d", len(b.Chapters))
	}
	if !b.Chapters[1].Missing {
		t.Fatal("il capitolo cestinato deve risultare mancante, non ricomparire")
	}
}
