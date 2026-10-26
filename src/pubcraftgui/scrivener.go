package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ScrivenerProject rappresenta la struttura del file .scrivx
type ScrivenerProject struct {
	XMLName    xml.Name      `xml:"ScrivenerProject"`
	Title      string        `xml:"Title,attr"`
	Author     string        `xml:"Author,attr"`
	Identifier string        `xml:"Identifier,attr"`
	Version    string        `xml:"Version,attr"`
	Binder     ScrivBinder   `xml:"Binder"`
}

type ScrivBinder struct {
	Items []ScrivItem `xml:"BinderItem"`
}

type ScrivItem struct {
	UUID      string        `xml:"UUID,attr"`
	ID        string        `xml:"ID,attr"`
	Type      string        `xml:"Type,attr"`
	Title     string        `xml:"Title"`
	MetaData  ScrivMeta     `xml:"MetaData"`
	Children  *ScrivChildren `xml:"Children"`
}

type ScrivMeta struct {
	IncludeInCompile string `xml:"IncludeInCompile"`
}

type ScrivChildren struct {
	Items []ScrivItem `xml:"BinderItem"`
}

// scrivFS astrae la lettura dei file da cartella .scriv o da archivio zip
type scrivFS interface {
	readFile(relPath string) ([]byte, error)
	findScrivx() (string, []byte, error)
	close() error
}

// scrivDirFS legge da filesystem
type scrivDirFS struct {
	root string
}

func (d *scrivDirFS) readFile(relPath string) ([]byte, error) {
	clean := filepath.Clean(relPath)
	p := filepath.Join(d.root, clean)
	return os.ReadFile(p)
}

func (d *scrivDirFS) findScrivx() (string, []byte, error) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return "", nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".scrivx") {
			data, err := os.ReadFile(filepath.Join(d.root, e.Name()))
			return e.Name(), data, err
		}
	}
	return "", nil, fmt.Errorf("nessun file .scrivx trovato in %s", d.root)
}

func (d *scrivDirFS) close() error {
	return nil
}

// scrivZipFS legge da un file ZIP (es. .scriv.zip o estratto da .scrivtemplate)
type scrivZipFS struct {
	zr     *zip.Reader
	closer io.Closer
	prefix string
}

func newScrivZipFS(zr *zip.Reader, closer io.Closer) (*scrivZipFS, error) {
	fs := &scrivZipFS{zr: zr, closer: closer}
	// Determina l'eventuale prefisso comune della cartella .scriv all'interno dello zip
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".scrivx") {
			dir := filepath.Dir(f.Name)
			if dir == "." {
				fs.prefix = ""
			} else {
				fs.prefix = dir + "/"
			}
			break
		}
	}
	return fs, nil
}

