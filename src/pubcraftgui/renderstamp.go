package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// Validità della cache della mappa-pagina (C1).
//
// Tre valori distinti, mai confrontati fra loro a due a due:
//  1. identità *persistita* in .pubcraft-print.json insieme alla mappa
//     (RenderStamp/EnvStamp/CacheSchema, scritti solo a build riuscita);
//  2. identità dell'*ambiente corrente* (computeRenderIdentity), ricalcolata a
//     ogni generazione esplicita dal motore già selezionato;
//  3. decisione di riuso (pageMapValid) = confronto fra la 2 e la 1.
//
// Nessun hashing indiscriminato: si fa il digest solo di CLI e font (piccoli,
// letti una volta per generazione esplicita), mai del renderer (centinaia di
// MB) e mai durante i poll di stato. Per il renderer: versione (probe con
// timeout) + path/size/mtime; il limite residuo è dichiarato.

// cacheSchema è la versione esplicita del formato/semantica della cache,
// distinta dalla versione commerciale dell'applicazione.
const cacheSchema = 2

// layoutSchemaVersion va incrementata quando cambia qualcosa che sposta la
// paginazione lato Go (CSS tipografico embedded, regole di sezione, meccanismo
// dei numeri di pagina). Il lato C è coperto dal digest del binario.
const layoutSchemaVersion = 1

// Budget dei probe: mai bloccanti oltre il timeout, output realmente limitato.
const (
	rendererProbeTimeout  = 2 * time.Second
	rendererProbeMaxBytes = 4096
	// Oltre questa dimensione un font viene identificato solo per metadata:
	// limite dichiarato, nessun hashing di file enormi.
	fontDigestMaxSize = 32 << 20
	// Cap di sicurezza sulla lettura delle directory font (come il C, che
	// legge tutto: qui un numero anomalo è trattato come identità ignota).
	fontDirMaxEntries = 4096
)

// selectPDFBrowser è la selezione del motore: una sola volta per generazione,
// sostituibile nei test. Il percorso restituito è quello usato sia per
// l'identità sia per il rendering.
var selectPDFBrowser = findPDFBrowser

// buildRevision restituisce revisione VCS completa, stato dirty e disponibilità.
var buildRevision = func() (string, bool, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if len(rev) != 40 {
		return "", false, false
	}
	return rev, modified == "true", true
}

// platformID identifica la piattaforma (e la versione OS, prudenziale: non
// identifica i font di fallback di sistema). Sostituibile nei test.
var platformID = func() string {
	return runtime.GOOS + "/" + runtime.GOARCH + "@" + osVersion()
}

var osverOnce sync.Once
var osverValue = "unknown"

// osVersion legge la versione del sistema una volta per processo: su Linux
// senza eseguire nulla, su macOS con un singolo probe breve, su Windows
// dichiaratamente non disponibile (nessun exec di sistema).
func osVersion() string {
	osverOnce.Do(func() { osverValue = probeOSVersion() })
	return osverValue
}

func probeOSVersion() string {
	switch runtime.GOOS {
	case "linux":
		if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	case "darwin":
		if out, ok := runCapped("sw_vers", "-productVersion"); ok {
			if line := firstLine(out); line != "" {
				return line
			}
		}
	}
	return "unknown"
}

// limitedBuffer tronca davvero l'output: la memoria resta limitata a max
// byte, il resto viene scartato (il processo non riceve EPIPE).
type limitedBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
			b.truncated = true
		} else {
			b.buf = append(b.buf, p...)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.buf) }

// runCapped esegue un comando breve con timeout e output limitato.
func runCapped(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), rendererProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// Se il processo lascia figli che tengono aperte le pipe, Wait non deve
	// restare bloccato oltre il budget: WaitDelay chiude le pipe dopo il kill.
	cmd.WaitDelay = 500 * time.Millisecond
	buf := &limitedBuffer{max: rendererProbeMaxBytes}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Run(); err != nil {
		return "", false
	}
	s := strings.TrimSpace(buf.String())
	if s == "" {
		return "", false
	}
	return s, true
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// rendererProbeKind dice se il motore supporta un probe di versione. Non è una
// regola sulla cache: identità e geometria decidono la validità. Serve solo a
// non eseguire flag non supportati (helper WebKit) o rischiosi (Windows, dove
// avviare il browser senza i flag di stampa può aprire una finestra).
type rendererProbeMode int

