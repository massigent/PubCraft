package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Identificatori del libro.
//
// Policy (Go e CLI C condividono le stesse regole):
//   - i nuovi identificatori sono UUID canonici 8-4-4-4-12, versione 4 e
//     variant RFC 4122 (nessun prefisso "id");
//   - gli identificatori legacy già presenti in book.yaml non vengono mai
//     rigenerati: restano stabili tra build e anteprime;
//   - per l'identificatore di pubblicazione (OPF) un UUID canonico viene usato
//     così com'è, un legacy "id"+UUID viene normalizzato togliendo "id"
//     (stesso valore, forma valida dopo urn:uuid:), qualsiasi altra stringa
//     legacy viene emessa senza il prefisso urn:uuid: (che sarebbe mendace).
var canonicalUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isCanonicalUUID(s string) bool {
	return canonicalUUIDRe.MatchString(strings.TrimSpace(s))
}

// publicationUUID restituisce il valore da mettere dopo "urn:uuid:" nell'OPF.
func publicationUUID(stored string) (string, bool) {
	s := strings.TrimSpace(stored)
	if isCanonicalUUID(s) {
		return strings.ToLower(s), true
	}
	if len(s) > 2 && strings.EqualFold(s[:2], "id") && isCanonicalUUID(s[2:]) {
		return strings.ToLower(s[2:]), true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Modifica dell'uuid in book.yaml basata sulla struttura YAML.
//
// Il riconoscimento della chiave e del valore usa yaml.Node (quindi funziona
// anche per chiavi quotate, forme esplicite e valori block scalar); la
// riscrittura tocca soltanto lo span del valore individuato tramite le
// posizioni del nodo, preservando commenti, ordine e terminazioni di riga.
// Le forme che non possono essere riscritte in sicurezza vengono rifiutate
// PRIMA di scrivere (il file resta invariato).

type yamlLine struct {
	start, contentStart, contentEnd, end int
}

func yamlLines(data []byte) []yamlLine {
	var out []yamlLine
	for i := 0; i < len(data); {
		start := i
		for i < len(data) && data[i] != '\n' {
			i++
		}
		contentEnd := i
		if contentEnd > start && data[contentEnd-1] == '\r' {
			contentEnd--
		}
		if i < len(data) {
			i++
		}
		cs := start
		if start == 0 && bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
			cs = 3
		}
		out = append(out, yamlLine{start: start, contentStart: cs, contentEnd: contentEnd, end: i})
	}
	return out
}

func lineAt(lines []yamlLine, off int) (yamlLine, bool) {
	for _, ln := range lines {
		if off >= ln.start && off < ln.end {
			return ln, true
		}
	}
	return yamlLine{}, false
}

// offsetOfColumn converte una posizione yaml.v3 (riga/colonna 1-based, colonna
// in caratteri) in un offset di byte. La colonna è contata in rune: il prefisso
// delle chiavi gestite è ASCII, ma la conversione resta corretta anche con
// indentazioni o contenuti non ASCII.
func offsetOfColumn(data []byte, lines []yamlLine, line, column int) (int, bool) {
	if line < 1 || line > len(lines) || column < 1 {
		return 0, false
	}
	ln := lines[line-1]
	off := ln.contentStart
	remaining := column - 1
	for off < ln.contentEnd && remaining > 0 {
		_, size := utf8.DecodeRune(data[off:ln.contentEnd])
		off += size
		remaining--
	}
	if remaining != 0 {
		return 0, false
	}
	return off, true
}

type uuidEntry struct {
	key, value    *yaml.Node
	semantic      string
	form          string // "single", "block", "unsupported"
	valueStart    int    // inizio del valore (indicator per block)
	valueEnd      int    // fine del valore single-line (escluso commento)
	comment       string // commento finale sulla riga della chiave (block)
	keyLineStart  int    // per la rimozione di entry duplicate
	keyStart      int    // offset della chiave sulla riga
	entryEnd      int    // fine riga (incluso terminatore) per la rimozione
	lineOfValue   int
	keyLineIndent int
}

// uuidDoc rappresenta il documento analizzato e le occorrenze di chiave uuid.
type uuidDoc struct {
	doc        *yaml.Node
	root       *yaml.Node
	lines      []yamlLine
	entries    []uuidEntry
	chosen     string
	insertAt   int // offset della riga "..." se presente, altrimenti -1
	rootIndent int
	err        error
}

// scanUUIDDocument analizza la struttura YAML e localizza le occorrenze di
// "uuid" di primo livello. Rifiuta forme non aggiornabili in sicurezza.
func scanUUIDDocument(data []byte) (*uuidDoc, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("book.yaml non valido: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("book.yaml deve contenere un solo documento YAML")
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("book.yaml vuoto")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("la radice di book.yaml deve essere una mappa YAML")
	}
	res := &uuidDoc{doc: &doc, root: root, lines: yamlLines(data), insertAt: -1}
	res.rootIndent = firstKeyIndent(data, res.lines, root)
	flowRoot := root.Style&yaml.FlowStyle != 0

	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind == yaml.AliasNode {
			return nil, fmt.Errorf("uuid con chiave alias non supportato")
		}
		if key.Kind != yaml.ScalarNode {
			continue
		}
		if key.Anchor != "" || key.Style&yaml.TaggedStyle != 0 {
			if key.Value == "uuid" {
				return nil, fmt.Errorf("uuid con chiave anchor/tag non supportato")
			}
			continue
		}
		if key.Value != "uuid" {
			continue
		}
		if flowRoot {
			return nil, fmt.Errorf("uuid in book.yaml con mappa flow non supportato: usa la forma a blocchi")
		}
		if key.Line != value.Line {
			return nil, fmt.Errorf("uuid con chiave esplicita su più righe non supportato")
		}
		entry := uuidEntry{key: key, value: value, lineOfValue: value.Line}
		if value.Anchor != "" || value.Kind == yaml.AliasNode || value.Style&yaml.TaggedStyle != 0 {
			return nil, fmt.Errorf("uuid con anchor/alias/tag non supportato")
		}
		if value.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("uuid deve essere uno scalare YAML")
		}
		if value.Tag == "!!null" {
			entry.semantic = ""
		} else {
			entry.semantic = value.Value
		}
		start, ok := offsetOfColumn(data, res.lines, value.Line, value.Column)
		if !ok {
			return nil, fmt.Errorf("impossibile localizzare il valore uuid")
		}
		entry.valueStart = start
		ln, ok := lineAt(res.lines, start)
		if !ok {
			return nil, fmt.Errorf("impossibile localizzare la riga del valore uuid")
		}
		switch {
		case value.Style&yaml.LiteralStyle != 0:
			entry.form = "block"
			// Supportiamo solo il literal con chomping strip ("|-"): conserva
			// esattamente il testo senza aggiungere un newline finale. Altre
			// varianti (|, |+, |2, >) restano forme non supportate e vengono
			// rifiutate prima di scrivere.
			if !literalStripIndicator(data, start, ln.contentEnd) {
				return nil, fmt.Errorf("uuid con block scalar diverso da '|-' non supportato")
			}
		case value.Style&yaml.FoldedStyle != 0:
			return nil, fmt.Errorf("uuid con block scalar folded '>' non supportato")
		case value.Style&yaml.DoubleQuotedStyle != 0 || value.Style&yaml.SingleQuotedStyle != 0:
			end, ok := quotedValueEnd(data, start, ln.contentEnd)
			if !ok {
				return nil, fmt.Errorf("uuid con stringa quotata su più righe non supportato")
			}
			entry.form = "single"
			entry.valueEnd = end
		default:
			end, hasComment := plainValueEnd(data, start, ln.contentEnd)
			entry.form = "single"
			entry.valueEnd = end
			raw := strings.TrimSpace(string(data[start:end]))
			if raw != value.Value {
				// plain scalar proseguito su righe successive
				return nil, fmt.Errorf("uuid con valore plain su più righe non supportato: usa una singola riga")
			}
			_ = hasComment
		}
		if entry.form == "block" {
			// Il commento sulla riga dell'indicatore va preservato se in futuro
			// servisse una riscrittura; per ora i block scalar sono supportati
			// solo in lettura (nessuna modifica se il valore coincide).
			entry.comment = trailingComment(data, start, ln.contentEnd)
		}
		entry.keyLineStart = ln.start
		keyStart, ok := offsetOfColumn(data, res.lines, key.Line, key.Column)
		if !ok {
			return nil, fmt.Errorf("impossibile localizzare la chiave uuid")
		}
		entry.keyStart = keyStart
		entry.keyLineIndent = leadingIndent(data, ln)
		entry.entryEnd = ln.end
		res.entries = append(res.entries, entry)
		if entry.semantic != "" {
			if res.chosen == "" {
				res.chosen = entry.semantic
			} else if res.chosen != entry.semantic {
				return nil, fmt.Errorf("book.yaml contiene identificatori uuid in conflitto (%q e %q)", res.chosen, entry.semantic)
			}
		}
	}

	if len(res.entries) == 0 {
		if flowRoot {
			return nil, fmt.Errorf("book.yaml usa una mappa flow: impossibile aggiungere uuid senza riscriverla")
		}
		res.insertAt = findDocTerminator(data, res.lines)
	}
	return res, nil
}