func (z *scrivZipFS) readFile(relPath string) ([]byte, error) {
	target := filepath.ToSlash(filepath.Clean(z.prefix + relPath))
	// Cerca con match case-insensitive per robustezza tra OS
	targetLower := strings.ToLower(target)
	for _, f := range z.zr.File {
		if strings.ToLower(f.Name) == targetLower {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, os.ErrNotExist
}

func (z *scrivZipFS) findScrivx() (string, []byte, error) {
	for _, f := range z.zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".scrivx") {
			rc, err := f.Open()
			if err != nil {
				return "", nil, err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			return f.Name, data, err
		}
	}
	return "", nil, fmt.Errorf("nessun file .scrivx trovato nell'archivio")
}

func (z *scrivZipFS) close() error {
	if z.closer != nil {
		return z.closer.Close()
	}
	return nil
}

// openScrivenerFS apre una sorgente .scriv, .scrivx, .scrivtemplate o .zip
func openScrivenerFS(src string) (scrivFS, string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, "", err
	}

	cleanSrc := filepath.Clean(src)
	baseName := strings.TrimSuffix(filepath.Base(cleanSrc), filepath.Ext(cleanSrc))

	// Caso 1: Directory .scriv (o cartella contenente .scrivx)
	if info.IsDir() {
		// Controlla se contiene direttamente un .scrivx
		entries, err := os.ReadDir(cleanSrc)
		if err == nil {
			for _, e := range entries {
				if strings.HasSuffix(strings.ToLower(e.Name()), ".scrivx") {
					return &scrivDirFS{root: cleanSrc}, baseName, nil
				}
				// O se c'è una sottocartella .scriv
				if e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".scriv") {
					sub := filepath.Join(cleanSrc, e.Name())
					return &scrivDirFS{root: sub}, strings.TrimSuffix(e.Name(), ".scriv"), nil
				}
			}
		}
		return &scrivDirFS{root: cleanSrc}, baseName, nil
	}

	ext := strings.ToLower(filepath.Ext(cleanSrc))

	// Caso 2: File .scrivx selezionato singolarmente
	if ext == ".scrivx" {
		dir := filepath.Dir(cleanSrc)
		return &scrivDirFS{root: dir}, strings.TrimSuffix(filepath.Base(dir), ".scriv"), nil
	}

	// Caso 3: File .scrivtemplate
	if ext == ".scrivtemplate" {
		data, err := os.ReadFile(cleanSrc)
		if err != nil {
			return nil, "", fmt.Errorf("impossibile leggere template: %w", err)
		}
		type templateXML struct {
			Title          string `xml:"Title"`
			ProjectZipData string `xml:"ProjectZipData"`
		}
		var tmpl templateXML
		if err := xml.Unmarshal(data, &tmpl); err != nil {
			return nil, "", fmt.Errorf("XML template non valido: %w", err)
		}
		if tmpl.ProjectZipData == "" {
			return nil, "", fmt.Errorf("ProjectZipData non trovato nel template")
		}
		zipBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(tmpl.ProjectZipData))
		if err != nil {
			return nil, "", fmt.Errorf("decodifica base64 del progetto fallita: %w", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			return nil, "", fmt.Errorf("archivio zip del template non valido: %w", err)
		}
		fs, err := newScrivZipFS(zr, nil)
		title := baseName
		if tmpl.Title != "" {
			title = tmpl.Title
		}
		return fs, title, err
	}

	// Caso 4: Archivio ZIP (.scriv.zip o .zip)
	if ext == ".zip" || strings.HasSuffix(strings.ToLower(cleanSrc), ".scriv.zip") {
		zf, err := os.Open(cleanSrc)
		if err != nil {
			return nil, "", err
		}
		zr, err := zip.NewReader(zf, info.Size())
		if err != nil {
			zf.Close()
			return nil, "", fmt.Errorf("file zip non valido: %w", err)
		}
		fs, err := newScrivZipFS(zr, zf)
		return fs, baseName, err
	}

	return nil, "", fmt.Errorf("formato Scrivener non supportato: %s", ext)
}

// isScrivenerZip verifica se uno zip contiene un progetto Scrivener (.scrivx)
func isScrivenerZip(src string) bool {
	info, err := os.Stat(src)
	if err != nil {
		return false
	}
	f, err := os.Open(src)
	if err != nil {
		return false
	}
	defer f.Close()
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return false
	}
	for _, entry := range zr.File {
		if strings.HasSuffix(strings.ToLower(entry.Name), ".scrivx") {
			return true
		}
	}
	return false
}

// ScrivChapter rappresenta un capitolo estratto da Scrivener
type ScrivChapter struct {
	Title string
	Lines []string
}

