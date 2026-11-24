package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Mappa pagina per l'indice di stampa (meccanismo pageCountMap di Vellum,
// adattato a PubCraft): la CLI, in passata di esplorazione, stampa accanto a
// ogni titolo un marcatore nascosto "PCMAP-<chiave>" (bianco su bianco,
// 0.5pt). Dalla prima passata PDF si estrae su quale pagina fisica cade ogni
// marcatore; la GUI riscrive print.html passando --toc-pagemap e la numerazione
// dell'indice risulta corretta senza dipendere da target-counter (che Chromium
// non supporta).
//
// La mappa e' persistita in .pubcraft-print.json: se la sorgente non cambia
// (stesso SourceStamp) la passata di esplorazione viene saltata del tutto.

const tocPageMarkerPrefix = "PCMAP-"
const maxPageTextBytes = 4 << 20 // tetto di testo estratto per pagina
const maxPDFFileBytes = 512 << 20

// tocPageMap è il risultato dell'estrazione: chiave marcatore -> pagina (1-based).
type tocPageMap map[string]int

// I marcatori finiscono con '-' (PCMAP-ch12- o PCMAP-ch12-s3-): l'estrazione
// PDF concatena le stringhe adiacenti senza spazi, e il trattino finale
// impedisce che la chiave si saldi col testo seguente o con il marcatore
// successivo. Le chiavi sono solo ch<numero> / ch<numero>-s<numero>.
var tocMarkerRe = regexp.MustCompile(`PCMAP-(ch[0-9]+(?:-s[0-9]+)?)-`)

// extractTocPageMap apre il PDF e restituisce la mappa marcatore→pagina.
// Un PDF senza marcatori (passata finale) restituisce una mappa vuota, non un
// errore: l'assenza di marcatori è attesa e gestita dal chiamante.
func extractTocPageMap(pdfPath string) (tocPageMap, error) {
	data, err := osReadFileBounded(pdfPath, maxPDFFileBytes)
	if err != nil {
		return nil, fmt.Errorf("lettura PDF fallita: %w", err)
	}
	return extractTocPageMapFromData(data)
}

// extractTocPageMapFromData è la variante testabile su dati già in memoria.
func extractTocPageMapFromData(data []byte) (tocPageMap, error) {
	out := tocPageMap{}
	if len(data) < 32 {
		return nil, fmt.Errorf("file PDF troppo piccolo (%d byte)", len(data))
	}
	if bytes.Index(data[:min(1024, len(data))], []byte("%PDF-")) == -1 {
		return nil, fmt.Errorf("magic header %%PDF- mancante")
	}
	p, err := newPDFParserForData(data)
	if err != nil {
		return nil, err
	}
	pages, err := p.pagesInOrder()
	if err != nil {
		return nil, err
	}
	for pageNo, pg := range pages {
		fonts := make(map[string]map[string]string)
		twoByte := make(map[string]bool)
		p.loadFontResources(pg.dict, fonts, twoByte)
		text := extractPDFText(pg.content, p, fonts, twoByte)
		compact := collapseWhitespace(text)
		for _, m := range tocMarkerRe.FindAllStringSubmatch(compact, -1) {
			// chiave senza prefisso ("ch12", "ch12-s3"): e' il formato che la
			// CLI usa nel JSON --toc-pagemap (toc_anchor_key)
			key := m[1]
			if _, seen := out[key]; !seen {
				out[key] = pageNo + 1
			}
		}
	}
	return out, nil
}

