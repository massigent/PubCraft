package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlBool interpreta i booleani di `toc:` con la stessa semantica della CLI
// (yaml_bool_parse in pubcraft.c): true/false, yes/no, on/off, 1/0 senza
// distinzione di maiuscole. Un valore assente, vuoto, `~` o `null` significa
// "non impostato" (set=false): resta il default. Qualsiasi altro valore è un
// errore esplicito in entrambe le implementazioni.
func yamlBool(v any, field string) (val bool, set bool, err error) {
	switch t := v.(type) {
	case nil:
		return false, false, nil
	case bool:
		return t, true, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes", "on", "1":
			return true, true, nil
		case "false", "no", "off", "0":
			return false, true, nil
		case "", "~", "null":
			return false, false, nil
		}
	case int:
		// La CLI legge il testo grezzo e accetta solo 1/0: un intero diverso
		// (es. 2) è un errore anche qui, per non divergere.
		if t == 0 || t == 1 {
			return t == 1, true, nil
		}
	case float64:
		// Testo grezzo come 1.0 o 2.5 non è un booleano accettato dalla CLI.
		return false, false, fmt.Errorf("%s non valido (atteso true/false, yes/no, on/off, 1/0)", field)
	}
	return false, false, fmt.Errorf("%s non valido (atteso true/false, yes/no, on/off, 1/0)", field)
}

// Validate YAML syntax and the supported book schema before changing projects.
// Decoding via a map also detects duplicate keys; unknown scalar metadata is
// retained rather than silently discarded on the next write.
// TocConfig sono le opzioni dell'indice automatico in book.yaml:
//
//	toc:
//	  subheads: true      # sottocapi (h3) annidati nell'indice (default false)
//	  pagenumbers: false  # numeri di pagina nell'indice di stampa (default true)
//
// I puntatori distinguono "non impostato" (conservato com'è nel file) dai
// valori espliciti: i default sono applicati dalla CLI.
type TocConfig struct {
	Subheads    *bool `json:"subheads,omitempty"`
	PageNumbers *bool `json:"pagenumbers,omitempty"`
}