func firstKeyIndent(data []byte, lines []yamlLine, root *yaml.Node) int {
	if root.Kind == yaml.MappingNode && len(root.Content) > 0 {
		key := root.Content[0]
		if key.Line >= 1 && key.Line <= len(lines) {
			ln := lines[key.Line-1]
			return leadingIndent(data, ln)
		}
	}
	return 0
}

func leadingIndent(data []byte, ln yamlLine) int {
	n := 0
	for i := ln.contentStart; i < ln.contentEnd; i++ {
		if data[i] == ' ' {
			n++
		} else if data[i] == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}

func findDocTerminator(data []byte, lines []yamlLine) int {
	for _, ln := range lines {
		if strings.TrimSpace(string(data[ln.contentStart:ln.contentEnd])) == "..." {
			return ln.start
		}
	}
	return -1
}

// quotedValueEnd restituisce l'offset dopo la virgoletta di chiusura, solo se
// la stringa quotata termina sulla stessa riga.
func quotedValueEnd(data []byte, start, contentEnd int) (int, bool) {
	if start >= contentEnd || (data[start] != '"' && data[start] != '\'') {
		return 0, false
	}
	quote := data[start]
	for j := start + 1; j < contentEnd; j++ {
		if quote == '"' && data[j] == '\\' && j+1 < contentEnd {
			j++
			continue
		}
		if data[j] == quote {
			if quote == '\'' && j+1 < contentEnd && data[j+1] == '\'' {
				j++
				continue
			}
			return j + 1, true
		}
	}
	return 0, false
}

// plainValueEnd restituisce la fine del valore plain (prima di spazi e
// commento) sulla stessa riga.
func plainValueEnd(data []byte, start, contentEnd int) (int, bool) {
	end := contentEnd
	hasComment := false
	for j := start; j < contentEnd; j++ {
		if data[j] == '#' && (j == start || data[j-1] == ' ' || data[j-1] == '\t') {
			end = j
			hasComment = true
			break
		}
	}
	for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
		end--
	}
	return end, hasComment
}

