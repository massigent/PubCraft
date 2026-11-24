package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildPDFWithPageSizes costruisce un PDF minimo valido (xref classico) con una
// pagina per ogni formato indicato, in punti.
func buildPDFWithPageSizes(t *testing.T, sizes [][2]float64) []byte {
	t.Helper()
	if len(sizes) == 0 {
		t.Fatal("serve almeno una pagina")
	}
	var objs []string
	objs = append(objs, "")                                                       // 1: catalog
	objs = append(objs, "")                                                       // 2: pages
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>") // 3: font
	var kids []string
	for _, sz := range sizes {
		stream := "BT /F1 12 Tf 72 700 Td (pagina) Tj ET\n"
		contents := fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)
		pageObj := len(objs) + 1
		objs = append(objs, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>",
			sz[0], sz[1], pageObj+1))
		objs = append(objs, contents)
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(sizes))
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"

	var buf strings.Builder
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefOff)
	return []byte(buf.String())
}

// writePrintHTML crea il print.html minimo con la dichiarazione @page usata
// come formato atteso.
func writePrintHTML(t *testing.T, dir, cssSize string) string {
	t.Helper()
	p := filepath.Join(dir, "print.html")
	html := "<html><head><style>@page { size: " + cssSize + "; margin: 0.75in; }</style></head><body></body></html>"
	if err := os.WriteFile(p, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writePDF(t *testing.T, dir string, sizes [][2]float64) string {
	t.Helper()
	p := filepath.Join(dir, "book.pdf")
	if err := os.WriteFile(p, buildPDFWithPageSizes(t, sizes), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Una pagina sola del formato giusto è un documento valido.
func TestPrintGeometrySinglePageValid(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6in 9in")
	pdf := writePDF(t, dir, [][2]float64{{432, 648}})
	if err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", false); err != nil {
		t.Fatalf("una pagina 6x9 deve essere valida: %v", err)
	}
}

func TestPrintGeometryMultiPageValid(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6in 9in")
	pdf := writePDF(t, dir, [][2]float64{{432, 648}, {432, 648}, {432, 648}})
	if err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", false); err != nil {
		t.Fatalf("pagine 6x9 devono essere valide: %v", err)
	}
}

// Con l'abbondanza il formato atteso cresce di 0.125in in larghezza e 0.25in
// in altezza (stessa formula del writer C).
func TestPrintGeometryWithBleed(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6.125in 9.25in")
	pdf := writePDF(t, dir, [][2]float64{{441, 666}})
	if err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", true); err != nil {
		t.Fatalf("formato con abbondanza 6.125x9.25in deve essere valido: %v", err)
	}
	// la formula trim+bleed deve coincidere con il CSS generato
	g, err := expectedGeometryFromTrim("6x9", true)
	if err != nil {
		t.Fatal(err)
	}
	if g.WidthPt != 441 || g.HeightPt != 666 {
		t.Fatalf("atteso 441x666 pt con bleed, ottenuto %.1fx%.1f", g.WidthPt, g.HeightPt)
	}
}

// Arrotondamenti del renderer entro la tolleranza dichiarata non sono errori.
func TestPrintGeometryRoundingWithinTolerance(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6in 9in")
	for _, sizes := range [][][2]float64{
		{{432.4, 647.6}},
		{{431.5, 648.5}},
		{{433.0, 648.0}}, // esattamente al bordo della tolleranza
		{{432.0, 647.0}}, // idem
	} {
		pdf := writePDF(t, dir, sizes)
		if err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", false); err != nil {
			t.Fatalf("arrotondamento %v deve essere tollerato: %v", sizes, err)
		}
	}
}

// Una pagina fuori formato non sfugge se non è la prima.
func TestPrintGeometryRejectsMismatchedLaterPage(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6in 9in")
	pdf := writePDF(t, dir, [][2]float64{{432, 648}, {432, 648}, {595, 842}})
	err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", false)
	if err == nil {
		t.Fatal("una pagina A4 in mezzo a pagine 6x9 deve essere rifiutata")
	}
	if !strings.Contains(err.Error(), "pagina 3") {
		t.Errorf("l'errore deve indicare la pagina 3: %v", err)
	}
	if !strings.Contains(err.Error(), "595.0x842.0 pt") {
		t.Errorf("l'errore deve riportare le misure effettive: %v", err)
	}
	if !strings.Contains(err.Error(), "432.0x648.0 pt") {
		t.Errorf("l'errore deve riportare le misure attese: %v", err)
	}
	if !strings.Contains(err.Error(), "chromium") {
		t.Errorf("l'errore deve indicare il motore: %v", err)
	}
}