func newPDFParserForData(data []byte) (*PDFParser, error) {
	tailLen := 4096
	if len(data) < tailLen {
		tailLen = len(data)
	}
	tail := data[len(data)-tailLen:]
	eofIdx := bytes.LastIndex(tail, []byte("%%EOF"))
	if eofIdx == -1 {
		return nil, fmt.Errorf("marcatore %%%%EOF mancante o corrotto")
	}
	absEOF := len(data) - tailLen + eofIdx
	sxIdx := bytes.LastIndex(data[:absEOF], []byte("startxref"))
	if sxIdx == -1 {
		return nil, fmt.Errorf("marcatore startxref mancante")
	}
	fields := strings.Fields(strings.TrimSpace(string(data[sxIdx+len("startxref") : absEOF])))
	if len(fields) == 0 {
		return nil, fmt.Errorf("offset numerico mancante dopo startxref")
	}
	off, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || off < 0 || off >= int64(len(data)) {
		return nil, fmt.Errorf("offset startxref non valido (%s)", fields[0])
	}
	p := &PDFParser{
		data:        data,
		objects:     make(map[int]objLocation),
		objStmCache: make(map[int]map[int]string),
	}
	if err := p.loadXRefs(off); err != nil {
		return nil, fmt.Errorf("analisi xref/trailer fallita: %w", err)
	}
	return p, nil
}

// pdfPage raccoglie il dizionario e il contenuto decodificato di una pagina.
type pdfPage struct {
	dict    string
	content []byte
}

// pagesInOrder risolve l'albero /Pages e restituisce le pagine in ordine fisico.
func (p *PDFParser) pagesInOrder() ([]pdfPage, error) {
	rootObjNum, err := p.extractRef(p.trailerDict, "/Root")
	if err != nil {
		return nil, fmt.Errorf("trailer privo di /Root valido: %w", err)
	}
	catalogDict, err := p.resolveDict(rootObjNum)
	if err != nil {
		return nil, fmt.Errorf("risoluzione catalogo (obj %d) fallita: %w", rootObjNum, err)
	}
	if !regexp.MustCompile(`/Type\s*/Catalog\b`).MatchString(catalogDict) {
		return nil, fmt.Errorf("Root non è un Catalog")
	}
	pagesObjNum, err := p.extractRef(catalogDict, "/Pages")
	if err != nil {
		return nil, fmt.Errorf("catalogo privo di riferimento /Pages: %w", err)
	}
	var out []pdfPage
	seen := make(map[int]bool)
	if err := p.collectPages(pagesObjNum, seen, 0, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nessuna pagina trovata")
	}
	return out, nil
}

func (p *PDFParser) collectPages(obj int, seen map[int]bool, depth int, out *[]pdfPage) error {
	if depth > 128 || len(seen) >= maxPDFObjects || seen[obj] {
		return fmt.Errorf("albero pagine ciclico, duplicato o troppo profondo")
	}
	seen[obj] = true
	dict, err := p.resolveDict(obj)
	if err != nil {
		return err
	}
	kind := regexp.MustCompile(`/Type\s*/(Page|Pages)\b`).FindStringSubmatch(dict)
	if len(kind) < 2 {
		return fmt.Errorf("tipo del nodo pagine non valido")
	}
	if kind[1] == "Page" {
		content, err := p.pageContent(dict)
		if err != nil {
			// Una pagina non decodificabile non blocca l'estrazione: senza
			// testo non troverà marcatori, ma le altre restano valide.
			content = nil
		}
		*out = append(*out, pdfPage{dict: dict, content: content})
		return nil
	}
	kids := regexp.MustCompile(`(?s)/Kids\s*\[(.*?)\]`).FindStringSubmatch(dict)
	if len(kids) < 2 {
		return fmt.Errorf("Kids mancante")
	}
	refs := regexp.MustCompile(`(\d+)\s+\d+\s+R`).FindAllStringSubmatch(kids[1], -1)
	for _, ref := range refs {
		id, e := strconv.Atoi(ref[1])
		if e != nil {
			return e
		}
		if e := p.collectPages(id, seen, depth+1, out); e != nil {
			return e
		}
	}
	return nil
}

