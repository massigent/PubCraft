package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Guardia geometrica della stampa.
//
// Scopo: accorgersi quando il motore PDF non ha impaginato il documento con il
// formato richiesto (trim, più l'abbondanza). Il caso concreto è il fallback
// WebKit (`pubcraft-html2pdf`), che produce uno *snapshot* a pagina unica
// 1200x1600 pt ignorando `@page size`: senza questa guardia quello snapshot
// verrebbe pubblicato come PDF di stampa valido e i numeri dell'indice
// verrebbero calcolati su una paginazione che non esiste.
//
// Limite dichiarato: è un controllo puramente geometrico. Uno snapshot che
// avesse comunque le dimensioni corrette non è distinguibile qui, e non viene
// dichiarato rilevabile. Questo controllo non sostituisce il validatore
// strutturale (`validatePDFStructure`) e non modifica il renderer.

// printPageSizeTolerancePt è la tolleranza per dimensione, in punti tipografici.
// I renderer arrotondano al punto o al mezzo punto (misurato: 621.12 pt per un
// atteso di 621 pt), quindi 1 pt assorbe l'arrotondamento. La differenza che
// distingue un formato sbagliato è di ordini di grandezza superiore: "6x9" =
// 432x648 pt contro lo snapshot WebKit 1200x1600 pt, o Letter 612x792 pt.
const printPageSizeTolerancePt = 1.0

const pointsPerInch = 72.0

// pdfPageSize è il formato di una singola pagina, in punti.
type pdfPageSize struct {
	Page   int
	Width  float64
	Height float64
	Known  bool
}

// printGeometry è il formato atteso per la pagina stampata.
type printGeometry struct {
	WidthPt  float64
	HeightPt float64
	Source   string
}