func trailingComment(data []byte, start, contentEnd int) string {
	for j := start; j < contentEnd; j++ {
		if data[j] == '#' && (j == start || data[j-1] == ' ' || data[j-1] == '\t') {
			return string(data[j:contentEnd])
		}
	}
	return ""
}

func literalStripIndicator(data []byte, start, contentEnd int) bool {
	if start >= contentEnd || data[start] != '|' {
		return false
	}
	strip := false
	for i := start + 1; i < contentEnd; i++ {
		c := data[i]
		if c == '-' {
			strip = true
		} else if c == '+' || c == ' ' || c == '\t' || c == '#' {
			break
		} else if c < '1' || c > '9' {
			return false
		}
	}
	return strip
}

// rewriteUUIDDocument costruisce il documento aggiornato. Restituisce i byte
// originali invariati quando non serve alcuna modifica.
func rewriteUUIDDocument(data []byte, sd *uuidDoc, target string) ([]byte, error) {
	rendered := yamlScalar(target)
	if len(sd.entries) == 0 {
		return insertUUIDLine(data, sd.insertAt, sd.rootIndent, rendered), nil
	}

	// Rimozione delle entry duplicate: supportata solo per entry single-line
	// nella forma a blocchi, dove una entry occupa una sola riga.
	skip := make(map[int]bool)
	for _, e := range sd.entries[1:] {
		if e.form != "single" || e.key.Line != e.value.Line {
			return nil, fmt.Errorf("uuid duplicato in forma non supportata: correggi manualmente book.yaml")
		}
		ln, ok := lineAt(sd.lines, e.valueStart)
		if !ok {
			return nil, fmt.Errorf("impossibile localizzare uuid duplicato")
		}
		// La chiave deve essere il primo token della riga (niente entry inline).
		for _, b := range data[ln.contentStart:e.keyStart] {
			if b != ' ' && b != '\t' {
				return nil, fmt.Errorf("uuid duplicato in forma non supportata: correggi manualmente book.yaml")
			}
		}
		skip[ln.start] = true
	}

	keep := sd.entries[0]
	keepValueEnd := keep.valueEnd
	if keep.semantic == target {
		// Valore già corretto: non riscrivere la forma esistente (quoting,
		// block scalar, commenti); al massimo rimuovi i duplicati.
		keepValueEnd = -1
	}
	if keep.form == "block" && keep.semantic != target {
		return nil, fmt.Errorf("uuid in block scalar non aggiornabile automaticamente: riscrivi il valore su una riga")
	}
	if keepValueEnd < 0 && len(skip) == 0 {
		return data, nil
	}

	var out bytes.Buffer
	out.Grow(len(data) + len(rendered) + 32)
	for _, ln := range sd.lines {
		if skip[ln.start] {
			continue
		}
		if ln.start == keep.keyLineStart {
			if keepValueEnd < 0 {
				out.Write(data[ln.start:ln.end])
				continue
			}
			prefix := data[ln.start:keep.valueStart]
			out.Write(prefix)
			// Un valore nullo può iniziare subito dopo i due punti senza
			// spazio (Column punta al primo spazio): YAML richiede la
			// separazione tra ':' e valore.
			if len(prefix) > 0 && prefix[len(prefix)-1] != ' ' && prefix[len(prefix)-1] != '\t' {
				out.WriteByte(' ')
			}
			out.WriteString(rendered)
			if keep.valueEnd < len(data) && data[keep.valueEnd] == '#' {
				out.WriteByte(' ')
			}
			out.Write(data[keep.valueEnd:ln.end])
			continue
		}
		out.Write(data[ln.start:ln.end])
	}
	return out.Bytes(), nil
}

