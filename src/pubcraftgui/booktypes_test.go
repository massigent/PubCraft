package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Il vocabolario di booktypes.go deve restare allineato a src/pubcraft.c
// (type_is_known / type_is_copyright): questa tabella è il contratto condiviso.
func TestBookTypeVocabulary(t *testing.T) {
	known := []string{"chapter", "dedication", "epigraph", "copyright", "colophon",
		"about-author", "acknowledgments", "prologue", "epilogue", "preface", ""}
	for _, ty := range known {
		if !isKnownBookType(ty) {
			t.Errorf("tipo %q dovrebbe essere riconosciuto", ty)
		}
	}
	for _, ty := range []string{"part", "parts", "volume", "half-title", "toc", "copyrightt"} {
		if isKnownBookType(ty) {
			t.Errorf("tipo %q non dovrebbe essere riconosciuto", ty)
		}
	}
	// colophon è un alias di copyright, in qualunque forma/case
	for _, in := range []string{"colophon", "Colophon", " COLOPHON "} {
		if got := canonicalBookType(in); got != "copyright" {
			t.Errorf("canonicalBookType(%q) = %q, atteso copyright", in, got)
		}
	}
	if got := canonicalBookType("Dedication"); got != "dedication" {
		t.Errorf("canonicalBookType(Dedication) = %q, atteso dedication", got)
	}
}

// Un tipo sconosciuto non deve bloccare il progetto né perdere contenuti: viene
// solo segnalato, e book.yaml resta invariato (round-trip fedele).
func TestUnknownBookTypeWarnsAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	src := "title: Tipi\nauthor: A\nlanguage: it\nchapters:\n" +
		"- file: 01.md\n  id: c1\n  title: Uno\n  type: colophon\n" +
		"- file: 02.md\n  id: c2\n  title: Due\n  type: parties\n" +
		"- file: 03.md\n  id: c3\n  title: Tre\n"
	in := filepath.Join(dir, "book.yaml")
	if err := os.WriteFile(in, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := parseBookYAML(in)
	if err != nil {
		t.Fatalf("un tipo sconosciuto non deve bloccare il progetto: %v", err)
	}
	if len(b.Chapters) != 3 {
		t.Fatalf("capitoli persi: %d", len(b.Chapters))
	}
	if b.Chapters[0].Type != "colophon" {
		t.Errorf("il tipo colophon non deve essere riscritto, trovato %q", b.Chapters[0].Type)
	}
	// colophon = alias noto: non deve generare avvisi su colophon
	for _, w := range b.Warnings {
		if strings.Contains(w, "'colophon'") {
			t.Errorf("colophon non deve essere segnalato come sconosciuto: %s", w)
		}
	}
	var unknown []string
	for _, w := range b.Warnings {
		if strings.Contains(w, "'parties'") {
			unknown = append(unknown, w)
		}
	}
	if len(unknown) != 1 {
		t.Fatalf("atteso un avviso per il tipo 'parties', avvisi: %v", b.Warnings)
	}
	if !strings.Contains(unknown[0], "capitolo 2") {
		t.Errorf("l'avviso deve indicare il capitolo interessato: %s", unknown[0])
	}
	if !strings.Contains(unknown[0], "impaginato come capitolo normale") {
		t.Errorf("l'avviso deve dire come viene trattato: %s", unknown[0])
	}

	// round-trip: il file riscritto conserva tipi e contenuti non riconosciuti
	out := filepath.Join(dir, "out.yaml")
	if err := writeBookYAMLAtLocked(out, b); err != nil {
		t.Fatalf("scrittura fallita: %v", err)
	}
	b2, err := parseBookYAML(out)
	if err != nil {
		t.Fatalf("rilettura fallita: %v", err)
	}
	if len(b2.Chapters) != 3 || b2.Chapters[0].Type != "colophon" || b2.Chapters[1].Type != "parties" {
		t.Fatalf("round-trip non fedele: %+v", b2.Chapters)
	}
}

// Un valore toc non valido resta un errore esplicito anche lato GUI, con lo
// stesso vocabolario della CLI.
func TestTocInvalidValueIsAnError(t *testing.T) {
	yaml := "title: X\nchapters:\n- file: 01.md\n  title: Uno\ntoc:\n  subheads: forse\n"
	if _, err := decodeBookYAML([]byte(yaml)); err == nil {
		t.Fatal("un valore toc non valido deve produrre un errore")
	} else if !strings.Contains(err.Error(), "toc.subheads") {
		t.Fatalf("messaggio poco esplicito: %v", err)
	}
	// opzione sconosciuta: avviso non bloccante, valore noto applicato
	b, err := decodeBookYAML([]byte("title: X\nchapters:\n- file: 01.md\n  title: Uno\ntoc:\n  depth: 3\n  subheads: on\n"))
	if err != nil {
		t.Fatalf("un'opzione toc sconosciuta non deve bloccare il progetto: %v", err)
	}
	if b.TOC == nil || b.TOC.Subheads == nil || !*b.TOC.Subheads {
		t.Fatalf("subheads: on non applicato: %+v", b.TOC)
	}
	found := false
	for _, w := range b.Warnings {
		if strings.Contains(w, "'depth'") {
			found = true
		}
	}
	if !found {
		t.Fatalf("atteso un avviso per l'opzione 'depth', avvisi: %v", b.Warnings)
	}
}