// importScrivenerVault importa un progetto Scrivener (.scriv, .scrivx, .scrivtemplate, .scriv.zip)
// e genera un vault Markdown pronto per PubCraft.
func importScrivenerVault(src string) (*Book, error) {
	fs, defTitle, err := openScrivenerFS(src)
	if err != nil {
		return nil, fmt.Errorf("apertura progetto Scrivener fallita: %w", err)
	}
	defer fs.close()

	_, scrivxData, err := fs.findScrivx()
	if err != nil {
		return nil, fmt.Errorf("file di progetto .scrivx non trovato: %w", err)
	}

	var proj ScrivenerProject
	if err := xml.Unmarshal(scrivxData, &proj); err != nil {
		return nil, fmt.Errorf("lettura XML .scrivx fallita: %w", err)
	}

	title := proj.Title
	if title == "" {
		title = defTitle
	}
	author := proj.Author

	// Estrai i capitoli navigando il Binder
	chapters := extractScrivenerChapters(fs, &proj)
	if len(chapters) == 0 {
		return nil, fmt.Errorf("nessun capitolo o testo leggibile trovato nel progetto Scrivener")
	}

	// Cartella di destinazione accanto al file originale
	parentDir := filepath.Dir(filepath.Clean(src))
	if strings.HasSuffix(strings.ToLower(parentDir), ".scriv") {
		parentDir = filepath.Dir(parentDir)
	}
	name := slugify(title)
	if name == "" || name == "capitolo" {
		name = "libro-scrivener"
	}
	dest := filepath.Join(parentDir, name+"-md")
	for i := 2; fileExists(dest); i++ {
		dest = filepath.Join(parentDir, fmt.Sprintf("%s-md-%d", name, i))
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(dest, "cover"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "images"), 0o755)

	bookUUID, _ := newUUID()
	book := &Book{
		Title:      title,
		Author:     author,
		Language:   "it",
		Style:      "classico",
		Opening:    "drop-cap",
		SceneBreak: "asterischi",
		UUID:       bookUUID,
	}

	for i, c := range chapters {
		t := c.Title
		if t == "" {
			t = fmt.Sprintf("Capitolo %d", i+1)
		}
		file := fmt.Sprintf("%02d-%s.md", i+1, slugify(t))
		id, _ := newUUID()
		body := ""
		if len(c.Lines) > 0 {
			body = strings.Join(c.Lines, "\n\n") + "\n"
		}
		md := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", id, t, body)
		if err := os.WriteFile(filepath.Join(dest, file), []byte(md), 0o644); err != nil {
			return nil, err
		}
		book.Chapters = append(book.Chapters, Chapter{File: file, ID: id, Title: t})
	}

	setVault(dest)
	populateChapterStats(book, dest)
	if err := writeBookYAML(book); err != nil {
		return nil, err
	}
	return book, nil
}

// extractScrivenerChapters cerca la cartella Draft/Manuscript ed estrae i capitoli ordinati
func extractScrivenerChapters(fs scrivFS, proj *ScrivenerProject) []ScrivChapter {
	var chapters []ScrivChapter

	// 1. Cerca il BinderItem di tipo DraftFolder
	var draftItem *ScrivItem
	for i := range proj.Binder.Items {
		item := &proj.Binder.Items[i]
		if strings.EqualFold(item.Type, "DraftFolder") ||
			strings.EqualFold(item.Title, "Draft") ||
			strings.EqualFold(item.Title, "Manuscript") ||
			strings.EqualFold(item.Title, "Manoscritto") {
			draftItem = item
			break
		}
	}

	// 2. Se trovato il Draft, naviga i suoi elementi
	if draftItem != nil && draftItem.Children != nil && len(draftItem.Children.Items) > 0 {
		for _, child := range draftItem.Children.Items {
			chapters = append(chapters, collectItemChapters(fs, child)...)
		}
	}

	// 3. Fallback: se Draft è vuoto (es. template o progetto atipico), estrai tutti i testi non-trash
	if len(chapters) == 0 {
		for _, item := range proj.Binder.Items {
			tLower := strings.ToLower(item.Type)
			if strings.Contains(tLower, "trash") || strings.Contains(tLower, "research") {
				continue
			}
			chapters = append(chapters, collectItemChapters(fs, item)...)
		}
	}

	return chapters
}