func insertUUIDLine(data []byte, insertAt, indent int, rendered string) []byte {
	lineEnding := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		lineEnding = "\r\n"
	}
	indentText := strings.Repeat(" ", indent)
	insert := indentText + "uuid: " + rendered + lineEnding
	var out bytes.Buffer
	out.Grow(len(data) + len(insert) + 1)
	if insertAt >= 0 {
		out.Write(data[:insertAt])
		out.WriteString(insert)
		out.Write(data[insertAt:])
		return out.Bytes()
	}
	out.Write(data)
	if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteString(lineEnding)
	}
	out.WriteString(insert)
	return out.Bytes()
}

// verifyUUIDDocument controlla che il documento riscritto sia YAML valido,
// contenga esattamente una chiave uuid con il valore atteso e resti un libro
// decodificabile. In caso contrario il chiamante non deve scrivere nulla.
func verifyUUIDDocument(data []byte, target string) error {
	sd, err := scanUUIDDocument(data)
	if err != nil {
		return fmt.Errorf("verifica uuid fallita: %w", err)
	}
	if len(sd.entries) != 1 {
		return fmt.Errorf("verifica uuid fallita: attesa 1 chiave, trovate %d", len(sd.entries))
	}
	if sd.entries[0].semantic != target {
		return fmt.Errorf("verifica uuid fallita: valore %q, atteso %q", sd.entries[0].semantic, target)
	}
	if _, err := decodeBookYAML(data); err != nil {
		return fmt.Errorf("verifica book.yaml fallita dopo la riscrittura: %w", err)
	}
	return nil
}

