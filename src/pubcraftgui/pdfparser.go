package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const maxPDFStreamBytes = 64 << 20
const maxPDFObjects = 1_000_000

type objLocation struct {
	isCompressed bool
	fileOffset   int64
	stmObjNum    int
	stmIndex     int
}

// PDFParser supports unencrypted classic/Flate xref and object streams used by
// our renderers, with bounded decoding. It is not a full ISO 32000 validator.
type PDFParser struct {
	data        []byte
	objects     map[int]objLocation
	trailerDict string
	objStmCache map[int]map[int]string // stmObjNum -> (objNum -> dictContent)
}

// ParsePDFData convalida la struttura PDF ed estrae il numero effettivo di pagine.
func ParsePDFData(data []byte) (int, error) {
	if len(data) < 32 {
		return 0, fmt.Errorf("file PDF troppo piccolo (%d byte)", len(data))
	}

	// 1. Header %PDF-
	headerLimit := 1024
	if len(data) < headerLimit {
		headerLimit = len(data)
	}
	if bytes.Index(data[:headerLimit], []byte("%PDF-")) == -1 {
		return 0, fmt.Errorf("magic header %%PDF- mancante")
	}

	// 2. Controllo %%EOF negli ultimi 4096 byte
	tailLen := 4096
	if len(data) < tailLen {
		tailLen = len(data)
	}
	tail := data[len(data)-tailLen:]
	eofIdx := bytes.LastIndex(tail, []byte("%%EOF"))
	if eofIdx == -1 {
		return 0, fmt.Errorf("marcatore %%%%EOF mancante o corrotto")
	}

	// 3. Trova startxref prima di %%EOF
	absEOF := len(data) - tailLen + eofIdx
	searchRegion := data[:absEOF]
	startXRefMarker := []byte("startxref")
	sxIdx := bytes.LastIndex(searchRegion, startXRefMarker)
	if sxIdx == -1 {
		return 0, fmt.Errorf("marcatore startxref mancante prima di %%%%EOF")
	}

	// Leggi l'offset numerico dopo startxref
	afterSX := strings.TrimSpace(string(searchRegion[sxIdx+len(startXRefMarker):]))
	fields := strings.Fields(afterSX)
	if len(fields) == 0 {
		return 0, fmt.Errorf("offset numerico mancante dopo startxref")
	}
	startXRefOffset, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || startXRefOffset < 0 || startXRefOffset >= int64(len(data)) {
		return 0, fmt.Errorf("offset startxref non valido (%s)", fields[0])
	}

	p := &PDFParser{
		data:        data,
		objects:     make(map[int]objLocation),
		objStmCache: make(map[int]map[int]string),
	}

	if err := p.loadXRefs(startXRefOffset); err != nil {
		return 0, fmt.Errorf("analisi xref/trailer fallita: %w", err)
	}

	// 4. Risolvi /Root
	rootObjNum, err := p.extractRef(p.trailerDict, "/Root")
	if err != nil {
		return 0, fmt.Errorf("trailer privo di /Root valido: %w", err)
	}

	catalogDict, err := p.resolveDict(rootObjNum)
	if err != nil {
		return 0, fmt.Errorf("risoluzione catalogo (obj %d) fallita: %w", rootObjNum, err)
	}

	if !regexp.MustCompile(`/Type\s*/Catalog\b`).MatchString(catalogDict) {
		return 0, fmt.Errorf("Root non è un Catalog")
	}
	// 5. Risolvi /Pages dal catalogo
	pagesObjNum, err := p.extractRef(catalogDict, "/Pages")
	if err != nil {
		return 0, fmt.Errorf("catalogo privo di riferimento /Pages: %w", err)
	}

	count, err := p.countPageTree(pagesObjNum, make(map[int]bool), 0)
	if err != nil {
		return 0, err
	}
	if count <= 0 {
		return 0, fmt.Errorf("conteggio pagine non valido o zero")
	}

	return count, nil
}