const (
	probeVersionFlag rendererProbeMode = iota // --version
	probeNone                                 // nessun flag: solo metadata
)

func rendererProbeKind(engine string) rendererProbeMode {
	if runtime.GOOS == "windows" {
		return probeNone
	}
	base := strings.ToLower(filepath.Base(engine))
	if strings.Contains(base, "html2pdf") {
		return probeNone
	}
	return probeVersionFlag
}

// rendererProbe esegue il probe. Sostituibile nei test.
var rendererProbe = func(engine string, mode rendererProbeMode) (string, bool) {
	if mode == probeNone {
		return "", false
	}
	out, ok := runCapped(engine, "--version")
	if !ok {
		return "", false
	}
	return firstLine(out), true
}

type rendererProbeState int

const (
	probeStateOK          rendererProbeState = iota
	probeStateUnavailable                    // nessun flag eseguito (Windows/helper WebKit): limite dichiarato
	probeStateFailed                         // eseguito ma errore/timeout: identità non determinabile
)

type rendererProbeEntry struct {
	version string
	state   rendererProbeState
}

var (
	rendererProbeMu    sync.Mutex
	rendererProbeCache = map[string]rendererProbeEntry{}
)

// fileFingerprint identifica il *file* (path, size, mtime): è la chiave di
// memoizzazione del probe, così un motore o un binario diverso invalida.
func fileFingerprint(path string) (string, os.FileInfo, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", nil, false
	}
	return fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano()), st, true
}

// rendererVersion applica la memoizzazione per identità del file (non un
// sync.Once globale): stesso binario ⇒ un solo probe per processo; binario
// sostituito ⇒ nuovo probe. Anche gli esiti negativi sono memorizzati per
// identità, così un motore che non risponde non costa 2 s a ogni build finché
// il binario non cambia.
func rendererVersion(engine string) (string, rendererProbeState) {
	key, _, ok := fileFingerprint(engine)
	if !ok {
		return "", probeStateFailed
	}
	if rendererProbeKind(engine) == probeNone {
		// Nessun flag eseguito: l'identità resta quella dei metadata. È un
		// limite dichiarato, non un fallimento.
		return "", probeStateUnavailable
	}
	rendererProbeMu.Lock()
	if e, hit := rendererProbeCache[key]; hit {
		rendererProbeMu.Unlock()
		return e.version, e.state
	}
	rendererProbeMu.Unlock()

	v, ok := rendererProbe(engine, rendererProbeKind(engine))
	state := probeStateOK
	if !ok {
		state = probeStateFailed
	}
	rendererProbeMu.Lock()
	rendererProbeCache[key] = rendererProbeEntry{version: v, state: state}
	rendererProbeMu.Unlock()
	return v, state
}

// resetRendererProbeCache è usato solo dai test.
func resetRendererProbeCache() {
	rendererProbeMu.Lock()
	rendererProbeCache = map[string]rendererProbeEntry{}
	rendererProbeMu.Unlock()
}