// Lo snapshot WebKit (una pagina 1200x1600 pt) deve essere rifiutato con un
// messaggio azionabile, e il limite del controllo va dichiarato esplicitamente.
func TestPrintGeometryRejectsWebKitSnapshot(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "6in 9in")
	pdf := writePDF(t, dir, [][2]float64{{1200, 1600}})
	err := checkPrintPageGeometry(pdf, html, "pubcraft-html2pdf", "6x9", false)
	if err == nil {
		t.Fatal("lo snapshot 1200x1600 pt deve essere rifiutato")
	}
	msg := err.Error()
	for _, want := range []string{"formato di pagina incompatibile", "pubcraft-html2pdf", "1200.0x1600.0 pt", "atteso", "snapshot"} {
		if !strings.Contains(msg, want) {
			t.Errorf("messaggio d'errore senza %q:\n%s", want, msg)
		}
	}
	// con dimensioni corrette la geometria non può distinguere uno snapshot:
	// il controllo non deve dichiararlo rilevabile.
	ok := writePDF(t, dir, [][2]float64{{432, 648}})
	if err := checkPrintPageGeometry(ok, html, "pubcraft-html2pdf", "6x9", false); err != nil {
		t.Fatalf("una pagina di formato corretto non è un errore geometrico: %v", err)
	}
}

// Se il CSS generato e il formato richiesto divergono, l'errore riguarda la
// configurazione, non il renderer.
func TestPrintGeometryDetectsConfigDrift(t *testing.T) {
	dir := t.TempDir()
	html := writePrintHTML(t, dir, "8.5in 11in")
	pdf := writePDF(t, dir, [][2]float64{{612, 792}})
	err := checkPrintPageGeometry(pdf, html, "chromium", "6x9", false)
	if err == nil {
		t.Fatal("CSS 8.5x11in con trim 6x9 deve essere un errore di configurazione")
	}
	if !strings.Contains(err.Error(), "formato incoerente") {
		t.Errorf("messaggio inatteso: %v", err)
	}
}

func TestExpectedGeometryFromTrim(t *testing.T) {
	cases := []struct {
		trim    string
		bleed   bool
		wantW   float64
		wantH   float64
		wantErr bool
	}{
		{"6x9", false, 432, 648, false},
		{"5x8", false, 360, 576, false},
		{"6.14x9.21", false, 442.08, 663.12, false},
		{"6x9", true, 441, 666, false},
		{"", false, 0, 0, true},
		{"sei per nove", false, 0, 0, true},
	}
	for _, c := range cases {
		g, err := expectedGeometryFromTrim(c.trim, c.bleed)
		if c.wantErr {
			if err == nil {
				t.Errorf("trim %q doveva essere rifiutato", c.trim)
			}
			continue
		}
		if err != nil {
			t.Errorf("trim %q: %v", c.trim, err)
			continue
		}
		if math.Abs(g.WidthPt-c.wantW) > 1e-6 || math.Abs(g.HeightPt-c.wantH) > 1e-6 {
			t.Errorf("trim %q bleed=%t: atteso %.2fx%.2f pt, ottenuto %.2fx%.2f",
				c.trim, c.bleed, c.wantW, c.wantH, g.WidthPt, g.HeightPt)
		}
	}
}