// pageContent concatena e decodifica gli stream /Contents di una pagina.
func (p *PDFParser) pageContent(pageDict string) ([]byte, error) {
	var refs []string
	if m := regexp.MustCompile(`/Contents\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(pageDict); len(m) > 0 {
		refs = []string{m[1]}
	} else if m := regexp.MustCompile(`/Contents\s*\[(.*?)\]`).FindStringSubmatch(pageDict); len(m) > 1 {
		for _, r := range regexp.MustCompile(`(\d+)\s+\d+\s+R`).FindAllStringSubmatch(m[1], -1) {
			refs = append(refs, r[1])
		}
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("pagina priva di /Contents")
	}
	var full []byte
	for _, r := range refs {
		objNum, err := strconv.Atoi(r)
		if err != nil {
			return nil, err
		}
		loc, ok := p.objects[objNum]
		if !ok {
			return nil, fmt.Errorf("oggetto /Contents %d non trovato", objNum)
		}
		if loc.isCompressed {
			return nil, fmt.Errorf("/Contents in ObjStm non supportato")
		}
		if loc.fileOffset < 0 || loc.fileOffset >= int64(len(p.data)) {
			return nil, fmt.Errorf("offset /Contents fuori dal file")
		}
		chunk := p.data[loc.fileOffset:]
		dStart := bytes.Index(chunk, []byte("<<"))
		if dStart == -1 {
			return nil, fmt.Errorf("dizionario /Contents mancante")
		}
		dEnd := p.findMatchingDictEnd(string(chunk[dStart:]))
		if dEnd == -1 {
			return nil, fmt.Errorf("chiusura dizionario /Contents mancante")
		}
		dictStr := string(chunk[dStart : dStart+dEnd])
		raw, err := p.extractStreamBytes(chunk[dStart+dEnd:], dictStr)
		if err != nil {
			return nil, err
		}
		full = append(full, raw...)
		if len(full) > maxPDFStreamBytes {
			return nil, fmt.Errorf("contenuto pagina oltre il limite")
		}
	}
	return full, nil
}

// loadFontResources popola fonts (ToUnicode) e twoByte (codici a 2 byte dei
// font Type0) leggendo /Resources → /Font della pagina.
func (p *PDFParser) loadFontResources(pageDict string, fonts map[string]map[string]string, twoByte map[string]bool) {
	resDict := pageDict
	if m := regexp.MustCompile(`/Resources\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(pageDict); len(m) > 1 {
		if id, err := strconv.Atoi(m[1]); err == nil {
			if d, err := p.resolveDict(id); err == nil {
				resDict = d
			}
		}
	}
	var fontSrc string
	if m := regexp.MustCompile(`/Font\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(resDict); len(m) > 1 {
		if id, err := strconv.Atoi(m[1]); err == nil {
			if d, err := p.resolveDict(id); err == nil {
				fontSrc = d
			}
		}
	} else if m := regexp.MustCompile(`(?s)/Font\s*<<(.*?>>)`).FindStringSubmatch(resDict); len(m) > 1 {
		fontSrc = m[1]
	}
	if fontSrc == "" {
		return
	}
	re := regexp.MustCompile(`/(\S+)\s+(\d+)\s+\d+\s+R`)
	for _, m := range re.FindAllStringSubmatch(fontSrc, -1) {
		name := "/" + m[1]
		if _, exists := fonts[name]; exists {
			continue
		}
		id, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		fd, err := p.resolveDict(id)
		if err != nil {
			continue
		}
		if regexp.MustCompile(`/Subtype\s*/Type0\b`).MatchString(fd) {
			twoByte[name] = true
		}
		if cm := p.fontToUnicode(fd); cm != nil {
			fonts[name] = cm
		} else {
			fonts[name] = nil
		}
	}
}

// fontToUnicode scarica la CMap ToUnicode di un font (bfchar/bfrange → testo).
// Ritorna nil se assente: in quel caso i byte delle stringhe letterali
// vengono usati così come sono (font semplici Latin-1).
func (p *PDFParser) fontToUnicode(fontDict string) map[string]string {
	tuRef := regexp.MustCompile(`/ToUnicode\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(fontDict)
	if len(tuRef) < 2 {
		return nil
	}
	objNum, err := strconv.Atoi(tuRef[1])
	if err != nil {
		return nil
	}
	loc, ok := p.objects[objNum]
	if !ok || loc.isCompressed || loc.fileOffset < 0 || loc.fileOffset >= int64(len(p.data)) {
		return nil
	}
	chunk := p.data[loc.fileOffset:]
	dStart := bytes.Index(chunk, []byte("<<"))
	if dStart == -1 {
		return nil
	}
	dEnd := p.findMatchingDictEnd(string(chunk[dStart:]))
	if dEnd == -1 {
		return nil
	}
	dictStr := string(chunk[dStart : dStart+dEnd])
	raw, err := p.extractStreamBytes(chunk[dStart+dEnd:], dictStr)
	if err != nil {
		return nil
	}
	if len(raw) > maxPDFStreamBytes {
		return nil
	}
	return parseToUnicodeCMap(raw)
}

// parseToUnicodeCMap interpreta le sezioni beginbfchar/beginbfrange di una CMap.
func parseToUnicodeCMap(data []byte) map[string]string {
	out := make(map[string]string)
	s := string(data)
	for _, section := range []struct{ begin, end string }{
		{"beginbfchar", "endbfchar"},
		{"beginbfrange", "endbfrange"},
	} {
		bi := strings.Index(s, section.begin)
		for bi != -1 {
			ei := strings.Index(s[bi:], section.end)
			if ei == -1 {
				break
			}
			body := s[bi+len(section.begin) : bi+ei]
			if section.begin == "beginbfchar" {
				parseBFChar(body, out)
			} else {
				parseBFRange(body, out)
			}
			next := bi + ei + len(section.end)
			if next >= len(s) {
				break
			}
			bi = strings.Index(s[next:], section.begin)
			if bi != -1 {
				bi += next
			}
		}
	}
	return out
}

func parseBFChar(body string, out map[string]string) {
	// dst può contenere più unità UTF-16BE separate da spazi: <0043 004D> = "CM"
	re := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f ]+?)>`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		dst := utf16BEToUTF8(strings.Join(strings.Fields(m[2]), ""))
		if dst != "" {
			out[strings.ToUpper(m[1])] = dst
		}
	}
}

func parseBFRange(body string, out map[string]string) {
	// <lo> <hi> <dst> — ogni codice C mappa a dst+(C-lo) (incremento
	// big-endian, spec PDF 32000); oppure <lo> <hi> [<d1> <d2> ...]
	re := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*(<[0-9A-Fa-f ]+?>|\[[^\]]*\])`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		lo, e1 := strconv.ParseUint(m[1], 16, 64)
		hi, e2 := strconv.ParseUint(m[2], 16, 64)
		if e1 != nil || e2 != nil || hi < lo {
			continue
		}
		// tetto difensivo: range troncati non devono allocare a spazzane
		if hi-lo > 65535 {
			continue
		}
		srcLen := len(m[1])
		if strings.HasPrefix(m[3], "<") {
			baseHex := strings.Join(strings.Fields(strings.Trim(m[3], "<>")), "")
			for code := lo; code <= hi; code++ {
				dstHex := hexBEAdd(baseHex, uint64(code-lo))
				if dst := utf16BEToUTF8(dstHex); dst != "" {
					out[strings.ToUpper(fmt.Sprintf("%0*X", srcLen, code))] = dst
				}
			}
		} else {
			dsts := regexp.MustCompile(`<([0-9A-Fa-f ]+?)>`).FindAllStringSubmatch(m[3], -1)
			n := len(dsts)
			for code := lo; code <= hi; code++ {
				idx := int(code - lo)
				if idx < n {
					hexJoin := strings.Join(strings.Fields(dsts[idx][1]), "")
					if dst := utf16BEToUTF8(hexJoin); dst != "" {
						out[strings.ToUpper(fmt.Sprintf("%0*X", srcLen, code))] = dst
					}
				}
			}
		}
	}
}