// fileDigest calcola lo SHA-256 del contenuto. Su file oltre fontDigestMaxSize
// restituisce un'identità di soli metadata: limite dichiarato, niente lettura
// di file enormi.
func fileDigest(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > fontDigestMaxSize {
		return fmt.Sprintf("meta:%d:%d", st.Size(), st.ModTime().UnixNano()), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- Font: risoluzione come la fa il C (ordine e first-wins) -----------------

// resolvedFont descrive un file font che il rendering userà davvero.
type resolvedFont struct {
	Name   string
	Source string // directory da cui proviene il contenuto
	Size   int64
	MTime  int64
	Digest string // vuoto quando si chiede la sola metadata
}

// fontResolution è l'esito della risoluzione.
type fontResolution struct {
	Fonts   []resolvedFont
	Stale   []string // file in outDir/fonts che oscurano una sorgente diversa (o non hanno sorgente)
	Unknown string   // non vuoto ⇒ identità non determinabile (fail-closed)
}

// fontCandidateDirs riproduce l'ordine di src/pubcraft.c:2088-2107.
// Le directory 6 e 7 sono relative alla CWD del processo, come nel C.
func fontCandidateDirs(vDir, toolPath string) []string {
	var dirs []string
	if vDir != "" {
		dirs = append(dirs, filepath.Join(vDir, "fonts"))
	}
	if d := os.Getenv("PUBCRAFT_FONTS_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	exeDir := filepath.Dir(toolPath)
	dirs = append(dirs,
		filepath.Join(exeDir, "fonts"),
		filepath.Join(exeDir, "..", "Resources", "fonts"),
		filepath.Join(exeDir, "..", "share", "pubcraft", "fonts"),
		"fonts",
		filepath.Join("src", "pubcraftgui", "web", "static", "fonts"),
		"/usr/local/share/pubcraft/fonts",
		"/usr/share/pubcraft/fonts",
	)
	return dirs
}

func isFontFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".woff", ".woff2":
		return true
	}
	return false
}

// scanFontDir elenca i font (non nascosti) di una directory. Una directory
// inesistente o non apribile è ignorata come fa il C (opendir fallisce in
// silenzio); un numero anomalo di voci è invece trattato come identità ignota.
func scanFontDir(dir string) (map[string]os.FileInfo, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	if len(entries) > fontDirMaxEntries {
		return nil, fmt.Sprintf("%s: troppi file (%d)", dir, len(entries))
	}
	out := map[string]os.FileInfo{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !isFontFile(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Sprintf("%s: metadata di %s non leggibili: %v", dir, name, err)
		}
		out[name] = info
	}
	return out, ""
}

// resolveFonts ricostruisce i font che il rendering userà davvero:
//   - i file già presenti in <outDir>/fonts vincono su tutto (copy_fonts non
//     sovrascrive: access(df,F_OK) nel C),
//   - per i nomi mancanti, la prima directory in ordine C che li fornisce.
//
// wantDigests=false produce la sola metadata (usata dal Watch, che non deve
// calcolare digest nei poll); con wantDigests=true si confrontano i contenuti
// e si segnalano i font "obsoleti" che l'output continua a usare.
func resolveFonts(vDir, outDir, toolPath string, wantDigests bool) fontResolution {
	var res fontResolution
	outFontDir := filepath.Join(outDir, "fonts")
	existing, uerr := scanFontDir(outFontDir)
	if uerr != "" {
		res.Unknown = uerr
		return res
	}

	type fontSource struct {
		dir  string
		info os.FileInfo
	}
	sources := map[string]fontSource{}
	for _, dir := range fontCandidateDirs(vDir, toolPath) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		entries, uerr := scanFontDir(abs)
		if uerr != "" {
			res.Unknown = uerr
			return res
		}
		for name, info := range entries {
			if _, ok := sources[name]; ok {
				continue // first wins, come il C
			}
			// Nota: i nomi presenti in <outDir>/fonts NON vengono esclusi da
			// sources. Sono due tabelle distinte: sources = vincitori secondo
			// la precedenza del C, existing = file realmente in output. Servono
			// entrambe per distinguere "identico alla sorgente" da "obsoleto".
			sources[name] = fontSource{dir: abs, info: info}
		}
	}

	add := func(name, dir string, info os.FileInfo) string {
		rf := resolvedFont{Name: name, Source: dir, Size: info.Size(), MTime: info.ModTime().UnixNano()}
		if wantDigests {
			d, err := fileDigest(filepath.Join(dir, name))
			if err != nil {
				return fmt.Sprintf("%s: digest di %s fallito: %v", dir, name, err)
			}
			rf.Digest = d
		}
		res.Fonts = append(res.Fonts, rf)
		return ""
	}

	for name, info := range existing {
		if msg := add(name, outFontDir, info); msg != "" {
			res.Unknown = msg
			return res
		}
		if !wantDigests {
			continue
		}
		src, ok := sources[name]
		if !ok {
			// orfano: nessuna sorgente lo fornisce più, e il C non lo rimuove
			res.Stale = append(res.Stale, name)
			continue
		}
		// il digest dell'output è già stato calcolato in add(): niente doppia lettura
		var outDigest string
		for _, f := range res.Fonts {
			if f.Name == name {
				outDigest = f.Digest
				break
			}
		}
		srcDigest, err2 := fileDigest(filepath.Join(src.dir, name))
		if outDigest == "" || err2 != nil {
			res.Unknown = fmt.Sprintf("digest font %s fallito", name)
			return res
		}
		if outDigest != srcDigest || src.info.Size() != info.Size() {
			res.Stale = append(res.Stale, name)
		}
	}
	for name, src := range sources {
		if _, inOutput := existing[name]; inOutput {
			continue // già aggiunto dal contenuto dell'output
		}
		if msg := add(name, src.dir, src.info); msg != "" {
			res.Unknown = msg
			return res
		}
	}

	sort.Slice(res.Fonts, func(i, j int) bool { return res.Fonts[i].Name < res.Fonts[j].Name })
	sort.Strings(res.Stale)
	return res
}