// E2E (opzionale, PUBCRAFT_E2E_PDF=1): con il renderer WebKit reale la stampa
// deve fallire in modo esplicito, senza pubblicare il PDF e senza toccare né la
// cache dei numeri di pagina né un artefatto valido precedente.
func TestPrintGeometryRejectsWebKitSnapshotE2E(t *testing.T) {
	if os.Getenv("PUBCRAFT_E2E_PDF") != "1" {
		t.Skip("e2e disattivato (impostare PUBCRAFT_E2E_PDF=1)")
	}
	// pubcraft-html2pdf è un helper WebKit nativo macOS (src/pubcraft-html2pdf.m):
	// su altre piattaforme il binario non è eseguibile e il caso non è
	// applicabile. Lo skip dichiara il limite di piattaforma, non aggira la
	// guardia di geometria (coperta dai test non-E2E su ogni piattaforma).
	if runtime.GOOS != "darwin" {
		t.Skip("helper WebKit pubcraft-html2pdf disponibile solo su macOS (" + runtime.GOOS + ")")
	}
	webkit, err := filepath.Abs(filepath.Join("..", "..", "build", "pubcraft-html2pdf"))
	if err != nil || !fileExists(webkit) {
		t.Skip("build/pubcraft-html2pdf non disponibile")
	}
	if toolPath == "" {
		p, _ := filepath.Abs(filepath.Join("..", "..", "build", "pubcraft"))
		toolPath = p
	}
	t.Setenv("CHROMIUM_BIN", webkit)

	vDir := t.TempDir()
	outDir := filepath.Join(vDir, "out-stampa")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(vDir, "book.yaml"),
		[]byte("title: Guardia\nauthor: A\nlanguage: it\nchapters:\n- file: c1.md\n  title: Capitolo\n"), 0o600)
	os.WriteFile(filepath.Join(vDir, "c1.md"), []byte("Testo del capitolo.\n"), 0o600)
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// artefatto valido precedente + cache dei numeri di pagina esistenti
	finalPdf := filepath.Join(outDir, printName(b)+".pdf")
	prevPDF := buildPDFWithPageSizes(t, [][2]float64{{432, 648}})
	if err := os.WriteFile(finalPdf, prevPDF, 0o600); err != nil {
		t.Fatal(err)
	}
	printJSON := filepath.Join(vDir, ".pubcraft-print.json")
	prevConfig := []byte(`{"trim":"6x9","pages":4,"bleed":false,"outdir":"` + outDir + `","pagemap":{"ch1":1},"pagemap_stamp":"vecchio"}`)
	if err := os.WriteFile(printJSON, prevConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	c := printConfig{Trim: "6x9", Pages: 200, OutDir: outDir}
	_, _, _, err = buildPrint(vDir, b, c)
	if err == nil {
		t.Fatal("lo snapshot WebKit doveva essere rifiutato")
	}
	if !strings.Contains(err.Error(), "formato di pagina incompatibile") {
		t.Fatalf("errore inatteso: %v", err)
	}
	// l'artefatto valido precedente è intatto
	gotPDF, rerr := os.ReadFile(finalPdf)
	if rerr != nil || string(gotPDF) != string(prevPDF) {
		t.Fatalf("il PDF valido precedente è stato modificato")
	}
	// nessuna nuova cache della mappa pagine
	gotCfg, cerr := os.ReadFile(printJSON)
	if cerr != nil || string(gotCfg) != string(prevConfig) {
		t.Fatalf("la cache dei numeri di pagina è stata modificata: %s", gotCfg)
	}
	// nessun file candidato residuo
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".candidate.") {
			t.Errorf("file candidato residuo: %s", e.Name())
		}
	}
}