func (p *PDFParser) loadXRefs(offset int64) error {
	visited := make(map[int64]bool)
	curOffset := offset

	for curOffset > 0 && curOffset < int64(len(p.data)) {
		if visited[curOffset] {
			return fmt.Errorf("ciclo nella catena xref")
		}
		visited[curOffset] = true

		chunk := p.data[curOffset:]
		trimmed := bytes.TrimLeft(chunk, " \t\r\n")

		if bytes.HasPrefix(trimmed, []byte("xref")) {
			// Tabella xref classica
			nextPrev, err := p.parseClassicXRef(curOffset)
			if err != nil {
				return err
			}
			curOffset = nextPrev
		} else {
			// Stream xref (PDF 1.5+)
			nextPrev, err := p.parseXRefStream(curOffset)
			if err != nil {
				return err
			}
			curOffset = nextPrev
		}
	}

	if len(p.objects) == 0 {
		return fmt.Errorf("nessun oggetto valido trovato nella tabella xref")
	}
	if p.trailerDict == "" {
		return fmt.Errorf("dizionario trailer mancante")
	}
	return nil
}

func (p *PDFParser) parseClassicXRef(offset int64) (int64, error) {
	str := string(p.data[offset:])
	trailerIdx := strings.Index(str, "trailer")
	if trailerIdx == -1 {
		return 0, fmt.Errorf("sezione trailer non trovata dopo xref")
	}

	xrefPart := str[:trailerIdx]
	lines := strings.Split(xrefPart, "\n")
	var curObj = 0
	var countRemaining = 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "xref" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 {
			start, e1 := strconv.Atoi(parts[0])
			cnt, e2 := strconv.Atoi(parts[1])
			if e1 == nil && e2 == nil {
				curObj = start
				countRemaining = cnt
				continue
			}
		}
		if len(parts) >= 3 && countRemaining > 0 {
			off, err := strconv.ParseInt(parts[0], 10, 64)
			flag := parts[2]
			if err == nil && flag == "n" {
				if _, exists := p.objects[curObj]; !exists {
					p.objects[curObj] = objLocation{fileOffset: off}
				}
			}
			curObj++
			countRemaining--
		}
	}

	// Leggi dizionario trailer
	afterTrailer := str[trailerIdx+len("trailer"):]
	dictStart := strings.Index(afterTrailer, "<<")
	if dictStart == -1 {
		return 0, fmt.Errorf("apertura dizionario << non trovata in trailer")
	}
	dictEnd := p.findMatchingDictEnd(afterTrailer[dictStart:])
	if dictEnd == -1 {
		return 0, fmt.Errorf("chiusura dizionario >> non trovata in trailer")
	}

	if p.trailerDict == "" {
		p.trailerDict = afterTrailer[dictStart : dictStart+dictEnd]
	}

	// Controlla se c'è un /Prev
	prev, _ := p.extractInt64(afterTrailer[dictStart:dictStart+dictEnd], "/Prev")
	return prev, nil
}

