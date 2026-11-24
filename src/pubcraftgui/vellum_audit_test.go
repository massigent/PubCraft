package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNewChapterFrontmatterTitleRoundTrip(t *testing.T) {
	for _, kind := range []string{"chapter", "copyright", "dedication", "epigraph", "prologue", "epilogue", "about-author", "acknowledgments", "preface"} {
		for _, title := range []string{"Parte: la svolta", "Titolo # intero", "La svolta\nstatus: pubblicato", `Un titolo "citato"`} {
			t.Run(kind+"/"+title, func(t *testing.T) {
				dir := t.TempDir()
				setVault(dir)
				writeTestFile(t, filepath.Join(dir, "book.yaml"), "title: Audit\nauthor: Test\nchapters: []\n")
				body, _ := json.Marshal(map[string]string{"title": title, "type": kind})
				w := httptest.NewRecorder()
				apiNewChapter(w, httptest.NewRequest("POST", "/api/newchapter", bytes.NewReader(body)))
				if w.Code != 200 {
					t.Fatalf("create: %d %s", w.Code, w.Body.String())
				}
				var response struct {
					File string `json:"file"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(filepath.Join(dir, response.File))
				if err != nil {
					t.Fatal(err)
				}
				head, _, ok := splitFrontmatter(string(raw))
				if !ok {
					t.Fatal("missing frontmatter")
				}
				head = strings.TrimSuffix(strings.TrimPrefix(head, "---\n"), "---\n")
				var meta map[string]any
				if err := yaml.Unmarshal([]byte(head), &meta); err != nil {
					t.Fatalf("invalid frontmatter: %v\n%s", err, head)
				}
				if meta["title"] != title {
					t.Errorf("title changed: got %#v want %q", meta["title"], title)
				}
				if meta["status"] == "pubblicato" {
					t.Error("title injected a metadata field")
				}
			})
		}
	}
}

func TestPrintCopyrightFrontmatter(t *testing.T) {
	bin := pubcraftBinary(t)
	for _, explicit := range []bool{false, true} {
		name := "fallback"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			chapters := ""
			if explicit {
				chapters += "  - file: rights.md\n    title: Diritti\n    type: copyright\n"
				writeTestFile(t, filepath.Join(dir, "rights.md"), "---\ntitle: Diritti\ntype: copyright\n---\n\nDIRITTI RISERVATI SINTETICI\n\nSecondo paragrafo.\n")
			}
			chapters += "  - file: chapter.md\n    title: Capitolo\n"
			writeTestFile(t, filepath.Join(dir, "book.yaml"), "title: Audit\nauthor: Test\nlanguage: it\nchapters:\n"+chapters)
			writeTestFile(t, filepath.Join(dir, "chapter.md"), "Testo del capitolo.\n")
			out := filepath.Join(dir, "out")
			if _, stderr, err := runPubcraft(t, bin, "build-print", dir, out); err != nil {
				t.Fatalf("build-print: %v %s", err, stderr)
			}
			data, err := os.ReadFile(filepath.Join(out, "print.html"))
			if err != nil {
				t.Fatal(err)
			}
			html := string(data)
			placeholder := "Completa questa pagina prima della stampa."
			if !explicit {
				if strings.Count(html, placeholder) != 1 {
					t.Fatal("missing generic copyright fallback")
				}
				return
			}
			if strings.Contains(html, placeholder) {
				t.Error("generic copyright duplicated explicit colophon")
			}
			if !strings.Contains(html, `<div class="copyright custom-copyright" id="ch1">`) {
				t.Error("copyright not styled as frontmatter")
			}
			if strings.Contains(html, `href="#ch1"`) {
				t.Error("copyright included in print TOC")
			}
			start := strings.Index(html, `id="ch1">`)
			end := strings.Index(html, `id="ch2">`)
			if start < 0 || end < start {
				t.Fatal("chapter boundaries absent")
			}
			colophon := html[start:end]
			if !strings.Contains(colophon, "DIRITTI RISERVATI SINTETICI") {
				t.Error("copyright content altered")
			}
			if strings.Contains(colophon, `class="dropcap"`) || strings.Contains(colophon, "PCMAP-") {
				t.Error("copyright contains chapter ornament or TOC probe")
			}
			if !strings.Contains(html, `href="#ch2"`) {
				t.Error("ordinary chapter omitted from TOC")
			}
		})
	}
}