var mediaBoxRe = regexp.MustCompile(`/MediaBox\s*\[\s*(-?[0-9.]+)\s+(-?[0-9.]+)\s+(-?[0-9.]+)\s+(-?[0-9.]+)\s*\]`)
var mediaBoxRefRe = regexp.MustCompile(`/MediaBox\s+(\d+)\s+\d+\s+R`)
var pageCSSSizeRe = regexp.MustCompile(`size:\s*([0-9.]+)in\s+([0-9.]+)in`)
var trimSizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)x([0-9]+(?:\.[0-9]+)?)$`)

// pdfPageSizes elenca i formati di tutte le pagine del documento, risolvendo
// l'ereditarietà di /MediaBox lungo l'albero /Pages (MediaBox può stare su un
// nodo antenato). Le pagine senza alcun MediaBox risolvibile sono marcate
// Known=false.
func pdfPageSizes(data []byte) ([]pdfPageSize, error) {
	p, err := newPDFParserForData(data)
	if err != nil {
		return nil, err
	}
	rootObjNum, err := p.extractRef(p.trailerDict, "/Root")
	if err != nil {
		return nil, fmt.Errorf("trailer privo di /Root valido: %w", err)
	}
	catalog, err := p.resolveDict(rootObjNum)
	if err != nil {
		return nil, fmt.Errorf("catalogo illeggibile: %w", err)
	}
	pagesObjNum, err := p.extractRef(catalog, "/Pages")
	if err != nil {
		return nil, fmt.Errorf("catalogo privo di /Pages: %w", err)
	}
	var out []pdfPageSize
	seen := map[int]bool{}
	if err := p.walkPageSizes(pagesObjNum, nil, seen, 0, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nessuna pagina trovata nel PDF")
	}
	return out, nil
}

type pdfBox struct{ w, h float64 }

func (p *PDFParser) walkPageSizes(obj int, inherited *pdfBox, seen map[int]bool, depth int, out *[]pdfPageSize) error {
	if depth > 128 || len(seen) >= maxPDFObjects || seen[obj] {
		return fmt.Errorf("albero /Pages ciclico, duplicato o troppo profondo")
	}
	seen[obj] = true
	dict, err := p.resolveDict(obj)
	if err != nil {
		return err
	}
	box := inherited
	if b, ok := p.boxFromDict(dict); ok {
		box = b
	}
	kind := regexp.MustCompile(`/Type\s*/(Page|Pages)\b`).FindStringSubmatch(dict)
	if len(kind) < 2 {
		return fmt.Errorf("nodo dell'albero pagine senza /Type valido")
	}
	if kind[1] == "Page" {
		size := pdfPageSize{Page: len(*out) + 1}
		if box != nil {
			size.Width, size.Height, size.Known = box.w, box.h, true
		}
		*out = append(*out, size)
		return nil
	}
	kids := regexp.MustCompile(`(?s)/Kids\s*\[(.*?)\]`).FindStringSubmatch(dict)
	if len(kids) < 2 {
		return fmt.Errorf("nodo /Pages senza /Kids")
	}
	for _, ref := range regexp.MustCompile(`(\d+)\s+\d+\s+R`).FindAllStringSubmatch(kids[1], -1) {
		id, cerr := strconv.Atoi(ref[1])
		if cerr != nil {
			return cerr
		}
		if cerr := p.walkPageSizes(id, box, seen, depth+1, out); cerr != nil {
			return cerr
		}
	}
	return nil
}

// boxFromDict legge /MediaBox dal dizionario, anche quando è un riferimento
// indiretto a un oggetto array.
func (p *PDFParser) boxFromDict(dict string) (*pdfBox, bool) {
	if m := mediaBoxRe.FindStringSubmatch(dict); len(m) == 5 {
		return boxFromNumbers(m[1], m[2], m[3], m[4])
	}
	if m := mediaBoxRefRe.FindStringSubmatch(dict); len(m) == 2 {
		num, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, false
		}
		arr, err := p.resolveDict(num)
		if err != nil {
			return nil, false
		}
		if a := mediaBoxRe.FindStringSubmatch("/MediaBox " + arr); len(a) == 5 {
			return boxFromNumbers(a[1], a[2], a[3], a[4])
		}
		if a := regexp.MustCompile(`\[\s*(-?[0-9.]+)\s+(-?[0-9.]+)\s+(-?[0-9.]+)\s+(-?[0-9.]+)\s*\]`).FindStringSubmatch(arr); len(a) == 5 {
			return boxFromNumbers(a[1], a[2], a[3], a[4])
		}
	}
	return nil, false
}

func boxFromNumbers(x1, y1, x2, y2 string) (*pdfBox, bool) {
	v := make([]float64, 4)
	for i, s := range []string{x1, y1, x2, y2} {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, false
		}
		v[i] = f
	}
	w, h := math.Abs(v[2]-v[0]), math.Abs(v[3]-v[1])
	if w <= 0 || h <= 0 {
		return nil, false
	}
	return &pdfBox{w: w, h: h}, true
}

// expectedPrintGeometry ricava il formato atteso dal CSS che PubCraft ha
// generato (`@page { size: Win Hin; }`): è la stessa dichiarazione che il
// renderer deve rispettare e include già l'abbondanza. Il calcolo da
// trim+bleed resta come controllo di coerenza e come fallback.
func expectedPrintGeometry(htmlPath, trim string, bleed bool) (printGeometry, error) {
	fromTrim, err := expectedGeometryFromTrim(trim, bleed)
	if err != nil {
		return printGeometry{}, err
	}
	data, rerr := os.ReadFile(htmlPath)
	if rerr != nil {
		return fromTrim, nil
	}
	if m := pageCSSSizeRe.FindStringSubmatch(string(data)); len(m) == 3 {
		w, e1 := strconv.ParseFloat(m[1], 64)
		h, e2 := strconv.ParseFloat(m[2], 64)
		if e1 == nil && e2 == nil && w > 0 && h > 0 {
			fromCSS := printGeometry{WidthPt: w * pointsPerInch, HeightPt: h * pointsPerInch, Source: "CSS @page size"}
			// Coerenza fra CSS generato e formato richiesto: se divergono,
			// il problema è a monte del renderer e va segnalato come tale.
			if math.Abs(fromCSS.WidthPt-fromTrim.WidthPt) > printPageSizeTolerancePt ||
				math.Abs(fromCSS.HeightPt-fromTrim.HeightPt) > printPageSizeTolerancePt {
				return printGeometry{}, fmt.Errorf(
					"formato incoerente: il CSS generato dichiara %.3fx%.3f in ma trim %q con abbondanza %t richiede %.3fx%.3f in",
					w, h, trim, bleed, fromTrim.WidthPt/pointsPerInch, fromTrim.HeightPt/pointsPerInch)
			}
			return fromCSS, nil
		}
	}
	return fromTrim, nil
}

// expectedGeometryFromTrim riproduce la formula usata dal writer di stampa
// (src/pubcraft.c): l'abbondanza aggiunge 0.125 in in larghezza e 0.25 in in
// altezza al formato di trim.
func expectedGeometryFromTrim(trim string, bleed bool) (printGeometry, error) {
	m := trimSizeRe.FindStringSubmatch(strings.TrimSpace(trim))
	if len(m) != 3 {
		return printGeometry{}, fmt.Errorf("formato trim non riconosciuto: %q (atteso es. 6x9)", trim)
	}
	w, err1 := strconv.ParseFloat(m[1], 64)
	h, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return printGeometry{}, fmt.Errorf("formato trim non valido: %q", trim)
	}
	if bleed {
		w += 0.125
		h += 0.25
	}
	return printGeometry{WidthPt: w * pointsPerInch, HeightPt: h * pointsPerInch, Source: "trim+bleed"}, nil
}

// checkPrintPageGeometry verifica che ogni pagina del PDF abbia il formato
// atteso entro printPageSizeTolerancePt. Una sola pagina corretta è valida
// (un libro corto non è un errore). Se il motore ha prodotto dimensioni
// incompatibili restituisce un errore azionabile con motore, atteso ed
// effettivo.
func checkPrintPageGeometry(pdfPath, htmlPath, engine, trim string, bleed bool) error {
	expected, err := expectedPrintGeometry(htmlPath, trim, bleed)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		return fmt.Errorf("lettura PDF per la verifica geometrica fallita: %w", err)
	}
	sizes, err := pdfPageSizes(data)
	if err != nil {
		return fmt.Errorf("verifica geometrica: impossibile leggere le dimensioni delle pagine: %w", err)
	}
	var bad []string
	unknown := 0
	for _, s := range sizes {
		if !s.Known {
			unknown++
			continue
		}
		if math.Abs(s.Width-expected.WidthPt) <= printPageSizeTolerancePt &&
			math.Abs(s.Height-expected.HeightPt) <= printPageSizeTolerancePt {
			continue
		}
		bad = append(bad, fmt.Sprintf("pagina %d: %.1fx%.1f pt (%.3fx%.3f in)",
			s.Page, s.Width, s.Height, s.Width/pointsPerInch, s.Height/pointsPerInch))
	}
	if unknown == len(sizes) {
		return fmt.Errorf("verifica geometrica: nessuna pagina dichiara /MediaBox (motore: %s)", engine)
	}
	if len(bad) > 0 {
		shown := bad
		if len(shown) > 3 {
			shown = append(shown[:3:3], fmt.Sprintf("… e altre %d pagine", len(bad)-3))
		}
		snapshotNote := ""
		if looksLikePageSnapshot(sizes, expected) {
			snapshotNote = "\n  Firma di uno snapshot non impaginato: un'unica pagina molto più grande del formato richiesto."
		}
		return fmt.Errorf(
			"formato di pagina incompatibile con la stampa (motore: %s)%s\n"+
				"  atteso: %.1fx%.1f pt (%.3fx%.3f in, da %s), tolleranza ±%.1f pt\n"+
				"  trovato: %s\n"+
				"  Un motore che ignora @page size produce uno snapshot non impaginato: "+
				"usa Chromium/Chrome, weasyprint o wkhtmltopdf (o imposta CHROMIUM_BIN). "+
				"Nessun artefatto è stato pubblicato e la cache dei numeri di pagina non è stata aggiornata.",
			engine, snapshotNote,
			expected.WidthPt, expected.HeightPt,
			expected.WidthPt/pointsPerInch, expected.HeightPt/pointsPerInch, expected.Source,
			printPageSizeTolerancePt,
			strings.Join(shown, "; "))
	}
	return nil
}

// looksLikePageSnapshot è una diagnostica di supporto (usata nei messaggi e nei
// test): vero se il documento ha una sola pagina di dimensioni molto più grandi
// del formato atteso, cioè la firma tipica di uno snapshot WebKit.
func looksLikePageSnapshot(sizes []pdfPageSize, expected printGeometry) bool {
	if len(sizes) != 1 || !sizes[0].Known {
		return false
	}
	return sizes[0].Width > expected.WidthPt*1.5 || sizes[0].Height > expected.HeightPt*1.5
}

// engineNameFromPath restituisce un nome leggibile del motore PDF.
func engineNameFromPath(p string) string {
	if p == "" {
		return "sconosciuto"
	}
	return filepath.Base(p)
}
