package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func newRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

func newJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// ---------------------------------------------------------------------------
// Helper comuni: ambiente isolato, vault temporanei, esecuzione CLI C, ZIP.
// ---------------------------------------------------------------------------

func isolateTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PUBCRAFT_CONFIG_DIR", cfg)
	t.Cleanup(func() { setVault("") })
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeTestVault(t *testing.T, bookYAML string, chapters map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if bookYAML != "" {
		writeTestFile(t, filepath.Join(dir, "book.yaml"), bookYAML)
	}
	for name, content := range chapters {
		writeTestFile(t, filepath.Join(dir, name), content)
	}
	return dir
}

func optionalPubcraftBinary() (string, bool) {
	if p := os.Getenv("PUBCRAFT_BIN"); p != "" {
		return p, true
	}
	p := filepath.Join("..", "..", "build", "pubcraft")
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", false
	}
	if _, err := exec.LookPath("zip"); err != nil {
		return "", false
	}
	return p, true
}

func pubcraftBinary(t *testing.T) string {
	t.Helper()
	p, ok := optionalPubcraftBinary()
	if !ok {
		t.Skip("binario C o comando zip non disponibile: esegui make prima di go test")
	}
	return p
}

func runPubcraft(t *testing.T, bin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func readZipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("apertura EPUB fallita: %v", err)
	}
	defer zr.Close()
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("apertura %s fallita: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("lettura %s fallita: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func validateXMLEntries(t *testing.T, entries map[string][]byte) {
	t.Helper()
	for name, data := range entries {
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".xhtml") && !strings.HasSuffix(lower, ".opf") &&
			!strings.HasSuffix(lower, ".xml") && !strings.HasSuffix(lower, ".ncx") {
			continue
		}
		if !utf8.Valid(data) {
			t.Fatalf("%s non è UTF-8 valido", name)
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s non è XML valido: %v", name, err)
			}
		}
		for _, bad := range []byte{0x00, 0x01, 0x07, 0x0b, 0x0c, 0x1f} {
			if bytes.IndexByte(data, bad) >= 0 {
				t.Fatalf("%s contiene il carattere di controllo 0x%02x", name, bad)
			}
		}
	}
}

func opfIdentifier(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	opf, ok := entries["OEBPS/content.opf"]
	if !ok {
		t.Fatal("content.opf assente nell'EPUB")
	}
	re := regexp.MustCompile(`(?s)<dc:identifier[^>]*>(.*?)</dc:identifier>`)
	m := re.FindSubmatch(opf)
	if len(m) < 2 {
		t.Fatalf("dc:identifier assente: %s", opf)
	}
	return strings.TrimSpace(string(m[1]))
}