func decodeBookYAML(data []byte) (*Book, error) {
	// Un book.yaml legacy può contenere chiavi uuid duplicate (bug delle
	// versioni precedenti). Le normalizziamo prima della decodifica, senza
	// toccare il file: il rewrite avviene solo tramite ensureBookUUID.
	data, err := collapseUUIDDuplicatesForRead(data)
	if err != nil {
		return nil, err
	}
	var fields map[string]yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("book.yaml deve contenere un solo documento YAML")
	}
	b := &Book{Language: "it", Chapters: []Chapter{}, ExtraFields: make(map[string]string), extraRaw: make(map[string]string)}
	for key, node := range fields {
		if key == "chapters" {
			if node.Tag == "!!null" {
				continue
			}
			if node.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("chapters deve essere una lista")
			}
			for _, item := range node.Content {
				if item.Kind != yaml.MappingNode {
					return nil, fmt.Errorf("capitolo non valido")
				}
				var chapter map[string]string
				if err := item.Decode(&chapter); err != nil {
					return nil, err
				}
				fileName := strings.TrimSpace(chapter["file"])
				if fileName == "" {
					return nil, fmt.Errorf("capitolo privo di file")
				}
				// Contenimento del vault: il capitolo deve essere un semplice nome
				// di file nella cartella del libro. Percorsi assoluti, contenenti
				// separatori o ".." permetterebbero lettura e pubblicazione di file
				// arbitrari fuori dal progetto (es. da book.yaml ricevuti da terzi).
				if !isVaultChapterName(fileName) {
					return nil, fmt.Errorf("capitolo con percorso non consentito: %q (deve essere un nome file nella cartella del libro)", fileName)
				}
				b.Chapters = append(b.Chapters, Chapter{File: fileName, Title: chapter["title"], ID: chapter["id"], Type: chapter["type"]})
			}
			continue
		}
		if key == "toc" {
			if node.Tag == "!!null" {
				continue
			}
			if node.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("toc deve essere una mappa di opzioni (subheads, pagenumbers)")
			}
			var opts map[string]any
			if err := node.Decode(&opts); err != nil {
				return nil, fmt.Errorf("toc non valido: %w", err)
			}
			tc := &TocConfig{}
			if v, ok := opts["subheads"]; ok {
				bv, set, err := yamlBool(v, "toc.subheads")
				if err != nil {
					return nil, err
				}
				if set {
					tc.Subheads = &bv
				}
			}
			if v, ok := opts["pagenumbers"]; ok {
				bv, set, err := yamlBool(v, "toc.pagenumbers")
				if err != nil {
					return nil, err
				}
				if set {
					tc.PageNumbers = &bv
				}
			}
			for k := range opts {
				if k != "subheads" && k != "pagenumbers" {
					// Stessa politica della CLI: avvisa e ignora, senza
					// rendere il progetto inutilizzabile.
					b.Warnings = append(b.Warnings,
						fmt.Sprintf("opzione toc sconosciuta '%s' ignorata (supportate: subheads, pagenumbers)", k))
				}
			}
			b.TOC = tc
			continue
		}
		if node.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("il campo %s deve essere scalare", key)
		}
		value := node.Value
		switch key {
		case "title":
			b.Title = value
		case "author":
			b.Author = value
		case "language":
			b.Language = value
		case "uuid":
			// Chiave assente, vuota o null (~/null) sono equivalenti: solo un
			// valore scalare non nullo è un identificatore persistente.
			if node.Tag == "!!null" {
				b.UUID = ""
			} else {
				b.UUID = value
			}
		case "cover":
			b.Cover = value
		case "style":
			b.Style = value
		case "opening":
			b.Opening = value
		case "font":
			b.Font = value
		case "scenebreak":
			b.SceneBreak = value
		case "trim":
			b.Trim = value
		case "pages":
			if err := node.Decode(&b.Pages); err != nil {
				return nil, fmt.Errorf("pages non valido: %w", err)
			}
		case "bleed":
			switch strings.ToLower(value) {
			case "true", "yes", "1":
				b.Bleed = true
			case "false", "no", "0", "":
				b.Bleed = false
			default:
				return nil, fmt.Errorf("bleed non valido")
			}
		default:
			// ExtraFields conserva il valore decodificato; extraRaw il testo
			// YAML originale così che numeri e booleani ("5", "yes") non
			// diventino stringhe quotate al rewrite (P3-21).
			b.ExtraFields[key] = value
			b.extraRaw[key] = rawScalarText(node)
		}
	}
	if strings.TrimSpace(b.Title) == "" && len(b.Chapters) == 0 {
		return nil, fmt.Errorf("book.yaml privo di titolo o capitoli")
	}
	return b, nil
}

// isVaultChapterName verifica che il riferimento al capitolo sia un semplice
// nome di file contenuto nella cartella del libro (niente percorsi assoluti,
// separatori di directory, componenti ".." o nomi riservati).
func isVaultChapterName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	if name != filepath.Clean(name) {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return isMarkdownFile(name)
}

// rawScalarText restituisce il testo da riscrivere per un metadato extra:
// gli scalari plain su singola riga vengono preservati alla lettera così che
// numeri ("5") e booleani ("yes") non diventino stringhe quotate al rewrite.
// La riscrittura verbatim è ammessa solo se riletta produce la stessa stringa
// (esclude commenti troncati come "a #b", mappe come "a: b", ecc.).
func rawScalarText(node yaml.Node) string {
	if node.Style == 0 && node.Value != "" && !strings.ContainsAny(node.Value, "\n\r") {
		var probe string
		if err := yaml.Unmarshal([]byte(node.Value), &probe); err == nil && probe == node.Value {
			return node.Value
		}
	}
	if node.Value == "" {
		return `""`
	}
	return yamlScalar(node.Value)
}

// Keep simple values unchanged for CLI compatibility, but quote values whose
// punctuation/newlines would otherwise turn user-entered titles into invalid YAML.
func yamlScalar(value string) string {
	// Normalizza eventuali byte non UTF-8 (es. file legacy latin-1): senza
	// questo, strconv.Quote emette escape \xNN che YAML interpreta come
	// codepoint Unicode, corrompendo il testo al successivo caricamento.
	value = strings.ToValidUTF8(value, "\uFFFD")
	data, _ := yaml.Marshal(value)
	encoded := strings.TrimSpace(string(data))
	if encoded == value && !strings.ContainsAny(value, "\n\r\t") {
		return value
	}
	return strconv.Quote(value)
}