func collectItemChapters(fs scrivFS, item ScrivItem) []ScrivChapter {
	if strings.EqualFold(item.MetaData.IncludeInCompile, "no") {
		return nil
	}

	var results []ScrivChapter
	id := item.UUID
	if id == "" {
		id = item.ID
	}

	itemText := readScrivenerDoc(fs, id)

	// Caso A: Elemento di tipo Text
	if strings.EqualFold(item.Type, "Text") {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = "Senza titolo"
		}
		var lines []string
		if itemText != "" {
			lines = splitParagraphs(itemText)
		}
		results = append(results, ScrivChapter{
			Title: title,
			Lines: lines,
		})
		return results
	}

	// Caso B: Elemento di tipo Folder (possibile capitolo con scene, o parte con capitoli)
	hasChildren := item.Children != nil && len(item.Children.Items) > 0
	if hasChildren {
		// Controlla se i figli sono tutti Text (Capitolo con scene)
		allText := true
		for _, c := range item.Children.Items {
			if !strings.EqualFold(c.Type, "Text") {
				allText = false
				break
			}
		}

		if allText {
			// Unisci le scene nel capitolo corrente separate da stacchi di scena
			var lines []string
			if itemText != "" {
				lines = append(lines, splitParagraphs(itemText)...)
			}
			for idx, scene := range item.Children.Items {
				if strings.EqualFold(scene.MetaData.IncludeInCompile, "no") {
					continue
				}
				sID := scene.UUID
				if sID == "" {
					sID = scene.ID
				}
				sceneText := readScrivenerDoc(fs, sID)
				if sceneText != "" {
					if len(lines) > 0 && idx > 0 {
						lines = append(lines, "* * *")
					}
					lines = append(lines, splitParagraphs(sceneText)...)
				}
			}
			title := strings.TrimSpace(item.Title)
			if title == "" {
				title = "Capitolo"
			}
			if len(lines) > 0 {
				results = append(results, ScrivChapter{
					Title: title,
					Lines: lines,
				})
			}
			return results
		}

		// Se invece contiene ulteriori cartelle (es. Parte con Capitoli), naviga ricorsivamente
		for _, c := range item.Children.Items {
			results = append(results, collectItemChapters(fs, c)...)
		}
		return results
	}

	// Folder isolata con testo ma senza figli
	if itemText != "" {
		results = append(results, ScrivChapter{
			Title: strings.TrimSpace(item.Title),
			Lines: splitParagraphs(itemText),
		})
	}
	return results
}

// readScrivenerDoc tenta di leggere il file RTF del documento da Scrivener 3 o Scrivener 2
func readScrivenerDoc(fs scrivFS, id string) string {
	if id == "" {
		return ""
	}

	// Possibili percorsi interni di Scrivener
	candidates := []string{
		// Scrivener 3 (UUID): Files/Data/[UUID]/content.rtf
		filepath.Join("Files", "Data", id, "content.rtf"),
		filepath.Join("Files", "Data", id, "content.txt"),
		// Scrivener 2 (ID numerico): Files/Docs/[ID].rtf o Files/Data/[ID]/content.rtf
		filepath.Join("Files", "Docs", id+".rtf"),
		filepath.Join("Files", "Docs", id+".txt"),
	}

	for _, cand := range candidates {
		data, err := fs.readFile(cand)
		if err == nil && len(data) > 0 {
			if strings.HasSuffix(cand, ".txt") {
				return cleanScrivenerMarkdown(string(data))
			}
			return cleanScrivenerMarkdown(rtfToMarkdown(string(data)))
		}
	}
	return ""
}

func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")
	var out []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// rtfToMarkdown converte RTF pulito in Markdown preservando corsivi, grassetti e paragrafi
