package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Infrastruttura di test (fixture sintetiche, nessun motore reale) ---------

// withCleanBuild simula una build di release: revisione VCS completa e non
// dirty. Senza questo, sull'albero di sviluppo (vcs.modified=true) l'identità
// non sarebbe affidabile e nessuna cache sarebbe mai riutilizzabile.
func withCleanBuild(t *testing.T, rev string) {
	t.Helper()
	if len(rev) != 40 {
		rev = strings.Repeat("a", 40)
	}
	prev := buildRevision
	buildRevision = func() (string, bool, bool) { return rev, false, true }
	t.Cleanup(func() { buildRevision = prev })
}

func withDirtyBuild(t *testing.T) {
	t.Helper()
	prev := buildRevision
	buildRevision = func() (string, bool, bool) { return strings.Repeat("a", 40), true, true }
	t.Cleanup(func() { buildRevision = prev })
}

func withPlatform(t *testing.T, id string) {
	t.Helper()
	prev := platformID
	platformID = func() string { return id }
	t.Cleanup(func() { platformID = prev })
}

// writeScript crea uno script eseguibile di test.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

// fakePrintCLI replica il comportamento osservabile della CLI C per la parte
// che serve qui: scrive print.html con @page coerente con trim/bleed e dichiara
// nella pagina la modalità (numbered/probe/plain) e il formato richiesto, così
// il motore fittizio può scegliere il PDF giusto.
const fakePrintCLIBody = `#!/bin/sh
set -eu
export LC_ALL=C
printf '%s\n' "$*" >> "$RENDER_TEST_LOG"
outdir=$3
vdir=$2
trim=6x9; bleed=0
shift 3
while [ $# -gt 0 ]; do
  case "$1" in
    --trim) trim=$2; shift 2 ;;
    --pages) shift 2 ;;
    --bleed) bleed=1; shift ;;
    --toc-pagemap) hasmap=1; shift 2 ;;
    --no-toc-pagenumbers) hasnone=1; shift ;;
    *) shift ;;
  esac
done
hasmap=${hasmap:-0}; hasnone=${hasnone:-0}
if [ "$hasmap" = 1 ]; then mode=numbered
elif [ "$hasnone" = 1 ]; then mode=plain
else mode=probe; fi
w=$(echo "$trim" | cut -dx -f1); h=$(echo "$trim" | cut -dx -f2)
if [ "$bleed" = 1 ]; then
  w=$(awk "BEGIN{printf \"%.3f\", $w+0.125}")
  h=$(awk "BEGIN{printf \"%.3f\", $h+0.25}")
fi
copy_fonts_from() {
  src=$1
  [ -d "$src" ] || return 0
  mkdir -p "$outdir/fonts"
  for f in "$src"/*; do
    [ -f "$f" ] || continue
    base=$(basename "$f")
    case "$base" in *.ttf|*.otf|*.woff|*.woff2) ;; *) continue ;; esac
    if [ -e "$outdir/fonts/$base" ]; then continue; fi
    cp "$f" "$outdir/fonts/$base"
  done
}
copy_fonts_from "$vdir/fonts"
if [ -n "${PUBCRAFT_FONTS_DIR:-}" ]; then copy_fonts_from "$PUBCRAFT_FONTS_DIR"; fi
mkdir -p "$outdir"
{
  printf '<html><head><style>@page { size: %sin %sin; margin: 0.75in; }</style></head><body>' "$w" "$h"
  printf '<!--FAKE-MODE:%s--><!--FAKE-SIZE:%sx%s-->' "$mode" "$w" "$h"
  if [ "$mode" = "numbered" ]; then printf '<div class="toc"><div class="toc-p">12</div></div>'; fi
  printf '<p>Testo del capitolo.</p></body></html>'
} > "$outdir/print.html"
echo "HTML di stampa in $outdir/print.html"
`

// fakeEngineBody: probe di versione quando non c'è --print-to-pdf, altrimenti
// copia il PDF preparato per (formato, modalità).
const fakeEngineBody = `#!/bin/sh
set -eu
printf '%s %s\n' "$0" "$*" >> "$RENDER_TEST_LOG.engine"
mode="${FAKE_ENGINE_MODE:-ok}"
out=""; htmlurl=""
for a in "$@"; do
  case "$a" in
    --print-to-pdf=*) out=${a#--print-to-pdf=} ;;
    file://*) htmlurl=$a ;;
  esac
done
if [ -z "$out" ]; then
  case "$mode" in
    fail) echo "probe non disponibile" >&2; exit 1 ;;
    slow) sleep 5; exit 0 ;;
    junk) head -c 9000 /dev/zero | tr '\0' 'x'; exit 0 ;;
  esac
  cat "$FAKE_ENGINE_VERSION_FILE"
  exit 0
fi
html=${htmlurl#file://}
pmode=$(sed -n 's/.*<!--FAKE-MODE:\([a-z]*\)-->.*/\1/p' "$html")
size=$(sed -n 's/.*<!--FAKE-SIZE:\([0-9.]*x[0-9.]*\)-->.*/\1/p' "$html")
kind=plain
if [ "$pmode" = "probe" ]; then kind=probe; fi
src="$FAKE_PDF_DIR/p-${size}-${kind}.pdf"
[ -f "$src" ] || { echo "payload mancante: $src" >&2; exit 3; }
cp "$src" "$out"
`