// E2E (opzionale): con il motore Chromium/Brave reale la stampa con abbondanza
// passa la guardia e pubblica il PDF al percorso definitivo.
func TestPrintGeometryAcceptsChromiumWithBleedE2E(t *testing.T) {
	if os.Getenv("PUBCRAFT_E2E_PDF") != "1" {
		t.Skip("e2e disattivato (impostare PUBCRAFT_E2E_PDF=1)")
	}
	browser := findPDFBrowser()
	if browser == "" {
		t.Skip("nessun motore PDF installato")
	}
	if strings.Contains(filepath.Base(browser), "html2pdf") {
		t.Skip("motore WebKit: caso coperto da TestPrintGeometryRejectsWebKitSnapshotE2E")
	}
	if toolPath == "" {
		p, _ := filepath.Abs(filepath.Join("..", "..", "build", "pubcraft"))
		toolPath = p
	}
	vDir := t.TempDir()
	outDir := filepath.Join(vDir, "out")
	os.WriteFile(filepath.Join(vDir, "book.yaml"),
		[]byte("title: Bleed E2E\nauthor: A\nlanguage: it\ntoc:\n  pagenumbers: off\nchapters:\n- file: c1.md\n  title: Capitolo\n"), 0o600)
	os.WriteFile(filepath.Join(vDir, "c1.md"), []byte("Testo.\n\n## Sezione\n"), 0o600)
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, pdf, _, pages, _, err := renderPrintCore(vDir, b, "5x8", 200, true, outDir, browser, nil, true)
	if err != nil {
		t.Fatalf("la stampa 5x8 con bleed deve passare la guardia: %v", err)
	}
	if pages < 1 {
		t.Fatalf("pagine non valide: %d", pages)
	}
	data, err := os.ReadFile(pdf)
	if err != nil {
		t.Fatalf("PDF non pubblicato al percorso definitivo: %v", err)
	}
	sizes, err := pdfPageSizes(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sizes {
		if math.Abs(s.Width-369) > printPageSizeTolerancePt || math.Abs(s.Height-594) > printPageSizeTolerancePt {
			t.Errorf("pagina %d: %.2fx%.2f pt, atteso 369x594 pt (5x8 + bleed)", s.Page, s.Width, s.Height)
		}
	}
}

// La pubblicazione finale sposta il candidato nel percorso definitivo.
func TestPublishPrintPDFMovesCandidate(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "libro.pdf.candidate.pdf")
	if err := os.WriteFile(candidate, []byte("%PDF-1.4\ncorpo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "libro.pdf")
	if err := publishPrintPDF(candidate, final); err != nil {
		t.Fatalf("pubblicazione fallita: %v", err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("il PDF definitivo non esiste: %v", err)
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Errorf("il candidato è rimasto dopo la pubblicazione (err=%v)", err)
	}
}

// Se la pubblicazione finale fallisce, l'errore è restituito e il candidato
// viene rimosso: nessun temporaneo resta nella cartella di esportazione.
func TestPublishPrintPDFCleansCandidateOnFailure(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "libro.pdf.candidate.pdf")
	if err := os.WriteFile(candidate, []byte("%PDF-1.4\ncorpo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Il percorso definitivo è una directory: os.Rename non può sovrascriverla.
	final := filepath.Join(dir, "libro.pdf")
	if err := os.MkdirAll(final, 0o700); err != nil {
		t.Fatal(err)
	}
	err := publishPrintPDF(candidate, final)
	if err == nil {
		t.Fatal("la pubblicazione su una directory doveva fallire")
	}
	if !strings.Contains(err.Error(), "pubblicazione del PDF fallita") {
		t.Errorf("messaggio inatteso: %v", err)
	}
	if _, statErr := os.Stat(candidate); !os.IsNotExist(statErr) {
		t.Errorf("il candidato non è stato rimosso dopo il fallimento (err=%v)", statErr)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".candidate.") {
			t.Errorf("temporaneo residuo: %s", e.Name())
		}
	}
}

// writeFakeGeometryEngine crea un motore PDF fittizio (da indicare in
// CHROMIUM_BIN) che copia un PDF precompilato: strutturalmente valido ma con
// pagine di formato volutamente incompatibile con la stampa richiesta.
// Serve a esercitare la pipeline reale (buildPrint) senza renderer esterni; il
// caso WebKit reale resta coperto, invariato, da
// TestPrintGeometryRejectsWebKitSnapshotE2E.
func writeFakeGeometryEngine(t *testing.T, dir string, pdf []byte) string {
	t.Helper()
	payload := filepath.Join(dir, "wrong-geometry.pdf")
	if err := os.WriteFile(payload, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-engine")
	body := `#!/bin/sh
set -eu
out=""
for a in "$@"; do
  case "$a" in
    --print-to-pdf=*) out=${a#--print-to-pdf=} ;;
  esac
done
[ -n "$out" ] || { echo "fake-engine: --print-to-pdf mancante" >&2; exit 2; }
cp "$FAKE_ENGINE_PDF" "$out"
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ENGINE_PDF", payload)
	return script
}

// writeFakePrintCLI crea la CLI fittizia usata da buildPrint: produce solo
// print.html con la dichiarazione @page coerente con il formato richiesto
// (6x9 = 432x648 pt). Il PDF lo scrive il motore fittizio.
func writeFakePrintCLI(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-pubcraft")
	body := `#!/bin/sh
set -eu
outdir=$3
mkdir -p "$outdir"
printf '<html><head><style>@page { size: 6in 9in; margin: 0.75in; }</style></head><body><p>Testo del capitolo.</p></body></html>' > "$outdir/print.html"
echo "HTML di stampa in $outdir/print.html"
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

// Un rifiuto della pipeline per geometria incompatibile non deve toccare gli
// artefatti precedenti: PDF definitivo e cache .pubcraft-print.json restano
// byte per byte identici e non resta alcun .candidate. Esercita buildPrint
// (convergenza gutter → guardia geometrica → pubblicazione), non il solo
// validatore né publishPrintPDF. Deterministico e senza renderer reale.
func TestBuildPrintPreservesPreviousArtifactsOnGeometryRejection(t *testing.T) {
	dir := t.TempDir()
	vDir := filepath.Join(dir, "vault")
	outDir := filepath.Join(vDir, "out-stampa")
	for _, d := range []string{vDir, outDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vDir, "book.yaml"),
		[]byte("title: Preservazione\nauthor: A\nlanguage: it\nchapters:\n- file: c1.md\n  title: Capitolo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vDir, "c1.md"), []byte("Testo del capitolo.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Artefatti precedenti validi: PDF definitivo 6x9 e cache dell'indice.
	finalPdf := filepath.Join(outDir, printName(b)+".pdf")
	prevPDF := buildPDFWithPageSizes(t, [][2]float64{{432, 648}})
	if err := os.WriteFile(finalPdf, prevPDF, 0o600); err != nil {
		t.Fatal(err)
	}
	printJSON := filepath.Join(vDir, ".pubcraft-print.json")
	prevConfig := []byte(`{"trim":"6x9","pages":4,"bleed":false,"outdir":"` + outDir + `","pagemap":{"ch1":1},"pagemap_stamp":"vecchio"}`)
	if err := os.WriteFile(printJSON, prevConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	// Motore che produce uno snapshot 1200x1600 pt (valido ma incompatibile):
	// la guardia geometrica deve rifiutarlo.
	engine := writeFakeGeometryEngine(t, dir, buildPDFWithPageSizes(t, [][2]float64{{1200, 1600}}))
	t.Setenv("CHROMIUM_BIN", engine)
	prevTool := toolPath
	toolPath = writeFakePrintCLI(t, dir)
	t.Cleanup(func() { toolPath = prevTool })

	c := printConfig{Trim: "6x9", Pages: 200, OutDir: outDir}
	if _, _, _, err = buildPrint(vDir, b, c); err == nil {
		t.Fatal("buildPrint doveva essere rifiutata dalla guardia geometrica")
	}
	for _, want := range []string{
		"formato di pagina incompatibile",
		"fake-engine",
		"atteso: 432.0x648.0 pt",
		"1200.0x1600.0 pt",
		"snapshot",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("motivo del rifiuto senza %q:\n%s", want, err.Error())
		}
	}

	// Il PDF valido precedente è intatto, byte per byte.
	gotPDF, rerr := os.ReadFile(finalPdf)
	if rerr != nil {
		t.Fatalf("PDF precedente non più leggibile: %v", rerr)
	}
	if string(gotPDF) != string(prevPDF) {
		t.Fatal("il PDF valido precedente è stato modificato dal rifiuto")
	}
	// La cache dei numeri di pagina è intatta, byte per byte.
	gotCfg, cerr := os.ReadFile(printJSON)
	if cerr != nil {
		t.Fatalf("cache .pubcraft-print.json non più leggibile: %v", cerr)
	}
	if string(gotCfg) != string(prevConfig) {
		t.Fatalf("la cache dei numeri di pagina è stata modificata: %s", gotCfg)
	}
	// Nessun candidato residuo nella cartella di esportazione.
	entries, derr := os.ReadDir(outDir)
	if derr != nil {
		t.Fatal(derr)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".candidate.") {
			t.Errorf("file candidato residuo: %s", e.Name())
		}
	}
}