func (p *PDFParser) parseXRefStream(offset int64) (int64, error) {
	chunk := p.data[offset:]
	objMatch := regexp.MustCompile(`^(\d+)\s+(\d+)\s+obj`).FindSubmatch(chunk)
	if len(objMatch) < 3 {
		return 0, fmt.Errorf("inizio oggetto xref stream non valido all'offset %d", offset)
	}

	dictStart := bytes.Index(chunk, []byte("<<"))
	if dictStart == -1 {
		return 0, fmt.Errorf("dizionario xref stream mancante")
	}
	dictEnd := p.findMatchingDictEnd(string(chunk[dictStart:]))
	if dictEnd == -1 {
		return 0, fmt.Errorf("chiusura dizionario xref stream mancante")
	}

	dictStr := string(chunk[dictStart : dictStart+dictEnd])
	if p.trailerDict == "" {
		p.trailerDict = dictStr
	}

	// Leggi array /W [w0 w1 w2]
	reW := regexp.MustCompile(`/W\s*\[\s*(\d+)\s+(\d+)\s+(\d+)\s*\]`)
	wm := reW.FindStringSubmatch(dictStr)
	if len(wm) < 4 {
		return 0, fmt.Errorf("/W mancante o non valido in xref stream")
	}
	w0, e0 := strconv.Atoi(wm[1])
	w1, e1 := strconv.Atoi(wm[2])
	w2, e2 := strconv.Atoi(wm[3])
	if e0 != nil || e1 != nil || e2 != nil || w0 > 8 || w1 > 8 || w2 > 8 {
		return 0, fmt.Errorf("campi /W fuori limite")
	}
	entryLen := w0 + w1 + w2
	if entryLen <= 0 {
		return 0, fmt.Errorf("lunghezza record xref non valida")
	}

	// Leggi /Size
	size, err := p.extractInt(dictStr, "/Size")
	if err != nil || size <= 0 || size > maxPDFObjects {
		return 0, fmt.Errorf("/Size mancante o fuori limite in xref stream")
	}

	// Leggi /Index [start count ...] se presente, altrimenti [0 Size]
	var indexRanges [][2]int
	reIndex := regexp.MustCompile(`/Index\s*\[(.*?)\]`)
	if im := reIndex.FindStringSubmatch(dictStr); len(im) >= 2 {
		idxFields := strings.Fields(im[1])
		for i := 0; i+1 < len(idxFields); i += 2 {
			s, e1 := strconv.Atoi(idxFields[i])
			c, e2 := strconv.Atoi(idxFields[i+1])
			if e1 != nil || e2 != nil || s < 0 || c < 0 || s > maxPDFObjects || c > maxPDFObjects-s {
				return 0, fmt.Errorf("/Index fuori limite")
			}
			if c > 0 {
				indexRanges = append(indexRanges, [2]int{s, c})
			}
		}
	}
	if len(indexRanges) == 0 {
		indexRanges = append(indexRanges, [2]int{0, size})
	}

	// Estrai e decomprimi lo stream
	streamData, err := p.extractStreamBytes(chunk[dictStart+dictEnd:], dictStr)
	if err != nil {
		return 0, fmt.Errorf("estrazione stream xref fallita: %w", err)
	}

	byteOffset := 0
	for _, rng := range indexRanges {
		startObj := rng[0]
		cnt := rng[1]
		for i := 0; i < cnt; i++ {
			if byteOffset+entryLen > len(streamData) {
				return 0, fmt.Errorf("stream xref troncato")
			}
			rec := streamData[byteOffset : byteOffset+entryLen]
			byteOffset += entryLen

			fType := 1
			if w0 > 0 {
				fType = int(readBytesInt(rec[0:w0]))
			}
			fVal1 := readBytesInt(rec[w0 : w0+w1])
			fVal2 := 0
			if w2 > 0 {
				fVal2 = int(readBytesInt(rec[w0+w1 : w0+w1+w2]))
			}

			objNum := startObj + i
			switch fType {
			case 0:
				// Oggetto libero
			case 1:
				// Oggetto diretto in file
				if _, exists := p.objects[objNum]; !exists {
					p.objects[objNum] = objLocation{fileOffset: fVal1}
				}
			case 2:
				// Oggetto compresso dentro ObjStm
				if _, exists := p.objects[objNum]; !exists {
					p.objects[objNum] = objLocation{
						isCompressed: true,
						stmObjNum:    int(fVal1),
						stmIndex:     fVal2,
					}
				}
			}
		}
	}

	prev, _ := p.extractInt64(dictStr, "/Prev")
	return prev, nil
}

