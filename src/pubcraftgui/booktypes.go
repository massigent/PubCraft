package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Vocabolario dei tipi editoriali di sezione. Deve restare identico a
// src/pubcraft.c (type_is_known / type_is_copyright): una modifica qui va
// fatta anche lì, altrimenti la GUI e la CLI classificano diversamente gli
// stessi capitoli.
//
// `colophon` è un alias editoriale di `copyright`: in stampa è la stessa
// pagina di frontmatter (nessun titolo di capitolo, nessun capolettera, fuori
// dall'indice stampato). In book.yaml il valore scritto dall'autore non viene
// riscritto: si conserva com'è, cambia solo il comportamento.
var knownBookTypes = map[string]bool{
	"chapter":         true,
	"dedication":      true,
	"epigraph":        true,
	"copyright":       true,
	"about-author":    true,
	"acknowledgments": true,
	"prologue":        true,
	"epilogue":        true,
	"preface":         true,
}

// canonicalBookType normalizza un tipo di sezione al nome usato dal
// comportamento (alias compresi), senza toccare il valore salvato.
func canonicalBookType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "colophon":
		return "copyright"
	}
	return strings.ToLower(strings.TrimSpace(t))
}

// isKnownBookType indica se il tipo è riconosciuto (l'alias colophon lo è).
func isKnownBookType(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return true
	}
	return knownBookTypes[canonicalBookType(t)]
}

// unknownBookTypeWarnings elenca i tipi di sezione non riconosciuti, una voce
// per tipo, con i capitoli interessati (numerazione 1-based come nell'indice).
func unknownBookTypeWarnings(b *Book) []string {
	if b == nil {
		return nil
	}
	byType := map[string][]int{}
	for i, ch := range b.Chapters {
		t := strings.ToLower(strings.TrimSpace(ch.Type))
		if t == "" || isKnownBookType(t) {
			continue
		}
		byType[t] = append(byType[t], i+1)
	}
	if len(byType) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byType))
	for k := range byType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf(
			"tipo di sezione '%s' non riconosciuto (capitolo %s): impaginato come capitolo normale. "+
				"Tipi supportati: chapter, dedication, epigraph, copyright, colophon, about-author, "+
				"acknowledgments, prologue, epilogue, preface.",
			k, joinInts(byType[k])))
	}
	return out
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, ", ")
}

// Avvisi già emessi in questo processo, per non riscrivere il registro
// diagnostico a ogni lettura di book.yaml.
var bookWarningOnce = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

// reportBookWarnings registra una sola volta per processo e per progetto gli
// avvisi di metadati (tipi sconosciuti, opzioni toc ignorate). Non interrompe
// nulla: i contenuti restano e il progetto si apre normalmente.
func reportBookWarnings(vDir string, b *Book) {
	if b == nil || len(b.Warnings) == 0 {
		return
	}
	for _, w := range b.Warnings {
		key := vDir + "\x00" + w
		bookWarningOnce.mu.Lock()
		already := bookWarningOnce.seen[key]
		if !already {
			bookWarningOnce.seen[key] = true
		}
		bookWarningOnce.mu.Unlock()
		if already {
			continue
		}
		RecordDiagEvent("warning", "metadata", "BOOK_METADATA_WARNING", "parseBookYAML",
			w, "progetto: "+vDir, "", "")
		fmt.Fprintf(os.Stderr, "avviso: %s\n", w)
	}
}