func countTopLevelUUIDLines(t *testing.T, data []byte) int {
	t.Helper()
	count, err := countUUIDEntries(data)
	if err != nil {
		t.Fatalf("analisi uuid fallita: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// Problema 1 — import/selezione non distruttivi.
// ---------------------------------------------------------------------------

func threeChapterBookYAML() string {
	return `title: "Libro Completo"
author: "Autrice"
language: it
custom_meta: "da preservare"
chapters:
  - file: "01.md"
    id: "legacy-id-uno"
    title: "Uno"
    type: "dedication"
  - file: "02.md"
    id: "legacy-id-due"
    title: "Due"
  - file: "03.md"
    id: "legacy-id-tre"
    title: "Tre"
    type: "epilogue"
`
}

func TestImportSelectedFilesPreservesExistingBook(t *testing.T) {
	isolateTestEnv(t)
	dir := makeTestVault(t, threeChapterBookYAML(), map[string]string{
		"01.md": "---\ntitle: Uno\n---\n\nTesto uno.\n",
		"02.md": "---\ntitle: Due\n---\n\nTesto due.\n",
		"03.md": "---\ntitle: Tre\n---\n\nTesto tre.\n",
	})
	yamlPath := filepath.Join(dir, "book.yaml")
	before, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedBefore, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	// (a) selezione singola di un capitolo già presente: nessuna modifica.
	bk, err := importSelectedFiles(dir, []string{"02.md"})
	if err != nil {
		t.Fatalf("importSelectedFiles: %v", err)
	}
	if len(bk.Chapters) != 3 {
		t.Fatalf("la selezione singola ha ridotto i capitoli: %+v", bk.Chapters)
	}
	for i, want := range []string{"01.md", "02.md", "03.md"} {
		if bk.Chapters[i].File != want {
			t.Fatalf("ordine alterato: %+v", bk.Chapters)
		}
		if bk.Chapters[i].ID != parsedBefore.Chapters[i].ID ||
			bk.Chapters[i].Title != parsedBefore.Chapters[i].Title ||
			bk.Chapters[i].Type != parsedBefore.Chapters[i].Type {
			t.Fatalf("metadati alterati: %+v", bk.Chapters[i])
		}
	}
	after, _ := os.ReadFile(yamlPath)
	if !bytes.Equal(before, after) {
		t.Fatalf("book.yaml modificato senza necessità:\nPRIMA:\n%s\nDOPO:\n%s", before, after)
	}

	// (b) selezione multipla che include un file nuovo: i capitoli esistenti
	// restano intatti e il nuovo viene accodato.
	writeTestFile(t, filepath.Join(dir, "04.md"), "---\ntitle: Quattro\n---\n\nTesto quattro.\n")
	bk, err = importSelectedFiles(dir, []string{"01.md", "03.md", "04.md"})
	if err != nil {
		t.Fatalf("importSelectedFiles con file nuovo: %v", err)
	}
	if len(bk.Chapters) != 4 {
		t.Fatalf("attesi 4 capitoli, trovati %+v", bk.Chapters)
	}
	for i := 0; i < 3; i++ {
		if bk.Chapters[i] != parsedBefore.Chapters[i] {
			t.Fatalf("capitolo esistente alterato in posizione %d: %+v != %+v", i, bk.Chapters[i], parsedBefore.Chapters[i])
		}
	}
	if bk.Chapters[3].File != "04.md" {
		t.Fatalf("file nuovo non accodato: %+v", bk.Chapters)
	}
	reloaded, err := parseBookYAML(yamlPath)
	if err != nil {
		t.Fatalf("book.yaml illeggibile dopo aggiunta: %v", err)
	}
	if len(reloaded.Chapters) != 4 || reloaded.Chapters[0].ID != parsedBefore.Chapters[0].ID {
		t.Fatalf("metadati non persistiti: %+v", reloaded.Chapters)
	}
	if reloaded.ExtraFields["custom_meta"] != "da preservare" ||
		!strings.Contains(string(mustReadFile(t, yamlPath)), "custom_meta:") {
		t.Fatal("campo custom perso")
	}

	// (c) ripetere la selezione non deve duplicare né riscrivere nulla.
	before2 := mustReadFile(t, yamlPath)
	if _, err := importSelectedFiles(dir, []string{"04.md", "02.md"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before2, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml riscritto da una selezione senza novità")
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImportSelectedFilesRejectsInvalidBookYAML(t *testing.T) {
	isolateTestEnv(t)
	invalid := "title: \"Rotto\"\nchapters:\n\t- file: \"01.md\"\n    title: \"Uno\"\n" // tab illegale
	dir := makeTestVault(t, invalid, map[string]string{
		"01.md": "testo uno",
		"02.md": "testo due",
	})
	yamlPath := filepath.Join(dir, "book.yaml")
	before := mustReadFile(t, yamlPath)

	if _, err := importSelectedFiles(dir, []string{"01.md"}); err == nil {
		t.Fatal("atteso errore su book.yaml invalido")
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da importSelectedFiles")
	}

	if b := autoInitVaultFromMarkdown(dir); b != nil {
		t.Fatalf("autoInit ha inizializzato una cartella con book.yaml esistente: %+v", b)
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da autoInitVaultFromMarkdown")
	}

	if _, err := importAnyManuscript(dir); err == nil {
		t.Fatal("importAnyManuscript ha accettato un book.yaml invalido")
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da importAnyManuscript")
	}

	if _, err := importMarkdownVault(filepath.Join(dir, "01.md")); err == nil {
		t.Fatal("importMarkdownVault ha accettato una cartella con book.yaml invalido")
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da importMarkdownVault")
	}

	// Endpoint HTTP che delegano a importSelectedFiles.
	body := fmt.Sprintf(`{"path":%q,"files":["01.md"]}`, dir)
	w := newRecorder()
	apiOpenAt(w, newJSONRequest("POST", "/api/openat", body))
	if w.Code == 200 {
		t.Fatal("apiOpenAt ha accettato un book.yaml invalido")
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da apiOpenAt")
	}

	body = fmt.Sprintf(`{"dir":%q,"files":["01.md"]}`, dir)
	w = newRecorder()
	apiImportManuscript(w, newJSONRequest("POST", "/api/import-manuscript", body))
	if w.Code == 200 {
		t.Fatal("apiImportManuscript ha accettato un book.yaml invalido")
	}
	if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
		t.Fatal("book.yaml invalido è stato modificato da apiImportManuscript")
	}
}

func TestImportSelectedFilesInitializesOnlyWhenMissing(t *testing.T) {
	isolateTestEnv(t)
	dir := makeTestVault(t, "", map[string]string{
		"02-secondo.md": "# Secondo\n\ntesto",
		"01-primo.md":   "# Primo\n\ntesto",
	})
	bk, err := importSelectedFiles(dir, []string{"02-secondo.md", "01-primo.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bk.Chapters) != 2 || bk.Chapters[0].File != "01-primo.md" || bk.Chapters[1].File != "02-secondo.md" {
		t.Fatalf("ordine naturale non rispettato: %+v", bk.Chapters)
	}
	if !isCanonicalUUID(bk.UUID) {
		t.Fatalf("uuid non canonico: %q", bk.UUID)
	}
	reloaded, err := parseBookYAML(filepath.Join(dir, "book.yaml"))
	if err != nil {
		t.Fatalf("book.yaml creato non valido: %v", err)
	}
	if len(reloaded.Chapters) != 2 {
		t.Fatalf("capitoli non persistiti: %+v", reloaded.Chapters)
	}
}

func TestAutoInitDoesNotTreatUnreadableAsMissing(t *testing.T) {
	isolateTestEnv(t)
	if os.Geteuid() == 0 {
		t.Skip("eseguito come root: i permessi non bloccano la lettura")
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "01.md"), "testo")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("chmod non supportato: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if b := autoInitVaultFromMarkdown(dir); b != nil {
		t.Fatalf("autoInit ha trattato un errore di permessi come cartella nuova: %+v", b)
	}
}

// ---------------------------------------------------------------------------
// Problema 2 — uuid persistente: assente/vuoto/null/esistente/duplicati.
// ---------------------------------------------------------------------------

func TestEnsureBookUUIDVariants(t *testing.T) {
	cases := []struct {
		name       string
		yaml       string
		wantUUID   string // "" = deve essere generato un UUID canonico
		lineDelta  int    // variazione attesa del numero di righe
		unchanged  bool   // il file non deve essere riscritto
		wantErr    bool
		keepSuffix string
	}{
		{
			name:      "assente",
			yaml:      "title: T\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 1,
		},
		{
			name:      "vuoto",
			yaml:      "title: T\nuuid: \"\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:      "null",
			yaml:      "title: T\nuuid: null\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:      "tilde",
			yaml:      "title: T\nuuid: ~\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:      "esistente",
			yaml:      "title: T\nuuid: 11111111-2222-4333-8444-555555555555\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantUUID:  "11111111-2222-4333-8444-555555555555",
			lineDelta: 0,
			unchanged: true,
		},
		{
			name:      "legacy",
			yaml:      "title: T\nuuid: idfdc4189b-d21e-43b3-8147-358684021d11\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantUUID:  "idfdc4189b-d21e-43b3-8147-358684021d11",
			lineDelta: 0,
			unchanged: true,
		},
		{
			name:      "null-tra-virgolette-letterale",
			yaml:      "title: T\nuuid: \"null\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantUUID:  "null",
			lineDelta: 0,
			unchanged: true,
		},
		{
			name:      "commento-in-linea",
			yaml:      "title: T\nuuid: \"\"  # da generare\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:      "commento-senza-valore",
			yaml:      "title: T\nuuid:   # da generare\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:       "commento-header",
			yaml:       "# commento iniziale\ntitle: T\nuuid: \"\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta:  0,
			keepSuffix: "# commento iniziale",
		},
		{
			name:       "terminatore-esplicito",
			yaml:       "title: T\nuuid: ~\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n...\n",
			lineDelta:  0,
			keepSuffix: "...",
		},
		{
			name:      "chiave-quotata-doppia",
			yaml:      "title: T\n\"uuid\": \"\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:      "chiave-quotata-singola-null",
			yaml:      "title: T\n'uuid': null\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			lineDelta: 0,
		},
		{
			name:       "valore-block-literal-strip",
			yaml:       "title: T\nuuid: |-\n  legacy-book-id\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantUUID:   "legacy-book-id",
			lineDelta:  0,
			keepSuffix: "  legacy-book-id",
		},
		{
			name:    "block-literal-non-strip",
			yaml:    "title: T\nuuid: |\n  legacy-book-id\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantErr: true,
		},
		{
			name:    "block-folded",
			yaml:    "title: T\nuuid: >-\n  legacy book\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantErr: true,
		},
		{
			name:    "plain-multiriga",
			yaml:    "title: T\nuuid: legacy\n  book-id\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantErr: true,
		},
		{
			name:    "chiave-esplicita",
			yaml:    "title: T\n? uuid\n: legacy-book-id\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n",
			wantErr: true,
		},
		{
			name:      "duplicato-vuoto-piu-valore",
			yaml:      "title: T\nuuid: \"\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\nuuid: 11111111-2222-4333-8444-555555555555\n",
			wantUUID:  "11111111-2222-4333-8444-555555555555",
			lineDelta: -1,
		},
		{
			name:      "duplicati-vuoti",
			yaml:      "title: T\nuuid: \"\"\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\nuuid: null\n",
			lineDelta: -1,
		},
		{
			name:    "duplicati-conflittuali",
			yaml:    "title: T\nuuid: 11111111-2222-4333-8444-555555555555\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\nuuid: 99999999-2222-4333-8444-555555555555\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "book.yaml")
			writeTestFile(t, path, tc.yaml)
			before := mustReadFile(t, path)
			if !tc.wantErr {
				if _, err := parseBookYAML(path); err != nil {
					t.Fatalf("fixture non leggibile prima della correzione: %v", err)
				}
			}
			b, err := parseBookYAML(path)
			if tc.wantErr {
				if err == nil {
					b = &Book{UUID: ""}
				}
			}
			if b == nil {
				b = &Book{}
			}
			err = ensureBookUUID(dir, b)
			if tc.wantErr {
				if err == nil {
					t.Fatal("atteso errore per uuid conflittuali")
				}
				if !bytes.Equal(before, mustReadFile(t, path)) {
					t.Fatal("file modificato nonostante l'errore")
				}
				return
			}
			if err != nil {
				t.Fatalf("ensureBookUUID: %v", err)
			}
			after := mustReadFile(t, path)
			if tc.unchanged && !bytes.Equal(before, after) {
				t.Fatalf("file modificato nonostante l'uuid già valido:\nPRIMA:\n%s\nDOPO:\n%s", before, after)
			}
			if got := countTopLevelUUIDLines(t, after); got != 1 {
				t.Fatalf("attesa esattamente 1 riga uuid, trovate %d:\n%s", got, after)
			}
			if got, want := len(yamlLines(after)), len(yamlLines(before))+tc.lineDelta; got != want {
				t.Fatalf("righe %d, attese %d:\n%s", got, want, after)
			}
			reloaded, err := parseBookYAML(path)
			if err != nil {
				t.Fatalf("book.yaml illeggibile dopo l'uuid: %v", err)
			}
			if tc.wantUUID != "" {
				if reloaded.UUID != tc.wantUUID {
					t.Fatalf("uuid cambiato: %q -> %q", tc.wantUUID, reloaded.UUID)
				}
			} else if !isCanonicalUUID(reloaded.UUID) {
				t.Fatalf("uuid generato non canonico: %q", reloaded.UUID)
			}
			if tc.keepSuffix != "" && !strings.Contains(string(after), tc.keepSuffix) {
				t.Fatalf("%q perso:\n%s", tc.keepSuffix, after)
			}
			if strings.Contains(tc.yaml, "# da generare") && !strings.Contains(string(after), "# da generare") {
				t.Fatalf("commento in linea perso:\n%s", after)
			}
			// Idempotenza: la seconda chiamata non tocca il file.
			prev := mustReadFile(t, path)
			if err := ensureBookUUID(dir, b); err != nil {
				t.Fatalf("seconda ensureBookUUID: %v", err)
			}
			if !bytes.Equal(prev, mustReadFile(t, path)) {
				t.Fatal("ensureBookUUID non è idempotente")
			}
		})
	}
}

func TestWriteBookYAMLPreservesQuotedUUID(t *testing.T) {
	for _, id := range []string{"null", "my: legacy", "id con spazi"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "book.yaml")
		writeTestFile(t, path, "title: T\nuuid: "+yamlScalar(id)+"\nchapters:\n- file: 01.md\n  title: Uno\n")
		b, err := parseBookYAML(path)
		if err != nil {
			t.Fatalf("parse %q: %v", id, err)
		}
		if b.UUID != id {
			t.Fatalf("uuid letto %q, atteso %q", b.UUID, id)
		}
		if err := writeBookYAMLAt(path, b); err != nil {
			t.Fatal(err)
		}
		reloaded, err := parseBookYAML(path)
		if err != nil {
			t.Fatalf("reload %q: %v", id, err)
		}
		if reloaded.UUID != id {
			t.Fatalf("uuid persistente alterato: %q -> %q\n%s", id, reloaded.UUID, mustReadFile(t, path))
		}
	}
}

func TestEnsureBookUUIDWriteFailureIsExplicit(t *testing.T) {
	isolateTestEnv(t)
	if os.Geteuid() == 0 {
		t.Skip("eseguito come root: i permessi non bloccano la scrittura")
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "book.yaml"), "title: T\nuuid: \"\"\nchapters:\n- file: 01.md\n  title: Uno\n")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("chmod non supportato: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	b := &Book{UUID: ""}
	if err := ensureBookUUID(dir, b); err == nil {
		t.Fatal("atteso errore di scrittura esplicito")
	}
	if got := mustReadFile(t, filepath.Join(dir, "book.yaml")); !strings.Contains(string(got), `uuid: ""`) {
		t.Fatalf("file modificato nonostante l'errore:\n%s", got)
	}
}

func TestEnsureBookUUIDKeepsSourceRevisionStable(t *testing.T) {
	isolateTestEnv(t)
	dir := makeTestVault(t, "title: T\nauthor: A\nuuid: \"\"\nchapters:\n- file: 01.md\n  title: Uno\n", map[string]string{
		"01.md": "testo",
	})
	b, err := parseBookYAML(filepath.Join(dir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rev1, err := computeSourceRevision(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureBookUUID(dir, b); err != nil {
		t.Fatal(err)
	}
	rev2, err := computeSourceRevision(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rev1 == rev2 {
		t.Fatal("la persistenza dell'uuid non ha modificato il file (attesa una modifica una tantum)")
	}
	before := mustReadFile(t, filepath.Join(dir, "book.yaml"))
	if err := ensureBookUUID(dir, b); err != nil {
		t.Fatal(err)
	}
	rev3, err := computeSourceRevision(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rev2 != rev3 {
		t.Fatal("revisione modificata da una seconda ensureBookUUID")
	}
	if !bytes.Equal(before, mustReadFile(t, filepath.Join(dir, "book.yaml"))) {
		t.Fatal("file riscritto dalla seconda ensureBookUUID")
	}
}

func TestPublicationPreviewPersistsUUIDBeforeRevision(t *testing.T) {
	isolateTestEnv(t)
	dir := makeTestVault(t, "title: T\nauthor: A\nuuid: \"\"\nchapters:\n- file: 01.md\n  title: Uno\n", map[string]string{
		"01.md": "testo",
	})
	setVault(dir)
	oldTool := toolPath
	t.Cleanup(func() { toolPath = oldTool; setVault("") })
	// Preferisce la CLI C reale (percorso completo: persistenza uuid reale);
	// in assenza del binario usa un finto tool che produce l'artefatto.
	if bin, ok := optionalPubcraftBinary(); ok {
		toolPath = bin
	} else {
		tool := filepath.Join(t.TempDir(), "fake-pubcraft")
		writeTestFile(t, tool, "#!/bin/sh\nout=\"$3\"\nmkdir -p \"$(dirname \"$out\")\"\n: > \"$out\"\nexit 0\n")
		if err := os.Chmod(tool, 0o755); err != nil {
			t.Fatal(err)
		}
		toolPath = tool
	}
	body := fmt.Sprintf(`{"vault":%q,"target":"ebook","request_id":"req-1"}`, dir)
	for i := 0; i < 2; i++ {
		w := newRecorder()
		req := newJSONRequest("POST", "/api/publication-preview", body)
		req.Header.Set("X-PubCraft-Vault", dir)
		apiPublicationPreview(w, req)
		if w.Code != 200 {
			t.Fatalf("preview %d: status %d body %s", i, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "sorgenti sono state modificate") {
			t.Fatalf("preview %d: warning spurio di sorgenti modificate: %s", i, w.Body.String())
		}
	}
	if got := countTopLevelUUIDLines(t, mustReadFile(t, filepath.Join(dir, "book.yaml"))); got != 1 {
		t.Fatalf("attesa una sola chiave uuid, trovate %d", got)
	}
	if _, err := parseBookYAML(filepath.Join(dir, "book.yaml")); err != nil {
		t.Fatalf("book.yaml non leggibile dopo la preview: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Problema 3 — EPUB: XML valido anche con input non valido.
// ---------------------------------------------------------------------------

func TestEpubXMLValidityWithHostileInput(t *testing.T) {
	bin := pubcraftBinary(t)
	isolateTestEnv(t)
	dir := t.TempDir()
	body := "Testo con FF \x0c, VT \x0b, BEL \x07, byte Latin-1 \xe8 isolato.\n\n" +
		"**grassetto\x0c** e `codice\x0b` e [link\x01t](https://example.com).\n\n" +
		"Unicode valido: emoji 😀, accenti àèìòù, simboli A & B < C > D \"E\".\n\n" +
		"![didascalia\x0c](images/pic.png)\n"
	writeTestFile(t, filepath.Join(dir, "01.md"), "---\ntitle: \"Capitolo\x0bUno\"\n---\n\n"+body)
	writeTestFile(t, filepath.Join(dir, "book.yaml"),
		"title: \"Titolo\x0ccon FF \xe8\"\nauthor: \"Autore\x07con BEL\"\nlanguage: it\n"+
			"chapters:\n  - file: \"01.md\"\n    title: \"Cap\x0bUno\"\n")
	// PNG minimo valido (1x1 trasparente).
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R', 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "pic.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "book.epub")
	_, stderr, err := runPubcraft(t, bin, "build", dir, out, "--kindle")
	if err != nil {
		t.Fatalf("build fallita: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "non valido per XML/UTF-8") {
		t.Fatalf("sostituzione non segnalata su stderr:\n%s", stderr)
	}
	entries := readZipEntries(t, out)
	validateXMLEntries(t, entries)
	chap := entries["OEBPS/chap001.xhtml"]
	if !bytes.Contains(chap, []byte("😀")) || !bytes.Contains(chap, []byte("àèìòù")) {
		t.Fatal("Unicode valido non conservato")
	}
	if !bytes.Contains(chap, []byte("A &amp; B")) || bytes.Contains(chap, []byte("&amp;amp;")) {
		t.Fatalf("escaping errato:\n%s", chap)
	}
	if !bytes.Contains(chap, []byte("\xef\xbf\xbd")) {
		t.Fatal("il byte UTF-8 invalido non è stato sostituito con U+FFFD")
	}
}

// ---------------------------------------------------------------------------
// Problema 4 — liste annidate strutturalmente valide (2 e 4 spazi).
// ---------------------------------------------------------------------------

type listStructure struct {
	items      map[string]int // testo -> livello (1 = lista top-level)
	emptyLists []string
	badParents []string
}

func analyzeListStructure(t *testing.T, markup []byte) listStructure {
	t.Helper()
	re := regexp.MustCompile(`<(/?)(ul|ol|li)>`)
	matches := re.FindAllSubmatchIndex(markup, -1)
	res := listStructure{items: map[string]int{}}
	var stack []string // solo ul/ol/li
	var listChildren []int
	listDepth := 0
	nextStart := func(i int) int {
		if i+1 < len(matches) {
			return matches[i+1][0]
		}
		return len(markup)
	}
	for i, m := range matches {
		closing := m[2] != -1 && m[3] > m[2]
		tag := string(markup[m[4]:m[5]])
		if !closing {
			switch tag {
			case "ul", "ol":
				parent := ""
				if len(stack) > 0 {
					parent = stack[len(stack)-1]
				}
				if parent != "" && parent != "li" {
					res.badParents = append(res.badParents, fmt.Sprintf("%s dentro %s", tag, parent))
				}
				stack = append(stack, tag)
				listChildren = append(listChildren, 0)
				listDepth++
			case "li":
				if listDepth == 0 {
					res.badParents = append(res.badParents, "li fuori da una lista")
					continue
				}
				listChildren[len(listChildren)-1]++
				stack = append(stack, "li")
				text := strings.TrimSpace(string(markup[m[1]:nextStart(i)]))
				res.items[text] = listDepth
			}
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1] != tag {
			res.badParents = append(res.badParents, "chiusura inattesa: "+tag)
			continue
		}
		if tag == "ul" || tag == "ol" {
			if listDepth == 0 || len(listChildren) == 0 {
				res.badParents = append(res.badParents, "lista non aperta: "+tag)
				continue
			}
			if listChildren[len(listChildren)-1] == 0 {
				res.emptyLists = append(res.emptyLists, tag)
			}
			listChildren = listChildren[:len(listChildren)-1]
			listDepth--
		}
		stack = stack[:len(stack)-1]
	}
	return res
}

func TestNestedListStructure(t *testing.T) {
	bin := pubcraftBinary(t)
	isolateTestEnv(t)
	cases := []struct {
		name     string
		markdown string
		items    map[string]int
	}{
		{
			name:     "due-spazi",
			markdown: "- Alfa\n  - Beta\n    - Gamma\n  - Delta\n- Epsilon\n",
			items:    map[string]int{"Alfa": 1, "Beta": 2, "Gamma": 3, "Delta": 2, "Epsilon": 1},
		},
		{
			name:     "quattro-spazi",
			markdown: "- Alfa\n    - Beta\n        - Gamma\n    - Delta\n- Epsilon\n",
			items:    map[string]int{"Alfa": 1, "Beta": 2, "Gamma": 3, "Delta": 2, "Epsilon": 1},
		},
		{
			name:     "mista-e-rientro",
			markdown: "1. Primo\n   - Beta\n     1. Gamma\n2. Secondo\n\nParagrafo dopo.\n\n- Nuova lista\n",
			items:    map[string]int{"Primo": 1, "Beta": 2, "Gamma": 3, "Secondo": 1, "Nuova lista": 1},
		},
		{
			name:     "continuazione-lazy",
			markdown: "- Alfa\ncontinuazione lazy\n- Beta\n",
			items:    map[string]int{"Alfa continuazione lazy": 1, "Beta": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "book.yaml"),
				"title: L\nlanguage: it\nchapters:\n  - file: \"01.md\"\n    title: Uno\n")
			writeTestFile(t, filepath.Join(dir, "01.md"), "---\ntitle: Uno\n---\n\n"+tc.markdown)
			out := filepath.Join(t.TempDir(), "book.epub")
			if _, stderr, err := runPubcraft(t, bin, "build", dir, out); err != nil {
				t.Fatalf("build EPUB: %v\n%s", err, stderr)
			}
			entries := readZipEntries(t, out)
			validateXMLEntries(t, entries)
			epub := analyzeListStructure(t, entries["OEBPS/chap001.xhtml"])
			if len(epub.emptyLists) > 0 {
				t.Fatalf("EPUB: contenitori vuoti %v", epub.emptyLists)
			}
			if len(epub.badParents) > 0 {
				t.Fatalf("EPUB: struttura non valida %v", epub.badParents)
			}
			for text, depth := range tc.items {
				if got, ok := epub.items[text]; !ok || got != depth {
					t.Fatalf("EPUB: %q livello %d (atteso %d); trovati %+v", text, got, depth, epub.items)
				}
			}

			// Stessa logica condivisa dal renderer di stampa.
			printDir := filepath.Join(t.TempDir(), "print")
			if _, stderr, err := runPubcraft(t, bin, "build-print", dir, printDir, "--trim", "6x9", "--pages", "100"); err != nil {
				t.Fatalf("build-print: %v\n%s", err, stderr)
			}
			printHTML := mustReadFile(t, filepath.Join(printDir, "print.html"))
			html := analyzeListStructure(t, printHTML)
			if len(html.emptyLists) > 0 {
				t.Fatalf("print: contenitori vuoti %v", html.emptyLists)
			}
			if len(html.badParents) > 0 {
				t.Fatalf("print: struttura non valida %v", html.badParents)
			}
			for text, depth := range tc.items {
				if got, ok := html.items[text]; !ok || got != depth {
					t.Fatalf("print: %q livello %d (atteso %d); trovati %+v", text, got, depth, html.items)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Problema 5 — UUID canonici e identificatore di pubblicazione.
// ---------------------------------------------------------------------------

func TestCBookUUIDUnsupportedFormsRejected(t *testing.T) {
	bin := pubcraftBinary(t)
	isolateTestEnv(t)
	cases := []struct {
		name     string
		uuidLine string
	}{
		{name: "literal-clip", uuidLine: "uuid: |\n  legacy-book-id\n"},
		{name: "literal-keep", uuidLine: "uuid: |+\n  legacy-book-id\n"},
		{name: "folded-strip", uuidLine: "uuid: >-\n  legacy book\n"},
		{name: "plain-multiriga", uuidLine: "uuid: legacy\n  book-id\n"},
		{name: "chiave-esplicita", uuidLine: "? uuid\n: legacy-book-id\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			yamlPath := filepath.Join(dir, "book.yaml")
			writeTestFile(t, yamlPath,
				"title: L\nlanguage: it\n"+tc.uuidLine+"chapters:\n  - file: \"01.md\"\n    title: Uno\n")
			writeTestFile(t, filepath.Join(dir, "01.md"), "---\ntitle: U\n---\n\nTesto.\n")
			before := mustReadFile(t, yamlPath)
			out := filepath.Join(t.TempDir(), "book.epub")
			if _, _, err := runPubcraft(t, bin, "build", dir, out); err == nil {
				t.Fatalf("build doveva essere rifiutata per forma non supportata")
			}
			if !bytes.Equal(before, mustReadFile(t, yamlPath)) {
				t.Fatalf("book.yaml modificato nonostante la forma non supportata:\n%s", mustReadFile(t, yamlPath))
			}
		})
	}
}

func TestPublicationPreviewQuotedUUIDKey(t *testing.T) {
	isolateTestEnv(t)
	dir := makeTestVault(t, "title: T\nauthor: A\n\"uuid\": \"\"\nchapters:\n- file: 01.md\n  title: Uno\n", map[string]string{
		"01.md": "testo",
	})
	setVault(dir)
	oldTool := toolPath
	t.Cleanup(func() { toolPath = oldTool; setVault("") })
	if bin, ok := optionalPubcraftBinary(); ok {
		toolPath = bin
	} else {
		t.Skip("serve la CLI C per la preview ebook")
	}
	body := fmt.Sprintf(`{"vault":%q,"target":"ebook","request_id":"req-q"}`, dir)
	for i := 0; i < 2; i++ {
		w := newRecorder()
		req := newJSONRequest("POST", "/api/publication-preview", body)
		req.Header.Set("X-PubCraft-Vault", dir)
		apiPublicationPreview(w, req)
		if w.Code != 200 {
			t.Fatalf("preview %d: status %d body %s", i, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "sorgenti sono state modificate") {
			t.Fatalf("preview %d: warning spurio", i)
		}
	}
	data := mustReadFile(t, filepath.Join(dir, "book.yaml"))
	if n, err := countUUIDEntries(data); err != nil || n != 1 {
		t.Fatalf("attesa una sola chiave uuid (n=%d, err=%v):\n%s", n, err, data)
	}
	if !bytes.Contains(data, []byte(`"uuid":`)) {
		t.Fatalf("la chiave quotata è stata persa:\n%s", data)
	}
	if _, err := parseBookYAML(filepath.Join(dir, "book.yaml")); err != nil {
		t.Fatalf("book.yaml non leggibile dopo la preview: %v", err)
	}
}

var canonicalV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDCanonicalV4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := newUUID()
		if err != nil {
			t.Fatal(err)
		}
		if !canonicalV4Re.MatchString(id) {
			t.Fatalf("uuid non canonico/v4: %q", id)
		}
		if strings.HasPrefix(id, "id") {
			t.Fatalf("uuid con prefisso legacy: %q", id)
		}
		if seen[id] {
			t.Fatalf("uuid duplicato: %q", id)
		}
		seen[id] = true
	}
}

func TestPublicationUUIDPolicy(t *testing.T) {
	cases := []struct {
		stored string
		want   string
		ok     bool
	}{
		{"11111111-2222-4333-8444-555555555555", "11111111-2222-4333-8444-555555555555", true},
		{"idfdc4189b-d21e-43b3-8147-358684021d11", "fdc4189b-d21e-43b3-8147-358684021d11", true},
		{"IDFDC4189B-D21E-43B3-8147-358684021D11", "fdc4189b-d21e-43b3-8147-358684021d11", true},
		{"my-legacy-id", "", false},
		{"", "", false},
		{"not-a-uuid", "", false},
	}
	for _, tc := range cases {
		got, ok := publicationUUID(tc.stored)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("publicationUUID(%q) = %q,%v; atteso %q,%v", tc.stored, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCBookUUIDStableAndPublicationIdentifier(t *testing.T) {
	bin := pubcraftBinary(t)
	isolateTestEnv(t)
	chapter := "---\ntitle: U\n---\n\nTesto.\n"
	cases := []struct {
		name         string
		uuidLine     string
		wantStored   string
		wantOPFIdent string // "" = urn:uuid:<uuid letto da book.yaml>
		unchanged    bool
	}{
		{name: "assente"},
		{name: "vuoto", uuidLine: "uuid: \"\"\n"},
		{name: "null", uuidLine: "uuid: null\n"},
		{name: "commento-senza-valore", uuidLine: "uuid:   # da generare\n"},
		{name: "esistente", uuidLine: "uuid: 11111111-2222-4333-8444-555555555555\n", wantStored: "11111111-2222-4333-8444-555555555555", unchanged: true},
		{name: "legacy-id", uuidLine: "uuid: idfdc4189b-d21e-43b3-8147-358684021d11\n", wantStored: "idfdc4189b-d21e-43b3-8147-358684021d11", wantOPFIdent: "urn:uuid:fdc4189b-d21e-43b3-8147-358684021d11", unchanged: true},
		{name: "legacy-arbitrario", uuidLine: "uuid: my-legacy-id\n", wantStored: "my-legacy-id", wantOPFIdent: "my-legacy-id", unchanged: true},
		{name: "chiave-quotata-doppia", uuidLine: "\"uuid\": \"\"\n"},
		{name: "chiave-quotata-singola", uuidLine: "'uuid': 'legacy-book-id'\n", wantStored: "legacy-book-id", wantOPFIdent: "legacy-book-id", unchanged: true},
		{name: "block-literal-strip", uuidLine: "uuid: |-\n  legacy-book-id\n", wantStored: "legacy-book-id", wantOPFIdent: "legacy-book-id", unchanged: true},
		{name: "block-literal-strip-uuid", uuidLine: "uuid: |-\n  11111111-2222-4333-8444-555555555555\n", wantStored: "11111111-2222-4333-8444-555555555555", wantOPFIdent: "urn:uuid:11111111-2222-4333-8444-555555555555", unchanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "book.yaml"),
				"title: L\nlanguage: it\n"+tc.uuidLine+"chapters:\n  - file: \"01.md\"\n    title: Uno\n")
			writeTestFile(t, filepath.Join(dir, "01.md"), chapter)
			initialYAML := mustReadFile(t, filepath.Join(dir, "book.yaml"))
			var first string
			for i := 0; i < 2; i++ {
				out := filepath.Join(t.TempDir(), "book.epub")
				if _, stderr, err := runPubcraft(t, bin, "build", dir, out); err != nil {
					t.Fatalf("build %d: %v\n%s", i, err, stderr)
				}
				entries := readZipEntries(t, out)
				validateXMLEntries(t, entries)
				id := opfIdentifier(t, entries)
				book, err := parseBookYAML(filepath.Join(dir, "book.yaml"))
				if err != nil {
					t.Fatalf("book.yaml non leggibile dopo la build: %v", err)
				}
				want := tc.wantOPFIdent
				if want == "" {
					if !canonicalV4Re.MatchString(book.UUID) {
						t.Fatalf("uuid generato non canonico v4: %q", book.UUID)
					}
					want = "urn:uuid:" + book.UUID
				}
				if id != want {
					t.Fatalf("identificatore OPF %q, atteso %q", id, want)
				}
				if i == 0 {
					first = id
				} else if id != first {
					t.Fatalf("identificatore non stabile tra build: %q -> %q", first, id)
				}
			}
			data := mustReadFile(t, filepath.Join(dir, "book.yaml"))
			if tc.unchanged && !bytes.Equal(initialYAML, data) {
				t.Fatalf("book.yaml modificato nonostante l'uuid già valido:\nPRIMA:\n%s\nDOPO:\n%s", initialYAML, data)
			}
			if got := countTopLevelUUIDLines(t, data); got != 1 {
				t.Fatalf("attesa una sola chiave uuid, trovate %d:\n%s", got, data)
			}
			book, err := parseBookYAML(filepath.Join(dir, "book.yaml"))
			if err != nil {
				t.Fatalf("book.yaml non leggibile dopo la build: %v", err)
			}
			if tc.wantStored != "" && book.UUID != tc.wantStored {
				t.Fatalf("uuid esistente modificato: %q -> %q", tc.wantStored, book.UUID)
			}
		})
	}
}