type printHarness struct {
	t        *testing.T
	dir      string
	vDir     string
	outDir   string
	bin      string
	tool     string
	engine   string
	version  string
	logPath  string
	book     *Book
	selector int
}

// newPrintHarness prepara vault sintetico, CLI fittizia, motore fittizio e
// payload PDF (4 pagine 432x648 = 6x9) con/senza marcatore PCMAP.
func newPrintHarness(t *testing.T) *printHarness {
	t.Helper()
	h := &printHarness{t: t, dir: t.TempDir()}
	h.vDir = filepath.Join(h.dir, "vault")
	h.outDir = filepath.Join(h.vDir, "out-stampa")
	h.bin = filepath.Join(h.dir, "bin")
	for _, d := range []string{h.vDir, h.bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(h.vDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("book.yaml", "title: Cache\nauthor: A\nlanguage: it\nchapters:\n- file: c1.md\n  title: Capitolo\n")
	write("c1.md", "Testo del capitolo.\n")
	b, err := parseBookYAML(filepath.Join(h.vDir, "book.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	h.book = b

	// Payload per i formati usati dai test. L'etichetta è quella che la CLI
	// fittizia scrive nell'HTML (pollici, come li formatta awk): 6x9, 5x8 e le
	// varianti con bleed (6.125x9.250, 5.125x8.250).
	payloads := []struct {
		label string
		w, h  float64
	}{
		{"6x9", 432, 648},
		{"5x8", 360, 576},
		{"6.125x9.250", 441, 666},
		{"5.125x8.250", 369, 594},
	}
	for _, p := range payloads {
		for _, probe := range []bool{false, true} {
			marker, kind := "", "plain"
			if probe {
				marker, kind = "PCMAP-ch1-", "probe"
			}
			name := fmt.Sprintf("p-%s-%s.pdf", p.label, kind)
			if err := os.WriteFile(filepath.Join(h.bin, name), buildFakeRenderPDF(t, 4, p.w, p.h, marker), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	h.tool = filepath.Join(h.bin, "fake-pubcraft")
	writeScript(t, h.tool, fakePrintCLIBody)
	h.engine = filepath.Join(h.bin, "fake-chrome")
	writeScript(t, h.engine, fakeEngineBody)
	h.version = filepath.Join(h.bin, "engine.version")
	if err := os.WriteFile(h.version, []byte("FakeChrome 1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.logPath = filepath.Join(h.bin, "calls.log")

	// Isolamento completo: HOME e configurazione in directory temporanee.
	t.Setenv("HOME", filepath.Join(h.dir, "home"))
	t.Setenv("PUBCRAFT_CONFIG_DIR", filepath.Join(h.dir, "config"))
	t.Setenv("RENDER_TEST_LOG", h.logPath)
	t.Setenv("FAKE_PDF_DIR", h.bin)
	t.Setenv("FAKE_ENGINE_VERSION_FILE", h.version)
	t.Setenv("FAKE_ENGINE_MODE", "ok")
	t.Setenv("PUBCRAFT_FONTS_DIR", "")

	prevTool := toolPath
	toolPath = h.tool
	t.Cleanup(func() { toolPath = prevTool })

	prevSelector := selectPDFBrowser
	selectPDFBrowser = func() string {
		h.selector++
		return h.engine
	}
	t.Cleanup(func() { selectPDFBrowser = prevSelector })

	withCleanBuild(t, "")
	withPlatform(t, "test/test@1")
	resetRendererProbeCache()
	t.Cleanup(resetRendererProbeCache)
	return h
}

// runCommand esegue un comando con timeout (usato dal test di parità con la CLI).
func runCommand(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cfg legge la cache come fa apiBuild/triggerPrintBuild (loadPrintConfig) e
// poi impone i valori del test: così le prove verificano il percorso reale.
func (h *printHarness) cfg() printConfig {
	c := loadPrintConfig(h.vDir, h.book)
	c.Trim = "6x9"
	c.Pages = 100
	c.Bleed = false
	c.OutDir = h.outDir
	return c
}

func (h *printHarness) build() (string, error) {
	return h.buildWith(h.cfg())
}

func (h *printHarness) buildWith(c printConfig) (string, error) {
	h.t.Helper()
	_, _, out, err := buildPrint(h.vDir, h.book, c)
	return out, err
}

// cliCalls restituisce le chiamate alla CLI e azzera il log.
func (h *printHarness) cliCalls() []string {
	h.t.Helper()
	return takeLog(h.t, h.logPath)
}

func (h *printHarness) engineCalls() []string {
	h.t.Helper()
	return takeLog(h.t, h.logPath+".engine")
}

func takeLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cache legge la cache persistita e restituisce i byte grezzi.
func (h *printHarness) cache() (printConfig, []byte) {
	h.t.Helper()
	path := filepath.Join(h.vDir, ".pubcraft-print.json")
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("cache non leggibile: %v", err)
	}
	var c printConfig
	if err := json.Unmarshal(data, &c); err != nil {
		h.t.Fatalf("cache non decodificabile: %v", err)
	}
	return c, data
}

func (h *printHarness) writeCache(c printConfig) {
	h.t.Helper()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.vDir, ".pubcraft-print.json"), data, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func isProbeCall(call string) bool {
	return !strings.Contains(call, "--toc-pagemap") && !strings.Contains(call, "--no-toc-pagenumbers")
}

func hasProbeCall(calls []string) bool {
	for _, c := range calls {
		if isProbeCall(c) {
			return true
		}
	}
	return false
}

// --- 1. Hit e invalidazioni -------------------------------------------------

func TestBuildPersistsPostBuildEnvStamp(t *testing.T) {
	h := newPrintHarness(t)
	// La CLI reale (e quella fittizia) copia i font in <outdir>/fonts senza
	// preservare l'mtime. L'EnvStamp persistito deve descrivere l'ambiente che
	// il Watch osserverà DOPO la build, altrimenti la prima richiesta Watch
	// dopo ogni build esplicita rigenera inutilmente il PDF.
	fontsDir := filepath.Join(h.vDir, "fonts")
	if err := os.MkdirAll(fontsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(fontsDir, "body.woff2")
	if err := os.WriteFile(src, []byte("font sintetico"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := h.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	c, _ := h.cache()
	current := computeEnvStamp(h.engine, h.tool, h.vDir, h.outDir)
	if c.EnvStamp != current {
		t.Fatalf("EnvStamp persistito non coincide con l'ambiente post-build: cache=%q corrente=%q", c.EnvStamp, current)
	}
	copied, err := os.Stat(filepath.Join(h.outDir, "fonts", "body.woff2"))
	if err != nil {
		t.Fatalf("font non copiato nella cartella di esportazione: %v", err)
	}
	if copied.ModTime().Equal(old) {
		t.Fatal("fixture non valida: la copia conserva l'mtime, il caso non è esercitato")
	}
}

func TestCacheHitOnIdenticalInputs(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatalf("prima build: %v", err)
	}
	first := h.cliCalls()
	if len(first) != 2 {
		t.Fatalf("prima build (fredda): %d render CLI, attesi 2 (sonda + numerata):\n%s", len(first), strings.Join(first, "\n"))
	}
	c, _ := h.cache()
	if c.CacheSchema != cacheSchema || c.RenderStamp == "" || c.EnvStamp == "" || len(c.PageMap) == 0 {
		t.Fatalf("cache non valida dopo la build: schema=%d stamp=%q env=%q map=%v", c.CacheSchema, c.RenderStamp, c.EnvStamp, c.PageMap)
	}
	if _, err := h.build(); err != nil {
		t.Fatalf("seconda build: %v", err)
	}
	second := h.cliCalls()
	if len(second) != 1 || !strings.Contains(second[0], "--toc-pagemap") {
		t.Fatalf("hit atteso: 1 render con mappa, ottenuto %d:\n%s", len(second), strings.Join(second, "\n"))
	}
	if h.selector != 2 {
		t.Fatalf("selezione motore: attesa 1 per build (2 totali), ottenute %d", h.selector)
	}
}

func TestCacheInvalidatesOnSourceChange(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if err := os.WriteFile(filepath.Join(h.vDir, "c1.md"), []byte("Testo modificato.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	calls := h.cliCalls()
	if !hasProbeCall(calls) {
		t.Fatalf("sorgenti cambiate: attesa una passata di sonda, chiamate:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnTrimAndBleedChange(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	c := h.cfg()
	c.Trim = "5x8"
	if _, err := h.buildWith(c); err != nil {
		t.Fatalf("build 5x8: %v", err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("trim cambiato: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
	h.cliCalls()
	c = h.cfg()
	c.Bleed = true
	if _, err := h.buildWith(c); err != nil {
		t.Fatalf("build con bleed: %v", err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("bleed cambiato: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnCacheSchemaMismatch(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	c, _ := h.cache()
	c.CacheSchema = cacheSchema - 1
	h.writeCache(c)
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("schema vecchio: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnGuiIdentityChange(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	withCleanBuild(t, strings.Repeat("b", 40))
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("revisione GUI diversa: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDirtyBuildFailsClosedButKeepsConfig(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	before, _ := h.cache()
	withDirtyBuild(t)
	h.cliCalls()
	c := h.cfg()
	c.Trim = "5x8"
	if _, err := h.buildWith(c); err != nil {
		t.Fatalf("build con albero dirty: %v", err)
	}
	calls := h.cliCalls()
	if !hasProbeCall(calls) {
		t.Fatalf("build dirty: la cache non deve essere riusata:\n%s", strings.Join(calls, "\n"))
	}
	after, _ := h.cache()
	if after.RenderStamp != "" {
		t.Errorf("build dirty: RenderStamp non deve essere persistito (%q)", after.RenderStamp)
	}
	if after.CacheSchema != cacheSchema || len(after.PageMap) != 0 || after.PageMapStamp != "" {
		t.Errorf("build dirty: la mappa non deve essere persistita come valida: %+v", after)
	}
	if after.Trim != "5x8" || after.Pages == 0 || after.OutDir != before.OutDir {
		t.Errorf("build dirty: configurazione persa: %+v", after)
	}
	if after.EnvStamp == "" {
		t.Error("build dirty: EnvStamp deve restare disponibile per il Watch")
	}
}

func TestCacheInvalidatesOnToolDigestSameSizeAndMtime(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(h.tool)
	if err != nil {
		t.Fatal(err)
	}
	// Stesso numero di byte, contenuto diverso, mtime ripristinato.
	body, err := os.ReadFile(h.tool)
	if err != nil {
		t.Fatal(err)
	}
	mutated := []byte(strings.Replace(string(body), "Testo del capitolo.", "Testo del capitolo!", 1))
	if len(mutated) != len(body) {
		t.Fatalf("mutazione di lunghezza diversa: %d vs %d", len(mutated), len(body))
	}
	if err := os.WriteFile(h.tool, mutated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(h.tool, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("CLI cambiata (stessi size/mtime): attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func (h *printHarness) writeVaultFont(name string, data []byte) string {
	h.t.Helper()
	dir := filepath.Join(h.vDir, "fonts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func TestCacheInvalidatesOnFontDigestSameSizeAndMtime(t *testing.T) {
	h := newPrintHarness(t)
	fontPath := h.writeVaultFont("testfont.woff2", []byte("FONTDATA-A"))
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fontPath, []byte("FONTDATA-B"), 0o600); err != nil { // stessa lunghezza
		t.Fatal(err)
	}
	if err := os.Chtimes(fontPath, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("font cambiato (stessi size/mtime): attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnRendererChange(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Motore aggiornato: stesso percorso ma binario diverso (mtime/size) e
	// versione diversa.
	writeScript(t, h.engine, fakeEngineBody+"\n: aggiornato\n")
	if err := os.WriteFile(h.version, []byte("FakeChrome 2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("renderer aggiornato: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnPlatformChange(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	withPlatform(t, "altro/altro@2")
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("piattaforma diversa: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCacheInvalidatesOnFontPrecedenceChange(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("VAULT-A"))
	dirB := filepath.Join(h.dir, "fontdir-b")
	if err := os.MkdirAll(dirB, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "testfont.woff2"), []byte("DIRB-B"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUBCRAFT_FONTS_DIR", dirB)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Il vault prevale: rimuovendolo vince la dir B (precedenza diversa).
	if err := os.Remove(filepath.Join(h.vDir, "fonts", "testfont.woff2")); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("cambio di precedenza font: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestShadowedFontChangeDoesNotInvalidate(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("VAULT-A"))
	dirB := filepath.Join(h.dir, "fontdir-b")
	if err := os.MkdirAll(dirB, 0o700); err != nil {
		t.Fatal(err)
	}
	shadowed := filepath.Join(dirB, "testfont.woff2")
	if err := os.WriteFile(shadowed, []byte("DIRB-B"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUBCRAFT_FONTS_DIR", dirB)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Il file oscurato cambia: il rendering non lo usa, quindi la cache vale.
	if err := os.WriteFile(shadowed, []byte("DIRB-B-MODIFICATO"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	calls := h.cliCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--toc-pagemap") {
		t.Fatalf("font oscurato modificato non deve invalidare, chiamate:\n%s", strings.Join(calls, "\n"))
	}
}

func TestStaleFontInOutputBlocksReuseAndPersistence(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("SORGENTE-NUOVA"))
	// Output preesistente con un font obsoleto: il C non lo sovrascrive.
	oldOut := filepath.Join(h.outDir, "fonts")
	if err := os.MkdirAll(oldOut, 0o700); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(oldOut, "testfont.woff2")
	if err := os.WriteFile(stalePath, []byte("OUTPUT-VECCHIO"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := h.build()
	if err != nil {
		t.Fatalf("build con font obsoleto: %v", err)
	}
	if !strings.Contains(out, "font obsoleti") {
		t.Errorf("manca la diagnosi sui font obsoleti:\n%s", out)
	}
	c, _ := h.cache()
	if len(c.PageMap) != 0 || c.RenderStamp != "" {
		t.Errorf("font obsoleti: la mappa non deve essere persistita come valida: %+v", c)
	}
	// Nessun font toccato automaticamente.
	got, err := os.ReadFile(stalePath)
	if err != nil || string(got) != "OUTPUT-VECCHIO" {
		t.Errorf("il font esistente nell'output è stato modificato: %q (%v)", got, err)
	}
	// Anche la build successiva non riusa nulla.
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("font obsoleti: nessun riuso atteso:\n%s", strings.Join(calls, "\n"))
	}
}

func TestLegacyCacheKeepsConfigButNotMap(t *testing.T) {
	h := newPrintHarness(t)
	legacy := printConfig{
		Trim: "5x8", Pages: 123, Bleed: true, OutDir: h.outDir,
		SourceStamp: "vecchio", PageMap: tocPageMap{"ch1": 9}, PageMapStamp: "vecchio|5x8|true",
	}
	h.writeCache(legacy)
	c := loadPrintConfig(h.vDir, h.book)
	if c.Trim != "5x8" || c.Pages != 123 || !c.Bleed || c.OutDir != h.outDir {
		t.Fatalf("configurazione legacy non leggibile: %+v", c)
	}
	h.cliCalls()
	// build con la stessa configurazione legacy: la vecchia mappa non va usata
	if _, err := h.buildWith(c); err != nil {
		t.Fatalf("build legacy: %v", err)
	}
	calls := h.cliCalls()
	if !hasProbeCall(calls) {
		t.Fatalf("cache legacy: la vecchia mappa non deve essere accettata:\n%s", strings.Join(calls, "\n"))
	}
	after, _ := h.cache()
	if after.CacheSchema != cacheSchema || after.RenderStamp == "" || len(after.PageMap) == 0 {
		t.Errorf("dopo la build la cache deve essere riscritta valida: %+v", after)
	}
	// Pages diventa il conteggio misurato (comportamento preesistente della
	// pipeline); le altre impostazioni legacy restano.
	if after.Trim != "5x8" || !after.Bleed || after.OutDir != h.outDir || after.Pages == 0 {
		t.Errorf("configurazione legacy persa: %+v", after)
	}
}

// --- 2. Probe: timeout, errore, troncamento, cambio motore -------------------

func TestRendererProbeTimeoutFailsClosed(t *testing.T) {
	h := newPrintHarness(t)
	t.Setenv("FAKE_ENGINE_MODE", "slow")
	start := time.Now()
	if _, err := h.build(); err != nil {
		t.Fatalf("build con probe in timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("probe non limitato dal timeout: %v", elapsed)
	}
	c, _ := h.cache()
	if c.RenderStamp != "" || len(c.PageMap) != 0 {
		t.Errorf("probe in timeout: identità ignota, niente persistenza della mappa: %+v", c)
	}
	if c.Trim == "" || c.OutDir == "" {
		t.Errorf("probe in timeout: la configurazione deve restare persistita: %+v", c)
	}
}

func TestRendererProbeFailureFailsClosed(t *testing.T) {
	h := newPrintHarness(t)
	t.Setenv("FAKE_ENGINE_MODE", "fail")
	if _, err := h.build(); err != nil {
		t.Fatalf("build con probe fallito: %v", err)
	}
	c, _ := h.cache()
	if c.RenderStamp != "" || len(c.PageMap) != 0 {
		t.Errorf("probe fallito: niente mappa riutilizzabile: %+v", c)
	}
}

func TestRendererProbeTruncatesOutput(t *testing.T) {
	h := newPrintHarness(t)
	t.Setenv("FAKE_ENGINE_MODE", "junk")
	v, state := rendererVersion(h.engine)
	if state != probeStateOK {
		t.Fatalf("probe con output enorme: atteso esito OK (troncato), stato %d", state)
	}
	if len(v) > rendererProbeMaxBytes {
		t.Fatalf("output del probe non limitato: %d byte (max %d)", len(v), rendererProbeMaxBytes)
	}
	if len(v) == 0 {
		t.Fatal("versione vuota")
	}
}

func TestRendererChangeWithinSameProcess(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Secondo motore nello stesso processo, stessa versione: l'identità del
	// file cambia, la memoizzazione del probe non deve mascherare il cambio.
	other := filepath.Join(h.bin, "fake-chrome-2")
	writeScript(t, other, fakeEngineBody)
	prev := selectPDFBrowser
	selectPDFBrowser = func() string { return other }
	t.Cleanup(func() { selectPDFBrowser = prev })
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if calls := h.cliCalls(); !hasProbeCall(calls) {
		t.Fatalf("cambio motore nello stesso processo: attesa una sonda:\n%s", strings.Join(calls, "\n"))
	}
}

func TestIdentityUsesTheRenderedEngine(t *testing.T) {
	h := newPrintHarness(t)
	before := h.selector
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	if h.selector-before != 1 {
		t.Fatalf("selezioni del motore per build: %d (attesa 1)", h.selector-before)
	}
	id := computeRenderIdentity(h.engine, h.tool, h.vDir, h.outDir)
	if id.Engine != h.engine {
		t.Fatalf("identità calcolata su %q, motore usato %q", id.Engine, h.engine)
	}
	// Il motore che ha prodotto il PDF è lo stesso percorso dell'identità.
	renders := h.engineCalls()
	found := false
	for _, line := range renders {
		if strings.HasPrefix(line, h.engine+" ") && strings.Contains(line, "--print-to-pdf=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("nessuna renderizzazione con il motore %q:\n%s", h.engine, strings.Join(renders, "\n"))
	}
}

// --- 3. Anteprima ----------------------------------------------------------

func TestPreviewIncompatibleSignatureNoProbeNoWrite(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Firma incompatibile: come se l'ambiente di rendering fosse cambiato.
	c, _ := h.cache()
	c.RenderStamp = strings.Repeat("f", 64)
	h.writeCache(c)
	cachePath := filepath.Join(h.vDir, ".pubcraft-print.json")
	tampered, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	artDir := filepath.Join(h.dir, "preview-art")
	if mapToUse := previewPageMap(h.vDir, h.book, "6x9", false, h.engine, artDir); mapToUse != nil {
		t.Fatalf("firma incompatibile: nessuna mappa attesa, ottenuta %v", mapToUse)
	}
	if _, _, _, _, _, err := renderPrintCore(h.vDir, h.book, "6x9", 100, false, artDir, h.engine, nil, false); err != nil {
		t.Fatalf("render anteprima: %v", err)
	}
	calls := h.cliCalls()
	if len(calls) != 1 {
		t.Fatalf("anteprima: %d render, atteso 1:\n%s", len(calls), strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[0], "--no-toc-pagenumbers") {
		t.Fatalf("anteprima: atteso indice senza numeri, chiamata %q", calls[0])
	}
	if hasProbeCall(calls) {
		t.Fatal("anteprima: nessuna sonda TOC consentita")
	}
	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(tampered) {
		t.Error("l'anteprima ha modificato la cache")
	}
}

func TestPreviewValidCacheReusesMapWithoutProbe(t *testing.T) {
	h := newPrintHarness(t)
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	h.cliCalls()
	artDir := filepath.Join(h.dir, "preview-ok")
	m := previewPageMap(h.vDir, h.book, "6x9", false, h.engine, artDir)
	if m == nil {
		t.Fatal("anteprima con cache valida: mappa attesa")
	}
	if _, _, _, _, _, err := renderPrintCore(h.vDir, h.book, "6x9", 100, false, artDir, h.engine, m, false); err != nil {
		t.Fatal(err)
	}
	calls := h.cliCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--toc-pagemap") {
		t.Fatalf("anteprima con cache valida: 1 render con mappa atteso:\n%s", strings.Join(calls, "\n"))
	}
}

// --- 4. Watch e validità (unit) --------------------------------------------

func TestWatchEnvironmentChangeIsDetected(t *testing.T) {
	h := newPrintHarness(t)
	setVault(h.vDir)
	t.Cleanup(func() { setVault("") })
	if _, err := h.build(); err != nil {
		t.Fatal(err)
	}
	// Nessun cambiamento: il Watch non deve rigenerare.
	h.cliCalls()
	w := newRecorder()
	apiWatchPDF(w, newJSONRequest("GET", "/api/watch/pdf", ""))
	if w.Code != 200 {
		t.Fatalf("watch: %d %s", w.Code, w.Body.String())
	}
	if calls := h.cliCalls(); len(calls) != 0 {
		t.Fatalf("watch senza cambiamenti: nessuna rigenerazione attesa, chiamate:\n%s", strings.Join(calls, "\n"))
	}
	// Cambia solo l'ambiente di rendering (motore aggiornato).
	writeScript(t, h.engine, fakeEngineBody+"\n: aggiornato\n")
	w = newRecorder()
	apiWatchPDF(w, newJSONRequest("GET", "/api/watch/pdf", ""))
	if w.Code != 200 {
		t.Fatalf("watch dopo cambio ambiente: %d %s", w.Code, w.Body.String())
	}
	if calls := h.cliCalls(); len(calls) == 0 {
		t.Fatal("cambio dell'ambiente di rendering ignorato dal Watch")
	}
}

func TestPageMapValidRequiresAllInputs(t *testing.T) {
	id := renderIdentity{Known: true, Stamp: "stamp"}
	base := printConfig{
		CacheSchema: cacheSchema, RenderStamp: "stamp", SourceStamp: "src",
		Trim: "6x9", Bleed: false, PageMap: tocPageMap{"ch1": 1},
	}
	base.PageMapStamp = pageMapStampOf(base)
	if !pageMapValid(base, id, "src", "6x9", false) {
		t.Fatal("configurazione identica: la mappa deve essere valida")
	}
	cases := []struct {
		name string
		mut  func(*printConfig, *renderIdentity)
	}{
		{"identità corrente ignota", func(_ *printConfig, i *renderIdentity) { i.Known = false }},
		{"font stale", func(_ *printConfig, i *renderIdentity) { i.FontsStale = true }},
		{"schema vecchio", func(c *printConfig, _ *renderIdentity) { c.CacheSchema = 1 }},
		{"render stamp assente", func(c *printConfig, _ *renderIdentity) { c.RenderStamp = "" }},
		{"render stamp diverso", func(c *printConfig, _ *renderIdentity) { c.RenderStamp = "altro" }},
		{"sorgenti diverse", func(c *printConfig, _ *renderIdentity) { c.SourceStamp = "altro" }},
		{"trim diverso", func(c *printConfig, _ *renderIdentity) { c.Trim = "5x8" }},
		{"bleed diverso", func(c *printConfig, _ *renderIdentity) { c.Bleed = true }},
		{"mappa vuota", func(c *printConfig, _ *renderIdentity) { c.PageMap = nil }},
		{"page map stamp assente", func(c *printConfig, _ *renderIdentity) { c.PageMapStamp = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			idc := id
			tc.mut(&c, &idc)
			if pageMapValid(c, idc, c.SourceStamp, c.Trim, c.Bleed) {
				t.Fatalf("%s: la mappa non doveva essere valida", tc.name)
			}
		})
	}
}

// --- 5. Parità della risoluzione font con la CLI reale ---------------------

func TestFontResolutionMirrorsCLI(t *testing.T) {
	bin := os.Getenv("PUBCRAFT_BIN")
	if bin == "" {
		cand, err := filepath.Abs(filepath.Join("..", "..", "build", "pubcraft"))
		if err != nil || !fileExists(cand) {
			t.Skip("binario C non disponibile: esegui make prima di go test")
		}
		bin = cand
	}
	dir := t.TempDir()
	vDir := filepath.Join(dir, "vault")
	outDir := filepath.Join(vDir, "out-stampa")
	if err := os.MkdirAll(filepath.Join(vDir, "fonts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vDir, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vDir, "book.yaml"),
		[]byte("title: Font\nauthor: A\nlanguage: it\nchapters:\n- file: c1.md\n  title: Capitolo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vDir, "c1.md"), []byte("Testo.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Due sorgenti per lo stesso nome: il vault vince (prima directory in ordine C).
	vaultFont := []byte("FONT-VAULT")
	if err := os.WriteFile(filepath.Join(vDir, "fonts", "parity-font.woff2"), vaultFont, 0o600); err != nil {
		t.Fatal(err)
	}
	shadowDir := filepath.Join(dir, "shadow-fonts")
	if err := os.MkdirAll(shadowDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadowDir, "parity-font.woff2"), []byte("FONT-SHADOW"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadowDir, "solo-ombra.woff2"), []byte("SOLO-OMBRA"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUBCRAFT_FONTS_DIR", shadowDir)

	// Esecuzione reale della CLI (copre anche la copia dei font).
	out, err := runCommand(t, bin, "build-print", vDir, outDir, "--trim", "6x9", "--pages", "100")
	if err != nil {
		t.Fatalf("build-print reale: %v\n%s", err, out)
	}
	res := resolveFonts(vDir, outDir, bin, true)
	if res.Unknown != "" {
		t.Fatalf("risoluzione font ignorata: %s", res.Unknown)
	}
	// Il modello prevede: parity-font dal vault, solo-ombra dalla dir ombra.
	want := map[string]string{}
	for _, f := range res.Fonts {
		want[f.Name] = f.Digest
	}
	if _, ok := want["parity-font.woff2"]; !ok {
		t.Fatalf("modello: parity-font.woff2 assente: %v", want)
	}
	if _, ok := want["solo-ombra.woff2"]; !ok {
		t.Fatalf("modello: solo-ombra.woff2 assente: %v", want)
	}
	entries, err := os.ReadDir(filepath.Join(outDir, "fonts"))
	if err != nil {
		t.Fatalf("outDir/fonts non creato dalla CLI: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		digest, err := fileDigest(filepath.Join(outDir, "fonts", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got[e.Name()] = digest
	}
	if len(got) != len(want) {
		t.Fatalf("font copiati dalla CLI (%d) diversi da quelli previsti (%d)\n CLI: %v\nmodello: %v", len(got), len(want), got, want)
	}
	for name, d := range want {
		if got[name] != d {
			t.Fatalf("font %s: digest CLI %q != modello %q", name, got[name], d)
		}
	}
}

// BenchmarkComputeRenderIdentity misura il costo reale dell'identità:
// CLI vera (build/pubcraft, ~137 KB) se disponibile, 24 font sintetici da
// ~73 KB (stessa mole della cartella fonts/ del repo, ~1,8 MB), motore
// fittizio per il probe (memoizzato per identità del file).
func BenchmarkComputeRenderIdentity(b *testing.B) {
	bin := filepath.Join("..", "..", "build", "pubcraft")
	if !fileExists(bin) {
		b.Skip("build/pubcraft non disponibile")
	}
	tool, err := filepath.Abs(bin)
	if err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	vDir := filepath.Join(dir, "vault")
	outDir := filepath.Join(vDir, "out-stampa")
	if err := os.MkdirAll(filepath.Join(vDir, "fonts"), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		b.Fatal(err)
	}
	block := make([]byte, 73*1024)
	for i := range block {
		block[i] = byte(i)
	}
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("font%02d.woff2", i)
		if err := os.WriteFile(filepath.Join(vDir, "fonts", name), block, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	engine := filepath.Join(dir, "fake-engine")
	writeScriptB(b, engine, fakeEngineBody)
	versionFile := filepath.Join(dir, "version")
	if err := os.WriteFile(versionFile, []byte("FakeChrome 1.0\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	b.Setenv("FAKE_ENGINE_VERSION_FILE", versionFile)
	b.Setenv("FAKE_ENGINE_MODE", "ok")
	b.Setenv("RENDER_TEST_LOG", filepath.Join(dir, "bench.log"))
	b.Setenv("FAKE_PDF_DIR", dir)
	b.Setenv("PUBCRAFT_FONTS_DIR", "")
	prevRev := buildRevision
	buildRevision = func() (string, bool, bool) { return strings.Repeat("a", 40), false, true }
	defer func() { buildRevision = prevRev }()
	prevPlatform := platformID
	platformID = func() string { return "test/test@1" }
	defer func() { platformID = prevPlatform }()

	if res := resolveFonts(vDir, outDir, tool, true); res.Unknown == "" {
		var total int64
		for _, f := range res.Fonts {
			total += f.Size
		}
		if st, err := os.Stat(tool); err == nil {
			b.Logf("font risolti: %d (%.1f KiB) + CLI %.1f KiB", len(res.Fonts), float64(total)/1024, float64(st.Size())/1024)
		}
	}

	if id := computeRenderIdentity(engine, tool, vDir, outDir); true {
		b.Logf("identità: known=%v font=%d reason=%q", id.Known, len(id.Fonts.Fonts), id.Reason)
	}
	b.Run("solo-digest-font", func(b *testing.B) {
		paths := make([]string, 0, 24)
		for i := 0; i < 24; i++ {
			paths = append(paths, filepath.Join(vDir, "fonts", fmt.Sprintf("font%02d.woff2", i)))
		}
		for i := 0; i < b.N; i++ {
			for _, p := range paths {
				if _, err := fileDigest(p); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("probe-versione-prima-volta", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resetRendererProbeCache()
			rendererVersion(engine)
		}
	})
	b.Run("probe-versione-memoizzato", func(b *testing.B) {
		resetRendererProbeCache()
		rendererVersion(engine)
		for i := 0; i < b.N; i++ {
			rendererVersion(engine)
		}
	})
	b.Run("env-stamp-metadata", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			computeEnvStamp(engine, tool, vDir, outDir)
		}
	})
	b.Run("identita-completa", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			computeRenderIdentity(engine, tool, vDir, outDir)
		}
	})
}

func writeScriptB(b *testing.B, path, body string) {
	b.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		b.Fatal(err)
	}
}

// --- 6. Font: output vs sorgenti (regressione del difetto segnalato) --------

// resolveFonts deve tenere distinte due tabelle: le sorgenti vincenti secondo
// la precedenza del C e i file realmente presenti in <outdir>/fonts. Un font
// presente in output NON è un orfano se una sorgente lo fornisce con lo stesso
// contenuto (prima veniva escluso dalle sorgenti e quindi marcato obsoleto).

func (h *printHarness) fontDir() string { return filepath.Join(h.outDir, "fonts") }

func (h *printHarness) writeOutputFont(name string, data []byte) string {
	h.t.Helper()
	if err := os.MkdirAll(h.fontDir(), 0o700); err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(h.fontDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func TestFontResolutionOutputIdenticalToSourceNotStale(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("STESSO-CONTENUTO"))
	h.writeOutputFont("testfont.woff2", []byte("STESSO-CONTENUTO"))
	res := resolveFonts(h.vDir, h.outDir, h.tool, true)
	if res.Unknown != "" {
		t.Fatalf("risoluzione ignorata: %s", res.Unknown)
	}
	if len(res.Stale) != 0 {
		t.Fatalf("font identico alla sorgente marcato obsoleto: %v", res.Stale)
	}
	if len(res.Fonts) != 1 || res.Fonts[0].Name != "testfont.woff2" {
		t.Fatalf("set risolto inatteso: %+v", res.Fonts)
	}
	// e la mappa è riutilizzabile: due build identiche, la seconda senza sonda
	if _, err := h.build(); err != nil {
		t.Fatalf("prima build: %v", err)
	}
	h.cliCalls()
	if _, err := h.build(); err != nil {
		t.Fatalf("seconda build: %v", err)
	}
	calls := h.cliCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--toc-pagemap") {
		t.Fatalf("font identico in output: atteso riuso (1 render con mappa), chiamate:\n%s", strings.Join(calls, "\n"))
	}
}

func TestFontResolutionOutputDifferentFromSourceIsStale(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("SORGENTE-NUOVA"))
	h.writeOutputFont("testfont.woff2", []byte("OUTPUT-VECCHIO"))
	res := resolveFonts(h.vDir, h.outDir, h.tool, true)
	if res.Unknown != "" {
		t.Fatalf("risoluzione ignorata: %s", res.Unknown)
	}
	if len(res.Stale) != 1 || res.Stale[0] != "testfont.woff2" {
		t.Fatalf("font diverso dalla sorgente non marcato obsoleto: %v", res.Stale)
	}
	// il contenuto effettivo è quello dell'output (il C non sovrascrive)
	for _, f := range res.Fonts {
		if f.Name != "testfont.woff2" {
			continue
		}
		if f.Source != h.fontDir() {
			t.Fatalf("contenuto effettivo atteso dall'output, trovato %q", f.Source)
		}
	}
}

func TestFontResolutionOutputWithoutSourceIsStale(t *testing.T) {
	h := newPrintHarness(t)
	h.writeOutputFont("orfano.woff2", []byte("SOLO-OUTPUT"))
	res := resolveFonts(h.vDir, h.outDir, h.tool, true)
	if res.Unknown != "" {
		t.Fatalf("risoluzione ignorata: %s", res.Unknown)
	}
	if len(res.Stale) != 1 || res.Stale[0] != "orfano.woff2" {
		t.Fatalf("font senza sorgente non marcato obsoleto: %v", res.Stale)
	}
}

// Percorso reale con copia effettiva dei font (la CLI fittizia li copia come
// il C, first-wins): la prima esportazione persiste la mappa, una seconda
// esportazione identica la riusa senza sonda.
func TestFontCopyPipelinePersistsAndReusesMap(t *testing.T) {
	h := newPrintHarness(t)
	h.writeVaultFont("testfont.woff2", []byte("FONT-COPIATO"))
	if _, err := h.build(); err != nil {
		t.Fatalf("prima esportazione: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.fontDir(), "testfont.woff2")); err != nil {
		t.Fatalf("la CLI non ha copiato il font in <outdir>/fonts: %v", err)
	}
	first := h.cliCalls()
	if len(first) != 2 {
		t.Fatalf("prima esportazione: %d render (attesi 2: sonda + numerata):\n%s", len(first), strings.Join(first, "\n"))
	}
	c, _ := h.cache()
	if len(c.PageMap) == 0 || c.RenderStamp == "" {
		t.Fatalf("prima esportazione: mappa non persistita: %+v", c)
	}
	second, err := h.build()
	if err != nil {
		t.Fatalf("seconda esportazione: %v", err)
	}
	calls := h.cliCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--toc-pagemap") {
		t.Fatalf("seconda esportazione identica: atteso riuso senza sonda, chiamate:\n%s\noutput:\n%s", strings.Join(calls, "\n"), second)
	}
	if hasProbeCall(calls) {
		t.Fatal("seconda esportazione: nessuna sonda consentita con cache valida")
	}
}