// countUUIDEntries è usato dai test per verificare la cardinalità strutturale.
func countUUIDEntries(data []byte) (int, error) {
	sd, err := scanUUIDDocument(data)
	if err != nil {
		return 0, err
	}
	return len(sd.entries), nil
}

// ensureBookUUID persiste un identificatore stabile PRIMA di qualsiasi calcolo
// di revisione o build. È idempotente: se il file è già coerente non viene
// riscritto (niente mtime/revisione spuria). Gli errori di scrittura o le forme
// non supportate vengono restituiti al chiamante senza toccare il file.
func ensureBookUUID(vDir string, b *Book) error {
	if b == nil {
		return fmt.Errorf("libro non valido")
	}
	p := filepath.Join(vDir, "book.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("lettura book.yaml per uuid fallita: %w", err)
	}
	sd, err := scanUUIDDocument(data)
	if err != nil {
		return err
	}
	target := b.UUID
	if strings.TrimSpace(target) == "" {
		target = sd.chosen
	}
	if strings.TrimSpace(target) == "" {
		target = ""
	}
	if target == "" {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("generazione uuid fallita: %w", err)
		}
		target = id
	}
	updated, err := rewriteUUIDDocument(data, sd, target)
	if err != nil {
		return err
	}
	if !bytes.Equal(updated, data) {
		if err := verifyUUIDDocument(updated, target); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if st, err := os.Stat(p); err == nil {
			mode = st.Mode().Perm()
		}
		if err := atomicWriteFile(p, updated, mode); err != nil {
			return fmt.Errorf("scrittura uuid in book.yaml fallita: %w", err)
		}
	}
	b.UUID = target
	return nil
}

// countUUIDKeysNode conta le chiavi uuid di primo livello usando yaml.Node:
// è una lettura permissiva (accetta tutte le forme YAML) e serve solo a capire
// se la decodifica in mappa fallirebbe per chiavi duplicate.
func countUUIDKeysNode(data []byte) (int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0, nil
	}
	count := 0
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "uuid" {
			count++
		}
	}
	return count, nil
}

// collapseUUIDDuplicatesForRead rimuove in memoria eventuali chiavi uuid
// duplicate legacy (bug delle versioni precedenti) prima della decodifica YAML,
// senza toccare il file. Una singola chiave, anche in una forma che non
// riscriveremmo (block/folded/flow), viene lasciata al decoder YAML: il
// rifiuto delle forme non aggiornabili avviene solo in scrittura.
func collapseUUIDDuplicatesForRead(data []byte) ([]byte, error) {
	count, err := countUUIDKeysNode(data)
	if err != nil {
		return nil, err
	}
	if count <= 1 {
		return data, nil
	}
	sd, err := scanUUIDDocument(data)
	if err != nil {
		return nil, err
	}
	if len(sd.entries) <= 1 {
		return data, nil
	}
	// Per la sola lettura non serve aggiornare il valore: scegliamo quello
	// non vuoto e rimuoviamo le entry duplicate semplici.
	updated, err := rewriteUUIDDocument(data, sd, sd.chosen)
	if err != nil {
		return nil, err
	}
	return updated, nil
}