func (p *PDFParser) resolveDict(objNum int) (string, error) {
	loc, ok := p.objects[objNum]
	if !ok {
		return "", fmt.Errorf("oggetto %d non presente nella tabella xref", objNum)
	}

	if loc.isCompressed {
		// Risolvi da cache / decompress ObjStm
		if objs, ok := p.objStmCache[loc.stmObjNum]; ok {
			if d, found := objs[objNum]; found {
				return d, nil
			}
		}
		if err := p.loadObjectStream(loc.stmObjNum); err != nil {
			return "", err
		}
		if objs, ok := p.objStmCache[loc.stmObjNum]; ok {
			if d, found := objs[objNum]; found {
				return d, nil
			}
		}
		return "", fmt.Errorf("oggetto %d non trovato all'interno di ObjStm %d", objNum, loc.stmObjNum)
	}

	// Oggetto diretto nel file
	if loc.fileOffset < 0 || loc.fileOffset >= int64(len(p.data)) {
		return "", fmt.Errorf("offset oggetto %d (%d) fuori dal file", objNum, loc.fileOffset)
	}

	chunk := p.data[loc.fileOffset:]
	objHeader := regexp.MustCompile(`^(\d+)\s+\d+\s+obj\b`).FindSubmatch(chunk)
	if len(objHeader) < 2 || string(objHeader[1]) != strconv.Itoa(objNum) {
		return "", fmt.Errorf("xref non corrisponde all'oggetto %d", objNum)
	}
	dictStart := bytes.Index(chunk, []byte("<<"))
	if dictStart == -1 {
		return "", fmt.Errorf("dizionario oggetto %d non trovato all'offset %d", objNum, loc.fileOffset)
	}
	dictEnd := p.findMatchingDictEnd(string(chunk[dictStart:]))
	if dictEnd == -1 {
		return "", fmt.Errorf("chiusura dizionario non trovata per oggetto %d", objNum)
	}
	return string(chunk[dictStart : dictStart+dictEnd]), nil
}

func (p *PDFParser) loadObjectStream(stmObjNum int) error {
	loc, ok := p.objects[stmObjNum]
	if !ok || loc.isCompressed {
		return fmt.Errorf("ObjStm %d non trovato come oggetto diretto", stmObjNum)
	}

	if loc.fileOffset < 0 || loc.fileOffset >= int64(len(p.data)) {
		return fmt.Errorf("offset ObjStm %d fuori dal file", stmObjNum)
	}
	chunk := p.data[loc.fileOffset:]
	dictStart := bytes.Index(chunk, []byte("<<"))
	if dictStart == -1 {
		return fmt.Errorf("dizionario ObjStm %d mancante", stmObjNum)
	}
	dictEnd := p.findMatchingDictEnd(string(chunk[dictStart:]))
	if dictEnd == -1 {
		return fmt.Errorf("chiusura dizionario ObjStm %d mancante", stmObjNum)
	}
	dictStr := string(chunk[dictStart : dictStart+dictEnd])

	numObjects, err := p.extractInt(dictStr, "/N")
	if err != nil || numObjects <= 0 || numObjects > maxPDFObjects {
		return fmt.Errorf("/N mancante o non valido in ObjStm %d", stmObjNum)
	}
	firstOffset, err := p.extractInt(dictStr, "/First")
	if err != nil || firstOffset < 0 {
		return fmt.Errorf("/First mancante in ObjStm %d", stmObjNum)
	}

	streamBytes, err := p.extractStreamBytes(chunk[dictStart+dictEnd:], dictStr)
	if err != nil {
		return fmt.Errorf("decomprimere stream ObjStm %d fallito: %w", stmObjNum, err)
	}

	if firstOffset > len(streamBytes) {
		return fmt.Errorf("/First supera la dimensione dello stream decompresso in ObjStm %d", stmObjNum)
	}

	headerStr := string(streamBytes[:firstOffset])
	fields := strings.Fields(headerStr)
	if len(fields) < numObjects*2 {
		return fmt.Errorf("coppie oggetto/offset incomplete nell'header ObjStm %d", stmObjNum)
	}

	parsedObjects := make(map[int]string)
	for i := 0; i < numObjects; i++ {
		oNum, e1 := strconv.Atoi(fields[i*2])
		oOff, e2 := strconv.Atoi(fields[i*2+1])
		if e1 != nil || e2 != nil {
			continue
		}
		if oOff < 0 || oOff > len(streamBytes)-firstOffset {
			return fmt.Errorf("offset oggetto compresso fuori dallo stream")
		}
		targetStart := firstOffset + oOff
		if targetStart < len(streamBytes) {
			targetChunk := streamBytes[targetStart:]
			dStart := bytes.Index(targetChunk, []byte("<<"))
			if dStart != -1 {
				dEnd := p.findMatchingDictEnd(string(targetChunk[dStart:]))
				if dEnd != -1 {
					parsedObjects[oNum] = string(targetChunk[dStart : dStart+dEnd])
				}
			}
		}
	}

	p.objStmCache[stmObjNum] = parsedObjects
	return nil
}