// hexBEAdd aggiunge delta a un valore esadecimale big-endian mantenendo la
// larghezza originale (usato per la semantica di incremento dei bfrange).
func hexBEAdd(hex string, delta uint64) string {
	if hex == "" {
		return ""
	}
	digits := []byte(hex)
	carry := uint64(delta)
	for i := len(digits) - 1; i >= 0 && carry > 0; i-- {
		v, err := strconv.ParseUint(string(digits[i]), 16, 8)
		if err != nil {
			return hex
		}
		sum := v + carry&0xF
		digits[i] = fmt.Sprintf("%X", sum&0xF)[0]
		carry = carry>>4 + sum>>4
	}
	return string(digits)
}

// utf16BEToUTF8 decodifica una sequenza esadecimale UTF-16BE in stringa.
func utf16BEToUTF8(hex string) string {
	if len(hex) == 0 || len(hex)%4 != 0 {
		return ""
	}
	units := make([]uint16, 0, len(hex)/4)
	for i := 0; i+4 <= len(hex); i += 4 {
		v, err := strconv.ParseUint(hex[i:i+4], 16, 16)
		if err != nil {
			return ""
		}
		units = append(units, uint16(v))
	}
	return string(utf16.Decode(units))
}

// extractPDFText estrae il testo da uno stream di contenuto PDF gestendo
// Tf (font corrente), stringhe esadecimali e letterali, e le CMap ToUnicode.
// Non è un renderer: serve solo al recupero dei marcatori PCMAP.
func extractPDFText(content []byte, p *PDFParser, fonts map[string]map[string]string, twoByte map[string]bool) string {
	var sb strings.Builder
	text := func(s string) {
		if sb.Len() <= maxPageTextBytes {
			sb.WriteString(s)
		}
	}
	var curFont string
	i := 0
	n := len(content)
	for i < n {
		c := content[i]
		switch {
		case c == '%':
			// commento PDF: salta a fine riga
			for i < n && content[i] != '\n' && content[i] != '\r' {
				i++
			}
			continue
		case c == '<':
			if i+1 < n && content[i+1] == '<' {
				i += 2
				continue
			}
			// stringa esadecimale
			j := i + 1
			for j < n && content[j] != '>' {
				j++
			}
			if j >= n {
				i = n
				continue
			}
			text(decodePDFStringHex(sanitizeHex(content[i+1:j]), curFont, fonts, twoByte))
			i = j + 1
			continue
		case c == '(':
			// stringa letterale (con escape e nesting bilanciato)
			j := i + 1
			depth := 1
			var lit []byte
			for j < n && depth > 0 {
				switch content[j] {
				case '\\':
					if j+1 < n {
						switch content[j+1] {
						case 'n':
							lit = append(lit, '\n')
						case 'r':
							lit = append(lit, '\r')
						case 't':
							lit = append(lit, '\t')
						case 'b', 'f':
							lit = append(lit, ' ')
						default:
							// \( \) \\ \ddd
							if content[j+1] >= '0' && content[j+1] <= '7' && j+3 < n &&
								content[j+2] >= '0' && content[j+2] <= '7' &&
								content[j+3] >= '0' && content[j+3] <= '7' {
								oct := (content[j+1]-'0')*64 + (content[j+2]-'0')*8 + (content[j+3] - '0')
								lit = append(lit, oct)
								j += 3
							} else {
								lit = append(lit, content[j+1])
							}
						}
						j += 2
						continue
					}
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						j++ // consuma la ')'
						goto done
					}
				}
				lit = append(lit, content[j])
				j++
			}
			j = min(j, n)
		done:
			text(decodePDFStringLit(lit, curFont, fonts, twoByte))
			i = j
			continue
		case c == '/':
			// nome risorsa: /F1 seguito eventualmente da "size Tf"
			j := i + 1
			for j < n && !isPDFDelim(content[j]) {
				j++
			}
			name := string(content[i+1 : j])
			k := j
			for k < n && (content[k] == ' ' || content[k] == '\t' || content[k] == '\r' || content[k] == '\n' ||
				(content[k] >= '0' && content[k] <= '9') || content[k] == '.' || content[k] == '-') {
				k++
			}
			if k+1 < n && content[k] == 'T' && content[k+1] == 'f' &&
				(k+2 >= n || isPDFDelim(content[k+2]) || content[k+2] == '\n') {
				curFont = "/" + name
			}
			i = j
			continue
		default:
			i++
		}
	}
	if sb.Len() > maxPageTextBytes {
		return sb.String()[:maxPageTextBytes]
	}
	return sb.String()
}

func isPDFDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func sanitizeHex(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// decodePDFStringHex decodifica una stringa esadecimale col font corrente.
func decodePDFStringHex(hex, font string, fonts map[string]map[string]string, twoByte map[string]bool) string {
	if hex == "" {
		return ""
	}
	if cm, ok := fonts[font]; ok && cm != nil {
		// stringa con più codici: un codice ogni 4 cifre per font a 2 byte
		var sb strings.Builder
		step := 2
		if twoByte[font] {
			step = 4
		}
		for i := 0; i+step <= len(hex); i += step {
			sb.WriteString(cmapLookup(hex[i:i+step], cm, twoByte[font]))
		}
		return sb.String()
	}
	if twoByte[font] {
		// Identity-H senza ToUnicode: niente testo ricavabile
		return ""
	}
	// font semplice: coppie esadecimali = byte Latin-1
	raw := make([]byte, 0, len(hex)/2)
	for i := 0; i+2 <= len(hex); i += 2 {
		v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
		if err != nil {
			return ""
		}
		raw = append(raw, byte(v))
	}
	return string(raw)
}

// decodePDFStringLit decodifica una stringa letterale col font corrente.
func decodePDFStringLit(lit []byte, font string, fonts map[string]map[string]string, twoByte map[string]bool) string {
	if cm, ok := fonts[font]; ok && cm != nil {
		var sb strings.Builder
		width := 1
		if twoByte[font] {
			width = 2
		}
		for i := 0; i < len(lit); i += width {
			end := min(i+width, len(lit))
			code := fmt.Sprintf("%0*X", width*2, bytesToUint(lit[i:end]))
			if s, ok2 := cm[code]; ok2 {
				sb.WriteString(s)
			} else if s2, ok3 := cm[strings.ToUpper(code)]; ok3 {
				sb.WriteString(s2)
			}
		}
		return sb.String()
	}
	if twoByte[font] {
		return ""
	}
	return string(lit)
}

// cmapLookup traduce un codice esadecimale (1 o 2 byte) tramite la CMap.
func cmapLookup(hex string, cmap map[string]string, isTwoByte bool) string {
	if isTwoByte && len(hex) >= 4 {
		if s, ok := cmap[strings.ToUpper(hex[:4])]; ok {
			return s
		}
	}
	if s, ok := cmap[strings.ToUpper(hex)]; ok {
		return s
	}
	return ""
}

func bytesToUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// collapseWhitespace rimuove ogni whitespace: i marcatori PCMAP non contengono
// spazi e le TJ dei motori PDF possono spezzarli con posizionamenti.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f', 0x00:
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// osReadFileBounded legge un file con tetto dimensionale (nessun input utente
// può far allocare memoria a dismisura).
func osReadFileBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("file oltre il limite di %d byte", limit)
	}
	buf := make([]byte, st.Size())
	// Un singolo Read può restituire meno byte del richiesto senza errore:
	// un PDF troncato verrebbe poi analizzato come corrotto. Leggi fino alla
	// dimensione dichiarata (o restituisci l'errore reale).
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