func rtfToMarkdown(raw string) string {
	var buf strings.Builder
	n := len(raw)
	i := 0
	skipDepth := 0
	bold := false
	italic := false

	for i < n {
		c := raw[i]

		if c == '{' {
			if skipDepth > 0 {
				skipDepth++
				i++
				continue
			}
			i++
			if i < n && raw[i] == '\\' {
				// Leggi comando di gruppo
				j := i + 1
				for j < n && ((raw[j] >= 'a' && raw[j] <= 'z') || (raw[j] >= 'A' && raw[j] <= 'Z')) {
					j++
				}
				cmd := raw[i+1 : j]
				switch cmd {
				case "fonttbl", "colortbl", "expandedcolortbl", "stylesheet", "info", "pict",
					"header", "headerl", "headerr", "footer", "footerl", "footerr",
					"generator", "doccomm", "keywords", "operator",
					"listtable", "listoverridetable", "pntext":
					skipDepth++
				}
				if i+1 < n && raw[i+1] == '*' {
					skipDepth++
				}
			}
			continue
		}

		if c == '}' {
			if skipDepth > 0 {
				skipDepth--
			}
			i++
			continue
		}

		if skipDepth > 0 {
			i++
			continue
		}

		if c == '\\' {
			i++
			if i >= n {
				break
			}
			nc := raw[i]
			if nc == '\\' || nc == '{' || nc == '}' {
				buf.WriteByte(nc)
				i++
				continue
			}
			if nc == '\'' {
				// Hex byte escape: \'hh (CP1252)
				if i+2 < n {
					hexStr := raw[i+1 : i+3]
					if b, err := strconv.ParseUint(hexStr, 16, 8); err == nil {
						buf.WriteString(cp1252ByteToUTF8(byte(b)))
					}
					i += 3
					continue
				}
			}
			if nc == '~' {
				buf.WriteString(" ")
				i++
				continue
			}
			if nc == '_' {
				buf.WriteString("-")
				i++
				continue
			}

			// Leggi nome comando
			j := i
			for j < n && ((raw[j] >= 'a' && raw[j] <= 'z') || (raw[j] >= 'A' && raw[j] <= 'Z')) {
				j++
			}
			cmd := raw[i:j]
			i = j

			// Leggi eventuale parametro numerico
			numStart := i
			if i < n && (raw[i] == '-' || (raw[i] >= '0' && raw[i] <= '9')) {
				i++
				for i < n && (raw[i] >= '0' && raw[i] <= '9') {
					i++
				}
			}
			numStr := raw[numStart:i]

			// Salta spazio delimitatore opzionale del comando RTF
			if i < n && raw[i] == ' ' {
				i++
			}

			switch cmd {
			case "par":
				buf.WriteString("\n\n")
			case "line":
				buf.WriteString("\n")
			case "page":
				buf.WriteString("\n\n* * *\n\n")
			case "tab":
				buf.WriteString(" ")
			case "bullet":
				buf.WriteString("- ")
			case "b":
				val := 1
				if numStr != "" {
					val, _ = strconv.Atoi(numStr)
				}
				if val == 0 && bold {
					closeFormattingMarker(&buf, "**")
					bold = false
				} else if val > 0 && !bold {
					buf.WriteString("**")
					bold = true
				}
			case "i":
				val := 1
				if numStr != "" {
					val, _ = strconv.Atoi(numStr)
				}
				if val == 0 && italic {
					closeFormattingMarker(&buf, "*")
					italic = false
				} else if val > 0 && !italic {
					buf.WriteString("*")
					italic = true
				}
			case "u":
				if numStr != "" {
					code, _ := strconv.Atoi(numStr)
					if code < 0 {
						code += 65536
					}
					if utf8.ValidRune(rune(code)) {
						buf.WriteRune(rune(code))
					}
				}
				// Salta carattere alternativo opzionale se presente (\uN?)
				if i < n && raw[i] == '?' {
					i++
				}
			}
			continue
		}

		if c == '\r' || c == '\n' {
			str := buf.String()
			if len(str) > 0 && str[len(str)-1] != ' ' && str[len(str)-1] != '\n' {
				buf.WriteByte(' ')
			}
			i++
			continue
		}
		buf.WriteByte(c)
		i++
	}

	if bold {
		closeFormattingMarker(&buf, "**")
	}
	if italic {
		closeFormattingMarker(&buf, "*")
	}

	res := buf.String()
	// Normalizza spaziature eccessive
	res = strings.ReplaceAll(res, "\r", "")
	return strings.TrimSpace(res)
}

func closeFormattingMarker(buf *strings.Builder, marker string) {
	str := buf.String()
	trimmed := strings.TrimRight(str, " \t")
	trailingSpaces := str[len(trimmed):]
	buf.Reset()
	buf.WriteString(trimmed)
	buf.WriteString(marker)
	buf.WriteString(trailingSpaces)
}

// cp1252ByteToUTF8 mappa i caratteri CP1252 estesi a UTF-8 valido
func cp1252ByteToUTF8(b byte) string {
	if b < 0x80 {
		return string(b)
	}
	if b >= 0xA0 {
		return string(rune(b))
	}
	// Mappa 0x80 - 0x9F
	switch b {
	case 0x80:
		return "€"
	case 0x82:
		return "‚"
	case 0x83:
		return "ƒ"
	case 0x84:
		return "„"
	case 0x85:
		return "…"
	case 0x86:
		return "†"
	case 0x87:
		return "‡"
	case 0x88:
		return "ˆ"
	case 0x89:
		return "‰"
	case 0x8A:
		return "Š"
	case 0x8B:
		return "‹"
	case 0x8C:
		return "Œ"
	case 0x8E:
		return "Ž"
	case 0x91:
		return "‘"
	case 0x92:
		return "’"
	case 0x93:
		return "“"
	case 0x94:
		return "”"
	case 0x95:
		return "•"
	case 0x96:
		return "–"
	case 0x97:
		return "—"
	case 0x98:
		return "˜"
	case 0x99:
		return "™"
	case 0x9A:
		return "š"
	case 0x9B:
		return "›"
	case 0x9C:
		return "œ"
	case 0x9E:
		return "ž"
	case 0x9F:
		return "Ÿ"
	default:
		return ""
	}
}

