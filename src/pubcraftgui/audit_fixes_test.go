package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"time"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtraNumericBoolPreserved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "book.yaml")
	orig := "title: T\nlanguage: it\nmycount: 5\nmyflag: yes\nmyratio: 1.5\nmystr: 'a: b'\nchapters:\n- file: 01.md\n  title: Uno\n"
	if err := os.WriteFile(file, []byte(orig), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := parseBookYAML(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBookYAMLAt(file, b); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	got := string(data)
	for _, want := range []string{"mycount: 5\n", "myflag: yes\n", "myratio: 1.5\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("metadato non preservato %q in:\n%s", want, got)
		}
	}
	b2, err := parseBookYAML(file)
	if err != nil {
		t.Fatal(err)
	}
	if b2.ExtraFields["mycount"] != "5" || b2.ExtraFields["myflag"] != "yes" {
		t.Fatalf("valori alterati: %#v", b2.ExtraFields)
	}
}

func TestChapterPathRejected(t *testing.T) {
	for _, bad := range []string{"../secret.md", "/etc/passwd", "sub/01.md", "..", ".", "a\\b.md", ".hidden.md", "note.txt"} {
		if isVaultChapterName(bad) {
			t.Fatalf("percorso capitolo accettato: %q", bad)
		}
		if _, err := decodeBookYAML([]byte("title: T\nchapters:\n- file: " + bad + "\n  title: X\n")); err == nil {
			t.Fatalf("book.yaml con capitolo %q accettato", bad)
		}
	}
	if !isVaultChapterName("01-capitolo.md") || !isVaultChapterName("a.markdown") {
		t.Fatal("nomi capitolo legittimi rifiutati")
	}
}

func TestCapitalizeFirstMultibyte(t *testing.T) {
	if got := capitalizeFirst("èra libro"); got != "Èra libro" {
		t.Fatalf("got %q", got)
	}
	if got := capitalizeFirst("über"); got != "Über" {
		t.Fatalf("got %q", got)
	}
	if capitalizeFirst("") != "" {
		t.Fatal("stringa vuota")
	}
}

func TestSplitFrontmatterOnlyAtStart(t *testing.T) {
	head, body, ok := splitFrontmatter("---\ntitle: T\n---\n\ncorpo\n\n---\n\nHR\n")
	if !ok || !strings.Contains(head, "title: T") || !strings.Contains(body, "corpo") {
		t.Fatalf("head=%q body=%q ok=%v", head, body, ok)
	}
	if _, _, ok := splitFrontmatter("testo\n\n---\n\n# Titolo\n"); ok {
		t.Fatal("HR scambiata per frontmatter")
	}
	if _, _, ok := splitFrontmatter("\xef\xbb\xbf---\ntitle: T\n---\ncorpo\n"); !ok {
		t.Fatal("BOM non gestito")
	}
}

func TestJoinMarkdownParasFences(t *testing.T) {
	out := joinMarkdownParas([]string{"para uno", "```", "codice", "```", "para due"})
	if !strings.Contains(out, "```\ncodice\n```") {
		t.Fatalf("fence diviso:\n%s", out)
	}
	if !strings.Contains(out, "para uno\n\n```") {
		t.Fatalf("separazione errata:\n%s", out)
	}
}

func TestUpdateCoverImageRefPreserves(t *testing.T) {
	md := "---\ntitle: X\n---\n\nTesto utente.\n\n![](images/vecchia.png)\n"
	updated := updateCoverImageRef(md, "images/nuova.png")
	if !strings.Contains(updated, "Testo utente.") || !strings.Contains(updated, "![](images/nuova.png)") {
		t.Fatalf("contenuto perso:\n%s", updated)
	}
	if strings.Contains(updated, "vecchia") {
		t.Fatalf("vecchio riferimento rimasto:\n%s", updated)
	}
	if got := updateCoverImageRef(md, "images/vecchia.png"); got != "" {
		t.Fatalf("doveva essere no-op, got %q", got)
	}
}

func TestDocxSkipsDeletedText(t *testing.T) {
	xml := `<w:document xmlns:w="http://x"><w:body><w:p><w:r><w:t>tenuto </w:t></w:r><w:del><w:r><w:t>eliminato</w:t></w:r></w:del><w:r><w:t>fine</w:t></w:r></w:p></w:body></w:document>`
	paras := docxParseParas([]byte(xml))
	if len(paras) != 1 {
		t.Fatalf("paras=%d", len(paras))
	}
	got := docxParaMarkdown(paras[0], map[string]string{})
	if strings.Contains(got, "eliminato") || !strings.Contains(got, "tenuto") {
		t.Fatalf("testo eliminato importato: %q", got)
	}
}

func TestRequiredNoDirtyClientBlocks(t *testing.T) {
	setVault(t.TempDir())
	dirtyClients["t1"] = clientDirtyEntry{Dirty: true, Description: "x", UpdatedAt: time.Now()}
	defer delete(dirtyClients, "t1")
	w := httptest.NewRecorder()
	if requireNoDirtyClient(w) {
		t.Fatal("cambio progetto consentito con client dirty")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, atteso 409", w.Code)
	}
	delete(dirtyClients, "t1")
	w2 := httptest.NewRecorder()
	if !requireNoDirtyClient(w2) {
		t.Fatal("cambio progetto bloccato senza dirty")
	}
}