// --- Identità ---------------------------------------------------------------

// renderIdentity è l'identità dell'ambiente corrente. Known=false ⇒ la mappa
// non è riutilizzabile e non va persistita come valida.
type renderIdentity struct {
	Known      bool
	Stamp      string // identità completa (con digest)
	EnvStamp   string // sottoinsieme di sola metadata (per il Watch)
	Reason     string // perché non è determinabile
	Engine     string
	EngineVer  string
	EngineOK   bool
	ToolDigest string
	Fonts      fontResolution
	FontsStale bool
}

// computeEnvStamp è la parte *economica* dell'identità: sole metadata, nessun
// digest e nessun probe. La usa il Watch per accorgersi che l'ambiente di
// rendering è cambiato senza pagare hashing nei poll.
func computeEnvStamp(engine, toolPath, vDir, outDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%d;layout=%d;", cacheSchema, layoutSchemaVersion)
	if rev, dirty, ok := buildRevision(); ok {
		fmt.Fprintf(&b, "gui=%s;dirty=%t;", rev, dirty)
		if dirty {
			// Build non committata: la revisione non distingue due alberi dirty,
			// quindi il binario stesso entra nei metadata (niente digest: 1 stat).
			if exe, err := os.Executable(); err == nil {
				b.WriteString("exe=" + fingerprintOrUnknown(exe) + ";")
			}
		}
	} else {
		b.WriteString("gui=unknown;")
	}
	b.WriteString("tool=" + fingerprintOrUnknown(toolPath) + ";")
	b.WriteString("engine=" + fingerprintOrUnknown(engine) + ";")
	b.WriteString("platform=" + platformID() + ";")
	fonts := resolveFonts(vDir, outDir, toolPath, false)
	if fonts.Unknown != "" {
		b.WriteString("fonts=unknown;")
	} else {
		for _, f := range fonts.Fonts {
			fmt.Fprintf(&b, "font=%s|%d|%d;", f.Name, f.Size, f.MTime)
		}
	}
	return shortHash(b.String())
}

