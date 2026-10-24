package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBookYAMLSyntaxAndSchema(t *testing.T) {
	for _, text := range []string{
		"---\ntitle: Valid\nchapters:\n- file: 01.md\n  title: First\n...\n",
		"title: Empty book\nchapters: []\n",
		"title: 'L''autore: prova'\nchapters: [{file: 01.md, title: One}]\n",
	} {
		if _, err := decodeBookYAML([]byte(text)); err != nil {
			t.Fatalf("valid YAML %q rejected: %v", text, err)
		}
	}
	for _, text := range []string{
		"title: Invalid\nchapters: [\n",
		"title: Invalid\nchapters: not-a-list\n",
		"title: Invalid\nchapters: [{title: missing-file}]\n",
		"title: First\ntitle: Duplicate\n",
		"---\ntitle: One\n---\ntitle: Two\n",
		":::CORRUPT:::",
	} {
		if _, err := decodeBookYAML([]byte(text)); err == nil {
			t.Fatalf("invalid YAML accepted: %q", text)
		}
	}
}

func TestBookYAMLUserTextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "book.yaml")
	title := "L'autore: \"Una prova\" # non un commento"
	book := &Book{Title: title, Author: "A: B", Language: "it", Chapters: []Chapter{{File: "01.md", Title: title}}, ExtraFields: map[string]string{"note": "a: b\nseconda riga"}}
	if err := writeBookYAMLAt(file, book); err != nil {
		t.Fatal(err)
	}
	got, err := parseBookYAML(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || got.Author != book.Author || got.Chapters[0].Title != title || got.ExtraFields["note"] != book.ExtraFields["note"] {
		t.Fatalf("round trip changed text: %#v", got)
	}
}

func TestProjectGuardRejectsStaleAndMissingOrigin(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	setVault(b)
	called := false
	handler := projectGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	for _, endpoint := range []string{"newchapter", "deletechapter", "reorder", "setstyle", "setfont", "setopening", "setscenebreak", "addcover", "delcover", "useasepubcover"} {
		for _, origin := range []string{"", a} {
			r := httptest.NewRequest("POST", "/api/"+endpoint, strings.NewReader("{}"))
			r.Header.Set("X-PubCraft-Vault", origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusConflict || called {
				t.Fatalf("%s origin=%q status=%d called=%v", endpoint, origin, w.Code, called)
			}
		}
	}
	r := httptest.NewRequest("POST", "/api/deletechapter", strings.NewReader("{}"))
	r.Header.Set("X-PubCraft-Vault", b)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 || !called {
		t.Fatal("valid origin rejected")
	}
}

func TestProjectGuardLocksTransitionsAndWrites(t *testing.T) {
	for _, endpoint := range []string{"openat", "newproject", "import", "importdocx", "import-manuscript", "settitle", "note-to-chapter", "save", "build", "setstyle"} {
		setVault(t.TempDir())
		handler := projectGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if projectRequestMu.TryLock() {
				projectRequestMu.Unlock()
				t.Errorf("%s not serialized", endpoint)
			}
		}))
		r := httptest.NewRequest("POST", "/api/"+endpoint, nil)
		r.Header.Set("X-PubCraft-Vault", getVaultDir())
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
}

func TestOpenRejectsMalformedChapterListWithoutSwitching(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	setVault(a)
	if err := os.WriteFile(filepath.Join(b, "book.yaml"), []byte("title: Broken\nchapters: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/openat", strings.NewReader(`{"path":"`+filepath.ToSlash(b)+`"}`))
	w := httptest.NewRecorder()
	apiOpenAt(w, r)
	if w.Code != 400 || getVaultDir() != a {
		t.Fatalf("status=%d vault=%s", w.Code, getVaultDir())
	}
}