func (p *PDFParser) extractStreamBytes(afterDict []byte, dictStr string) ([]byte, error) {
	afterDict = bytes.TrimLeft(afterDict, " \t\r\n")
	if !bytes.HasPrefix(afterDict, []byte("stream")) {
		return nil, fmt.Errorf("marcatore stream mancante")
	}
	start := len("stream")
	if start < len(afterDict) && afterDict[start] == '\r' {
		start++
	}
	if start < len(afterDict) && afterDict[start] == '\n' {
		start++
	} else if start == len("stream") {
		return nil, fmt.Errorf("newline stream mancante")
	}
	n, err := p.extractInt(dictStr, "/Length")
	if err != nil {
		return nil, err
	}
	if m := regexp.MustCompile(`/Length\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(dictStr); len(m) > 1 {
		id, _ := strconv.Atoi(m[1])
		loc, ok := p.objects[id]
		if !ok || loc.isCompressed || loc.fileOffset < 0 || loc.fileOffset >= int64(len(p.data)) {
			return nil, fmt.Errorf("riferimento Length non supportato o fuori limite")
		}
		value := regexp.MustCompile(`^\d+\s+\d+\s+obj\s+(\d+)\s+endobj`).FindSubmatch(p.data[loc.fileOffset:])
		if len(value) < 2 {
			return nil, fmt.Errorf("oggetto Length non valido")
		}
		n, err = strconv.Atoi(string(value[1]))
		if err != nil {
			return nil, err
		}
	}
	if n < 0 || n > len(afterDict)-start {
		return nil, fmt.Errorf("Length supera i dati dello stream")
	}
	rawStream := afterDict[start : start+n]
	if !bytes.HasPrefix(bytes.TrimLeft(afterDict[start+n:], " \t\r\n"), []byte("endstream")) {
		return nil, fmt.Errorf("endstream non corrisponde a Length")
	}
	fm := regexp.MustCompile(`/Filter\s*(/\w+|\[[^\]]*\])`).FindStringSubmatch(dictStr)
	if len(fm) == 0 {
		return rawStream, nil
	}
	filter := strings.Join(strings.Fields(fm[1]), "")
	if filter != "/FlateDecode" && filter != "[/FlateDecode]" {
		return nil, fmt.Errorf("filtro PDF non supportato: %s", fm[1])
	}
	zr, err := zlib.NewReader(bytes.NewReader(rawStream))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	decoded, err := io.ReadAll(io.LimitReader(zr, maxPDFStreamBytes+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxPDFStreamBytes {
		return nil, fmt.Errorf("stream PDF decompresso oltre il limite")
	}
	predictor := 1
	colors := 1
	bits := 8
	columns := 1
	for _, item := range []struct {
		key    string
		target *int
	}{{"/Predictor", &predictor}, {"/Colors", &colors}, {"/BitsPerComponent", &bits}, {"/Columns", &columns}} {
		if v, e := p.extractInt(dictStr, item.key); e == nil {
			*item.target = v
		}
	}
	return decodePDFPredictor(decoded, predictor, colors, bits, columns)
}

func (p *PDFParser) findMatchingDictEnd(s string) int {
	if !strings.HasPrefix(s, "<<") {
		return -1
	}
	depth, strDepth := 0, 0
	comment, escaped, hexString := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if comment {
			if c == '\r' || c == '\n' {
				comment = false
			}
			continue
		}
		if strDepth > 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '(' {
				strDepth++
			}
			if c == ')' {
				strDepth--
			}
			continue
		}
		if hexString {
			if c == '>' {
				hexString = false
			}
			continue
		}
		if c == '%' {
			comment = true
			continue
		}
		if c == '(' {
			strDepth = 1
			continue
		}
		if c == '<' && i+1 < len(s) && s[i+1] == '<' {
			depth++
			if depth > 128 {
				return -1
			}
			i++
			continue
		}
		if c == '<' {
			hexString = true
			continue
		}
		if c == '>' && i+1 < len(s) && s[i+1] == '>' {
			depth--
			i++
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func (p *PDFParser) extractRef(dict, key string) (int, error) {
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `\s+(\d+)\s+(\d+)\s+R`)
	m := re.FindStringSubmatch(dict)
	if len(m) < 2 {
		return 0, fmt.Errorf("chiave %s mancante o non è un riferimento indiretto", key)
	}
	return strconv.Atoi(m[1])
}

func (p *PDFParser) extractInt(dict, key string) (int, error) {
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `\s+(\d+)`)
	m := re.FindStringSubmatch(dict)
	if len(m) < 2 {
		return 0, fmt.Errorf("chiave %s mancante o non è un intero", key)
	}
	return strconv.Atoi(m[1])
}

func (p *PDFParser) extractInt64(dict, key string) (int64, error) {
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `\s+(\d+)`)
	m := re.FindStringSubmatch(dict)
	if len(m) < 2 {
		return 0, fmt.Errorf("chiave %s mancante o non è un intero", key)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

func (p *PDFParser) countPageTree(obj int, seen map[int]bool, depth int) (int, error) {
	if depth > 128 || len(seen) >= maxPDFObjects || seen[obj] {
		return 0, fmt.Errorf("albero pagine ciclico, duplicato o troppo profondo")
	}
	seen[obj] = true
	dict, err := p.resolveDict(obj)
	if err != nil {
		return 0, err
	}
	kind := regexp.MustCompile(`/Type\s*/(Page|Pages)\b`).FindStringSubmatch(dict)
	if len(kind) < 2 {
		return 0, fmt.Errorf("tipo del nodo pagine non valido")
	}
	if kind[1] == "Page" {
		return 1, nil
	}
	kids := regexp.MustCompile(`(?s)/Kids\s*\[(.*?)\]`).FindStringSubmatch(dict)
	if len(kids) < 2 {
		return 0, fmt.Errorf("Kids mancante")
	}
	re := regexp.MustCompile(`(\d+)\s+\d+\s+R`)
	refs := re.FindAllStringSubmatch(kids[1], -1)
	if strings.TrimSpace(re.ReplaceAllString(kids[1], "")) != "" {
		return 0, fmt.Errorf("riferimento Kids non valido")
	}
	total := 0
	for _, ref := range refs {
		id, e := strconv.Atoi(ref[1])
		if e != nil {
			return 0, e
		}
		n, e := p.countPageTree(id, seen, depth+1)
		if e != nil {
			return 0, e
		}
		total += n
	}
	declared, e := p.extractInt(dict, "/Count")
	if e != nil || declared != total || total == 0 {
		return 0, fmt.Errorf("Count non corrisponde alle pagine reali: dichiarate %d, reali %d", declared, total)
	}
	return total, nil
}

func readBytesInt(b []byte) int64 {
	var res int64
	for _, x := range b {
		res = (res << 8) | int64(x)
	}
	return res
}