func fingerprintOrUnknown(path string) string {
	if fp, _, ok := fileFingerprint(path); ok {
		return fp
	}
	return "unknown"
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// computeRenderIdentity calcola l'identità completa dell'ambiente corrente per
// il motore già selezionato (lo stesso che verrà usato per il rendering).
func computeRenderIdentity(engine, toolPath, vDir, outDir string) renderIdentity {
	id := renderIdentity{Engine: engine}
	// EnvStamp è di sola metadata e resta disponibile anche quando l'identità
	// completa non è determinabile: il Watch lo usa per accorgersi dei cambi.
	id.EnvStamp = computeEnvStamp(engine, toolPath, vDir, outDir)
	rev, dirty, revOK := buildRevision()
	if !revOK {
		id.Reason = "revisione VCS non disponibile nella build (identità GUI non determinabile)"
		return id
	}
	if dirty {
		id.Reason = "build con modifiche non committate (vcs.modified): identità non affidabile per il riuso"
		return id
	}
	toolFp, _, ok := fileFingerprint(toolPath)
	if !ok {
		id.Reason = "binario pubcraft non identificabile: " + toolPath
		return id
	}
	digest, err := fileDigest(toolPath)
	if err != nil {
		id.Reason = "digest del binario pubcraft fallito: " + err.Error()
		return id
	}
	id.ToolDigest = digest
	engineFp, _, ok := fileFingerprint(engine)
	if !ok {
		id.Reason = "motore PDF non identificabile: " + engine
		return id
	}
	var probeState rendererProbeState
	id.EngineVer, probeState = rendererVersion(engine)
	if probeState == probeStateFailed {
		// Probe eseguito ma errore/timeout: la versione non è determinabile e
		// l'identità non è affidabile per il riuso (fail-closed).
		id.Reason = "probe della versione del renderer fallito (errore o timeout): identità non determinabile"
		return id
	}
	id.EngineOK = probeState == probeStateOK
	fonts := resolveFonts(vDir, outDir, toolPath, true)
	if fonts.Unknown != "" {
		id.Reason = "font non identificabili: " + fonts.Unknown
		return id
	}
	id.Fonts = fonts
	id.FontsStale = len(fonts.Stale) > 0

	var b strings.Builder
	fmt.Fprintf(&b, "schema=%d;layout=%d;", cacheSchema, layoutSchemaVersion)
	fmt.Fprintf(&b, "gui=%s;dirty=false;", rev)
	fmt.Fprintf(&b, "tool=%s|%s;", toolFp, digest)
	verTag := "unavailable"
	if id.EngineOK {
		verTag = id.EngineVer
	}
	fmt.Fprintf(&b, "engine=%s|%s;", engineFp, verTag)
	fmt.Fprintf(&b, "platform=%s;", platformID())
	// I font contano per contenuto, non per provenienza: la copia in
	// outDir/fonts è identica alla sorgente, quindi prima e dopo la build la
	// firma coincide.
	for _, f := range fonts.Fonts {
		fmt.Fprintf(&b, "font=%s|%s;", f.Name, f.Digest)
	}
	id.Stamp = shortHash(b.String())
	id.EnvStamp = computeEnvStamp(engine, toolPath, vDir, outDir)
	id.Known = true
	return id
}

// --- Validità della mappa ---------------------------------------------------

// pageMapValid decide il riuso confrontando l'identità *persistita* con quella
// *corrente* (mai due valori letti dalla cache) e con sorgenti/impostazioni.
func pageMapValid(persisted printConfig, current renderIdentity, sourceStamp, trim string, bleed bool) bool {
	if !current.Known || current.FontsStale {
		return false
	}
	if persisted.CacheSchema != cacheSchema {
		return false
	}
	if persisted.RenderStamp == "" || persisted.RenderStamp != current.Stamp {
		return false
	}
	if persisted.PageMapStamp == "" || persisted.PageMapStamp != pageMapStampOf(printConfig{
		SourceStamp: sourceStamp, Trim: trim, Bleed: bleed,
	}) {
		return false
	}
	if persisted.SourceStamp != sourceStamp || persisted.Trim != trim || persisted.Bleed != bleed {
		return false
	}
	return len(persisted.PageMap) > 0
}

// copyTocPageMap restituisce una copia della mappa (mai condividere lo stato
// della cache con la pipeline).
func copyTocPageMap(m tocPageMap) tocPageMap {
	if len(m) == 0 {
		return nil
	}
	out := make(tocPageMap, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// fontsCoherentAfterBuild verifica, con digest ricalcolati *adesso*, che
// l'output contenga esattamente i font previsti prima della build. Serve a non
// persistere una mappa se il comportamento reale del C diverge dal modello.
func fontsCoherentAfterBuild(before, after fontResolution) bool {
	if before.Unknown != "" {
		return false
	}
	if after.Unknown != "" || len(after.Stale) > 0 {
		return false
	}
	if len(before.Fonts) != len(after.Fonts) {
		return false
	}
	for i := range before.Fonts {
		if before.Fonts[i].Name != after.Fonts[i].Name || before.Fonts[i].Digest != after.Fonts[i].Digest {
			return false
		}
	}
	return true
}