var (
	reScrH1       = regexp.MustCompile(`<\$Scr_H::1>`)
	reScrH2       = regexp.MustCompile(`<\$Scr_H::2>`)
	reScrHN       = regexp.MustCompile(`<\$Scr_H::[0-9]+>`)
	reScrHEnd     = regexp.MustCompile(`(?i)<!\$Scr_H::[0-9]+>`)
	reScrAllTags  = regexp.MustCompile(`(?i)<[!$]+Scr[^>]*>`)
	reRTFListDots = regexp.MustCompile(`(?m)^\s*•[;\s]*$`)
	reEmptyBold   = regexp.MustCompile(`\*{4,}`)
	reOrphanBold  = regexp.MustCompile(`(?m)^\s*\*{2}\s*$`)
	reMultiNL     = regexp.MustCompile(`\n{3,}`)
)

// cleanScrivenerMarkdown pulisce e converte i tag strutturali di Scrivener in Markdown nativo PubCraft.
func cleanScrivenerMarkdown(raw string) string {
	if raw == "" {
		return ""
	}

	// 1. Converti i marcatori di intestazione Scrivener
	// H1 (<$Scr_H::1>) -> H1 PubCraft (#)
	// H2 (<$Scr_H::2>) -> H2 PubCraft (##)
	// H3+ -> H3 PubCraft (###)
	t := reScrH1.ReplaceAllString(raw, "\n§§PUB_H1§§\n")
	t = reScrH2.ReplaceAllString(t, "\n§§PUB_H2§§\n")
	t = reScrHN.ReplaceAllString(t, "\n§§PUB_H3§§\n")

	// Chiusura titolo Scrivener (<!$Scr_H::N>) -> a capo per separare il titolo dal corpo del paragrafo
	t = reScrHEnd.ReplaceAllString(t, "\n\n")

	// 2. Rimuovi tutti i restanti tag Scrivener (<$ScrKeepWithNext>, <!$Scr_H::...>, <$Scr_Ps::...>, ecc.)
	t = reScrAllTags.ReplaceAllString(t, "")

	// 3. Rimuovi residui di liste vuote RTF (es. bullet semicolon " •;;;") ed empty bold artifacts
	t = reRTFListDots.ReplaceAllString(t, "")
	t = reEmptyBold.ReplaceAllString(t, "")
	t = reOrphanBold.ReplaceAllString(t, "")

	// 4. Elabora riga per riga per associare correttamente i titoli
	lines := strings.Split(t, "\n")
	var out []string
	pendingHeading := ""

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		if line == "§§PUB_H1§§" {
			pendingHeading = "# "
			continue
		}
		if line == "§§PUB_H2§§" {
			pendingHeading = "## "
			continue
		}
		if line == "§§PUB_H3§§" {
			pendingHeading = "### "
			continue
		}

		if pendingHeading != "" {
			if line == "" || line == "**" || line == "*" {
				continue
			}

			titleText := cleanHeadingTitle(line)
			if titleText != "" {
				out = append(out, pendingHeading+titleText)
				pendingHeading = ""
			}
			continue
		}

		// Riga ordinaria di testo
		// Salta righe di soli asterischi orfani o formattazione rotta
		if line == "**" || line == "*" {
			continue
		}

		out = append(out, lines[i])
	}

	res := strings.Join(out, "\n")
	res = reMultiNL.ReplaceAllString(res, "\n\n")
	return strings.TrimSpace(res)
}

func cleanHeadingTitle(s string) string {
	s = strings.TrimSpace(s)
	// I titoli Markdown (## e ###) sono già stilizzati in grassetto dalla tipografia:
	// rimuovi i doppi asterischi per evitare artefatti visivi
	s = strings.ReplaceAll(s, "**", "")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "*")
	s = strings.TrimSuffix(s, "*")
	return strings.TrimSpace(s)
}
