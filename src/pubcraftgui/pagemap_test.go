package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buildSyntheticPDF genera un PDF minimo (xref classico, font Type1 senza
// ToUnicode) con un marcatore PCMAP per pagina. Serve a testare l'estrazione
// senza dipendere da motori di rendering esterni.
// Layout oggetti: 1=catalog, 2=pages, 3=font, poi per ogni marker: page e
// contents consecutivi (4/5, 6/7, ...).
func buildSyntheticPDF(t *testing.T, markers []string) []byte {
	t.Helper()
	var objs []string
	objs = append(objs, "") // posto per il catalog
	objs = append(objs, "") // posto per le pages
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var kids []string
	for i, m := range markers {
		stream := "BT /F1 12 Tf 72 700 Td (" + m + ") Tj ET\n"
		contents := fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)
		pageObj := len(objs) + 1
		objs = append(objs, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>", pageObj+1))
		objs = append(objs, contents)
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
		_ = i
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(markers))
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

func TestExtractTocPageMapSynthetic(t *testing.T) {
	data := buildSyntheticPDF(t, []string{"PCMAP-ch1-", "PCMAP-ch2-", "PCMAP-ch3-s1-"})
	m, err := extractTocPageMapFromData(data)
	if err != nil {
		t.Fatalf("estrazione fallita: %v", err)
	}
	want := tocPageMap{"ch1": 1, "ch2": 2, "ch3-s1": 3}
	if len(m) != len(want) {
		t.Fatalf("mappa attesa %v, ottenuta %v", want, m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("voce %s: attesa pagina %d, ottenuta %d", k, v, m[k])
		}
	}
}

func TestExtractTocPageMapEmptyWhenNoMarkers(t *testing.T) {
	data := buildSyntheticPDF(t, []string{"ciao pagina uno", "ciao pagina due"})
	m, err := extractTocPageMapFromData(data)
	if err != nil {
		t.Fatalf("estrazione fallita: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("attesa mappa vuota, ottenuta %v", m)
	}
}

func TestParseToUnicodeCMapBFCharBFRange(t *testing.T) {
	cmap := []byte(
		"/CIDInit /ProcSet findresource begin\n" +
			"2 beginbfchar\n" +
			"<0041> <0050>\n" +
			"<0042> <0043 004D>\n" +
			"endbfchar\n" +
			"beginbfrange\n" +
			"<0030> <0032> <0061>\n" +
			"endbfrange\n" +
			"end")
	m := parseToUnicodeCMap(cmap)
	if m["0041"] != "P" {
		t.Errorf("bfchar semplice: atteso P, ottenuto %q", m["0041"])
	}
	if m["0042"] != "CM" {
		t.Errorf("bfchar multi-rune: atteso CM, ottenuto %q", m["0042"])
	}
	if m["0030"] != "a" || m["0031"] != "b" || m["0032"] != "c" {
		t.Errorf("bfrange: atteso a/b/c, ottenuto %q %q %q", m["0030"], m["0031"], m["0032"])
	}
}

func TestMapsEqual(t *testing.T) {
	a := tocPageMap{"ch1": 1, "ch2": 2}
	b := tocPageMap{"ch1": 1, "ch2": 2}
	if !mapsEqual(a, b) {
		t.Fatal("mappe identiche considerate diverse")
	}
	b["ch2"] = 3
	if mapsEqual(a, b) {
		t.Fatal("mappe diverse considerate identiche")
	}
	c := tocPageMap{"ch1": 1}
	if mapsEqual(a, c) {
		t.Fatal("mappe con lunghezza diversa considerate identiche")
	}
	if !mapsEqual(nil, tocPageMap{}) {
		t.Fatal("nil e mappa vuota sono equivalenti (nessun numero ancora)")
	}
}

func TestCollapseWhitespace(t *testing.T) {
	if got := collapseWhitespace("PCMAP-\nch1"); got != "PCMAP-ch1" {
		t.Errorf("atteso PCMAP-ch1, ottenuto %q", got)
	}
}

// --- E2E opzionale: pipeline completa con motore browser reale ---
// Attivabile con PUBCRAFT_E2E_PDF=1 su macchine con un motore PDF installato.
func TestRenderPrintCoreTocNumbersE2E(t *testing.T) {
	if os.Getenv("PUBCRAFT_E2E_PDF") != "1" {
		t.Skip("e2e disattivato (impostare PUBCRAFT_E2E_PDF=1)")
	}
	if toolPath == "" {
		p, err := filepath.Abs(filepath.Join("..", "..", "build", "pubcraft"))
		if err != nil {
			t.Skip("pubcraft non trovato")
		}
		if _, err := os.Stat(p); err != nil {
			t.Skip("pubcraft non trovato")
		}
		toolPath = p
	}
	if findPDFBrowser() == "" {
		t.Skip("nessun motore PDF installato")
	}
	vDir := t.TempDir()
	outDir := filepath.Join(vDir, "out-stampa")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(vDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("book.yaml", "title: Libro E2E\nauthor: T. Test\nlanguage: it\nchapters:\n- file: cap1.md\n  title: Capitolo uno\n- file: cap2.md\n  title: Capitolo due\n")
	write("cap1.md", "Testo del primo capitolo.\n\n## Sezione\n\nTesto sezione.\n")
	write("cap2.md", "Testo del secondo capitolo.\n")
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := printConfig{Trim: "6x9", Pages: 200, OutDir: outDir}
	// build "di release": l'identità dirty non sarebbe affidabile per il riuso
	// (il comportamento fail-closed è coperto da un test dedicato).
	withCleanBuild(t, "")
	engine := findPDFBrowser()
	html, pdf, out, finalPages, pageMap, err := renderPrintCore(vDir, b, c.Trim, c.Pages, false, outDir, engine, nil, true)
	if err != nil {
		t.Fatalf("renderPrintCore: %v", err)
	}
	defer func() {
		_ = os.Remove(pdf)
		_ = os.Remove(html)
	}()
	// la mappa estratta dal PDF di esplorazione deve contenere i marcatori
	// (il PDF finale non li contiene piu', la CLI li omette con la mappa nota)
	if len(pageMap) == 0 {
		t.Fatal("mappa pagine vuota: il motore probabilmente scarta il testo bianco o l'estrazione fallisce sui PDF reali")
	}
	for _, want := range []string{"ch1", "ch2"} {
		if pageMap[want] <= 0 {
			t.Errorf("voce %s assente dalla mappa: %v", want, pageMap)
		}
	}
	// il PDF finale deve avere i numeri nell'indice e nessun marcatore visibile
	hraw2, _ := os.ReadFile(html)
	if !regexp.MustCompile(`<div class="toc-p">\d+</div>`).Match(hraw2) {
		t.Errorf("l'HTML finale non contiene numeri di pagina dell'indice")
	}
	if strings.Contains(string(hraw2), "PCMAP-") {
		t.Errorf("l'HTML finale contiene ancora marcatori")
	}
	// la passata fredda costa la sonda + la passata numerata, ma la seconda
	// riparte dal conteggio misurato: al massimo 3 render CLI, mai 4
	if n := strings.Count(out, "HTML di stampa in"); n > 3 {
		t.Errorf("passata fredda troppo costosa: %d render CLI (attesi <=3)", n)
	}

	// cache valida: una sola renderizzazione, numeri gia' corretti
	html2, _, out2, _, pageMap2, err := renderPrintCore(vDir, b, c.Trim, finalPages, false, outDir, engine, pageMap, true)
	if err != nil {
		t.Fatalf("renderPrintCore (cache): %v", err)
	}
	if n := strings.Count(out2, "HTML di stampa in"); n != 1 {
		t.Errorf("passata calda: %d render CLI, atteso 1", n)
	}
	if len(pageMap2) != len(pageMap) {
		t.Errorf("mappa in cache non riusata: %v vs %v", pageMap2, pageMap)
	}
	hraw3, _ := os.ReadFile(html2)
	if !regexp.MustCompile(`<div class="toc-p">\d+</div>`).Match(hraw3) {
		t.Errorf("la passata calda non riporta i numeri nell'indice")
	}

	// allowProbe=false senza cache (anteprima): una sola renderizzazione e
	// indice senza numeri, senza marcatori nascosti
	html4, _, out4, _, map4, err := renderPrintCore(vDir, b, c.Trim, finalPages, false, outDir, engine, nil, false)
	if err != nil {
		t.Fatalf("renderPrintCore (anteprima): %v", err)
	}
	if n := strings.Count(out4, "HTML di stampa in"); n != 1 {
		t.Errorf("anteprima: %d render CLI, atteso 1", n)
	}
	if map4 != nil {
		t.Errorf("anteprima senza cache non deve produrre una mappa: %v", map4)
	}
	hraw4, _ := os.ReadFile(html4)
	if strings.Contains(string(hraw4), "PCMAP-") || strings.Contains(string(hraw4), "class=\"toc-p\"") {
		t.Errorf("anteprima senza cache: indice con marcatori o colonna numeri")
	}
}

// ---------------------------------------------------------------------------
// Regressione deterministica: allowProbe=false + mappa in cache + cambio banda
// gutter. L'anteprima non deve mai avviare la sonda PCMAP né la passata
// numerata: le uniche renderizzazioni ammesse sono quelle della convergenza
// del gutter, e la mappa in cache non va restituita come valida quando la banda
// è cambiata.
//
// Il test non dipende da motori o CLI reali: toolPath e CHROMIUM_BIN puntano a
// due script che (a) registrano ogni invocazione con i suoi argomenti e (b)
// producono un PDF con un numero di pagine fisso, così il cambio di banda del
// gutter è deterministico (100 pagine richieste → 160 prodotte: 0.375 → 0.500
// in di gutter, vedi kdpGutter).
// ---------------------------------------------------------------------------

// buildFakeRenderPDF genera un PDF minimo con `pages` pagine di formato w×h.
// Se mapMarker non è vuoto lo scrive come testo della prima pagina, così
// extractTocPageMap lo trova (solo la passata di sonda lo contiene).
func buildFakeRenderPDF(t *testing.T, pages int, w, h float64, mapMarker string) []byte {
	t.Helper()
	if pages < 1 {
		t.Fatal("serve almeno una pagina")
	}
	var objs []string
	objs = append(objs, "")                                                       // 1: catalog
	objs = append(objs, "")                                                       // 2: pages
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>") // 3: font
	var kids []string
	for i := 0; i < pages; i++ {
		text := fmt.Sprintf("pagina %d", i+1)
		if i == 0 && mapMarker != "" {
			text = mapMarker
		}
		stream := "BT /F1 12 Tf 72 700 Td (" + text + ") Tj ET\n"
		contents := fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)
		pageObj := len(objs) + 1
		objs = append(objs, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>",
			w, h, pageObj+1))
		objs = append(objs, contents)
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages)
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

func writeExecutableScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// printRenderCalls restituisce le righe di log delle invocazioni della CLI.
func printRenderCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(c, want) {
			n++
		}
	}
	return n
}

func TestRenderPrintCorePreviewNeverProbesTocOnGutterBandShift(t *testing.T) {
	// Struttura del banco: vault minimo, due PDF finti (sonda con marcatore,
	// definitivo senza), CLI finta che registra gli argomenti e dichiara nel
	// CSS il formato 6x9, motore finto che sceglie il PDF in base alla
	// modalità scritta dalla CLI nell'HTML.
	dir := t.TempDir()
	vDir := filepath.Join(dir, "vault")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{vDir, bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vDir, "book.yaml"),
		[]byte("title: Gutter\nauthor: A\nlanguage: it\nchapters:\n- file: c1.md\n  title: Capitolo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vDir, "c1.md"), []byte("Testo.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// 432x648 pt = 6x9 in, coerente con il CSS della CLI finta e con la guardia.
	const w, h = 432.0, 648.0
	probePdf := filepath.Join(bin, "probe.pdf")
	plainPdf := filepath.Join(bin, "plain.pdf")
	if err := os.WriteFile(probePdf, buildFakeRenderPDF(t, 160, w, h, "PCMAP-ch1-"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainPdf, buildFakeRenderPDF(t, 160, w, h, ""), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := filepath.Join(bin, "fake-pubcraft")
	writeExecutableScript(t, tool, `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$PAGEMAP_TEST_LOG"
cmd=$1; vdir=$2; outdir=$3; shift 3
pages=200; mapfile=""; numbering=1
while [ $# -gt 0 ]; do
  case "$1" in
    --trim) shift 2 ;;
    --pages) pages=$2; shift 2 ;;
    --bleed) shift ;;
    --toc-pagemap) mapfile=$2; shift 2 ;;
    --no-toc-pagenumbers) numbering=0; shift ;;
    *) shift ;;
  esac
done
mkdir -p "$outdir"
if [ -n "$mapfile" ]; then mode=numbered
elif [ "$numbering" = "1" ]; then mode=probe
else mode=plain
fi
{
  printf '<html><head><style>@page { size: 6in 9in; margin: 0.75in; }</style></head><body>'
  printf '<!--FAKE-MODE:%s-->' "$mode"
  if [ "$mode" = "numbered" ]; then printf '<div class="toc"><div class="toc-p">12</div></div>'; fi
  if [ "$mode" = "probe" ]; then printf '<span>PCMAP-ch1-</span>'; fi
  printf '</body></html>'
} > "$outdir/print.html"
echo "HTML di stampa in $outdir/print.html"
`)

	engine := filepath.Join(bin, "fake-chromium")
	writeExecutableScript(t, engine, `#!/bin/sh
set -eu
out=""; htmlurl=""
for a in "$@"; do
  case "$a" in
    --print-to-pdf=*) out=${a#--print-to-pdf=} ;;
    file://*) htmlurl=$a ;;
  esac
done
[ -n "$out" ] || { echo "fake engine: --print-to-pdf mancante" >&2; exit 2; }
[ -n "$htmlurl" ] || { echo "fake engine: URL mancante" >&2; exit 2; }
html=${htmlurl#file://}
mode=$(sed -n 's/.*<!--FAKE-MODE:\([a-z]*\)-->.*/\1/p' "$html")
case "$mode" in
  probe) src="$FAKE_PDF_DIR/probe.pdf" ;;
  numbered|plain) src="$FAKE_PDF_DIR/plain.pdf" ;;
  *) echo "fake engine: modalita sconosciuta '$mode'" >&2; exit 3 ;;
esac
cp "$src" "$out"
`)

	logPath := filepath.Join(bin, "calls.log")
	t.Setenv("PAGEMAP_TEST_LOG", logPath)
	t.Setenv("FAKE_PDF_DIR", bin)
	t.Setenv("CHROMIUM_BIN", engine)
	prevTool := toolPath
	toolPath = tool
	t.Cleanup(func() { toolPath = prevTool })
	// identità di build "di release": con albero dirty nessuna cache sarebbe
	// riutilizzabile e il test non potrebbe distinguere i due casi.
	withCleanBuild(t, "")
	resetRendererProbeCache()
	t.Cleanup(resetRendererProbeCache)

	// Mappa in cache volutamente "vecchia" (ch1:7) e 100 pagine dichiarate:
	// il motore ne produce 160, quindi la banda del gutter cambia (0.375→0.500).
	staleMap := tocPageMap{"ch1": 7}

	// --- Caso 1: anteprima (allowProbe=false) ---
	outPreview := filepath.Join(dir, "out-preview")
	htmlPath, _, out, finalPages, pageMap, err := renderPrintCore(vDir, b, "6x9", 100, false, outPreview, engine, staleMap, false)
	if err != nil {
		t.Fatalf("renderPrintCore (anteprima): %v (output: %s)", err, out)
	}
	calls := printRenderCalls(t, logPath)

	// Le passate necessarie alla convergenza del gutter restano ammesse: la
	// prima usa la cache, la seconda riconverge sulla banda nuova.
	if len(calls) != 2 {
		t.Fatalf("anteprima: %d render CLI, attesi 2 (solo convergenza gutter)\nchiamate:\n%s",
			len(calls), strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[0], "--toc-pagemap") {
		t.Errorf("prima passata: attesa la mappa in cache, trovata %q", calls[0])
	}
	if !strings.Contains(calls[1], "--no-toc-pagenumbers") {
		t.Errorf("riconvergenza: atteso indice senza numeri, trovata %q", calls[1])
	}
	// Nessuna sonda TOC: una passata senza né --toc-pagemap né
	// --no-toc-pagenumbers è la base della sonda (marcatori PCMAP).
	for _, c := range calls {
		if !strings.Contains(c, "--toc-pagemap") && !strings.Contains(c, "--no-toc-pagenumbers") {
			t.Errorf("anteprima: passata di sonda TOC non consentita: %q", c)
		}
	}
	if pageMap != nil {
		t.Errorf("anteprima: la mappa non deve essere restituita quando la banda cambia, ottenuto %v", pageMap)
	}
	if finalPages != 160 {
		t.Errorf("anteprima: attese 160 pagine finali, ottenute %d", finalPages)
	}
	// la passata di convergenza del gutter è avvenuta: non è una sonda e non è
	// una numerazione, è il ricalcolo dovuto al cambio di banda
	if !strings.Contains(out, "Ricalcolo gutter su 160 pagine") {
		t.Errorf("anteprima: manca la riconvergenza del gutter nell'output:\n%s", out)
	}
	if !strings.Contains(out, "indice senza numeri") {
		t.Errorf("anteprima: manca la diagnosi 'indice senza numeri' nell'output:\n%s", out)
	}
	hraw, rerr := os.ReadFile(htmlPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(hraw), "PCMAP-") {
		t.Error("anteprima: l'HTML finale contiene marcatori PCMAP (sonda eseguita)")
	}
	if strings.Contains(string(hraw), `class="toc-p"`) {
		t.Error("anteprima: l'HTML finale contiene numeri di pagina dell'indice")
	}

	// --- Caso 2: esportazione (allowProbe=true), stesso scenario ---
	if err := os.WriteFile(logPath, nil, 0o600); err != nil { // azzera il log
		t.Fatal(err)
	}
	outExport := filepath.Join(dir, "out-export")
	htmlExport, _, outExp, finalPagesExp, pageMapExp, err := renderPrintCore(vDir, b, "6x9", 100, false, outExport, engine, staleMap, true)
	if err != nil {
		t.Fatalf("renderPrintCore (esportazione): %v (output: %s)", err, outExp)
	}
	callsExp := printRenderCalls(t, logPath)
	// convergenza (2) + sonda (1) + passata numerata (1)
	if len(callsExp) != 4 {
		t.Fatalf("esportazione: %d render CLI, attesi 4 (2 convergenza + sonda + numerata)\nchiamate:\n%s",
			len(callsExp), strings.Join(callsExp, "\n"))
	}
	probes := 0
	probeIdx := -1
	for i, c := range callsExp {
		if !strings.Contains(c, "--toc-pagemap") && !strings.Contains(c, "--no-toc-pagenumbers") {
			probes++
			probeIdx = i
		}
	}
	if probes != 1 {
		t.Fatalf("esportazione: attesa esattamente 1 sonda TOC, trovate %d\nchiamate:\n%s",
			probes, strings.Join(callsExp, "\n"))
	}
	// le passate con la mappa si dividono in: 2 di convergenza (prima e dopo il
	// cambio banda, entrambe con la cache) prima della sonda, 1 numerata dopo.
	beforeProbe, afterProbe := 0, 0
	for i, c := range callsExp {
		if !strings.Contains(c, "--toc-pagemap") {
			continue
		}
		if i < probeIdx {
			beforeProbe++
		} else {
			afterProbe++
		}
	}
	if beforeProbe != 2 || afterProbe != 1 {
		t.Errorf("esportazione: attese 2 passate con mappa prima della sonda e 1 numerata dopo, trovate %d e %d",
			beforeProbe, afterProbe)
	}
	if !strings.Contains(callsExp[len(callsExp)-1], "--toc-pagemap") {
		t.Errorf("esportazione: l'ultima passata deve essere numerata, trovata %q", callsExp[len(callsExp)-1])
	}
	// la sonda ha misurato ch1 a pagina 1: la mappa vecchia (7) non va riusata
	if pageMapExp["ch1"] != 1 {
		t.Errorf("esportazione: attesa mappa fresca {ch1:1}, ottenuta %v", pageMapExp)
	}
	if finalPagesExp != 160 {
		t.Errorf("esportazione: attese 160 pagine finali, ottenute %d", finalPagesExp)
	}
	if !strings.Contains(outExp, "Aggiornamento numeri di pagina dell'indice") {
		t.Errorf("esportazione: manca l'aggiornamento dei numeri nell'output:\n%s", outExp)
	}
	hrawExp, rerr := os.ReadFile(htmlExport)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(hrawExp), `class="toc-p"`) {
		t.Error("esportazione: l'HTML finale deve contenere i numeri dell'indice")
	}
	if strings.Contains(string(hrawExp), "PCMAP-") {
		t.Error("esportazione: l'HTML finale non deve contenere marcatori PCMAP")
	}
}

func TestOSReadFileBounded(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := osReadFileBounded(empty, 0); err != nil || len(data) != 0 {
		t.Fatalf("file vuoto: data=%q err=%v", data, err)
	}

	exact := filepath.Join(dir, "exact")
	if err := os.WriteFile(exact, []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := osReadFileBounded(exact, 4); err != nil || string(data) != "1234" {
		t.Fatalf("dimensione esatta al limite: data=%q err=%v", data, err)
	}

	if _, err := osReadFileBounded(exact, 3); err == nil {
		t.Fatal("file oltre il limite: atteso errore")
	}

	if _, err := osReadFileBounded(filepath.Join(dir, "assente"), 4); err == nil {
		t.Fatal("file inesistente: atteso errore")
	}
}
