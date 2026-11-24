package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Settings belong to an artifact/project, not to the currently open browser tab.
// Keep them outside book.yaml to preserve user metadata and ad-hoc exports.
type printConfig struct {
	Trim        string `json:"trim"`
	Pages       int    `json:"pages"`
	Bleed       bool   `json:"bleed"`
	OutDir      string `json:"outdir"`
	SourceStamp string `json:"source_stamp,omitempty"`
	// Mappa pagina dell'indice (marcatore→pagina): se PageMapStamp coincide
	// con lo stamp corrente la passata di esplorazione viene saltata
	// (meccanismo pageCountMap di Vellum).
	PageMap      map[string]int `json:"pagemap,omitempty"`
	PageMapStamp string         `json:"pagemap_stamp,omitempty"`
	// Identità dell'ambiente di rendering con cui la mappa è stata calcolata
	// (C1). CacheSchema è esplicito e distinto dalla versione dell'app;
	// RenderStamp è l'identità completa (digest CLI/font + renderer + schema),
	// EnvStamp il sottoinsieme di sola metadata usato dal Watch. Cache legacy:
	// i campi mancano ⇒ la configurazione resta leggibile, la mappa no.
	CacheSchema int    `json:"cache_schema,omitempty"`
	RenderStamp string `json:"render_stamp,omitempty"`
	EnvStamp    string `json:"env_stamp,omitempty"`
}

func loadPrintConfig(vDir string, b *Book) printConfig {
	c := printConfig{Trim: "6x9", Pages: 200, OutDir: filepath.Join(vDir, "out-stampa")}
	if b != nil {
		if b.Trim != "" {
			c.Trim = b.Trim
		}
		if b.Pages > 0 {
			c.Pages = b.Pages
		}
		c.Bleed = b.Bleed
	}
	if data, err := os.ReadFile(filepath.Join(vDir, ".pubcraft-print.json")); err == nil {
		var saved printConfig
		if json.Unmarshal(data, &saved) == nil && saved.Trim != "" && saved.Pages > 0 && filepath.IsAbs(saved.OutDir) {
			c = saved
		}
	}
	return c
}

func apiPrintConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	vDir := getVaultDir()
	if vDir == "" {
		jsonErr(w, http.StatusBadRequest, "Apri prima una cartella libro")
		return
	}
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "Metadati del libro non validi")
		return
	}
	cfg := loadPrintConfig(vDir, b)
	writeJSON(w, map[string]any{
		"trim": cfg.Trim, "pages": cfg.Pages, "bleed": cfg.Bleed,
		"outdir": cfg.OutDir, "default_outdir": filepath.Join(vDir, "out-stampa"),
	})
}

func printName(b *Book) string {
	if b != nil && b.Title != "" {
		return slugify(b.Title)
	}
	return "libro"
}

// Include deletions/additions as well as mtimes. Generated artifacts must not
// invalidate themselves, including exports to a custom directory within the vault.
func printSourceStamp(vDir string, b *Book, c printConfig) (string, error) {
	h := sha256.New()
	root := filepath.Clean(vDir)
	out := filepath.Clean(c.OutDir)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || (info.IsDir() && (p == out || name == "build" || strings.HasPrefix(name, "out-stampa"))) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if filepath.Dir(p) == out && (name == "print.html" || name == "book.pdf" || name == printName(b)+".pdf") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func injectPrintStyles(html, vDir string, b *Book) {
	stripInjectedCSS(html)
	sb := ""
	if b != nil {
		sb = b.SceneBreak
	}
	if css, ok := sceneBreakCSSFor(sb); ok {
		injectCSS(html, "scenebreak", css)
	}
	if b == nil {
		return
	}
	if css, ok := fontCSSFor(b.Font); ok {
		injectCSS(html, "font-"+b.Font, css)
	}
	if b.Style == "custom" {
		if css, err := os.ReadFile(filepath.Join(vDir, "style.css")); err == nil {
			injectStyle(html, "custom", string(css)+"\n"+commonTypographyCSS)
		}
	} else if b.Style != "" && b.Style != "classico" {
		if css, ok := cssFor(b.Style); ok {
			injectStyle(html, b.Style, css)
		}
	}
}

// publishPrintPDF sposta il PDF candidato nel percorso definitivo. Se la
// pubblicazione fallisce il candidato viene comunque rimosso: nella cartella di
// esportazione non resta un temporaneo e il PDF precedente non viene toccato
// (os.Rename è atomico e fallisce senza effetti sul destinatario).
func publishPrintPDF(candidate, final string) error {
	if rerr := os.Rename(candidate, final); rerr != nil {
		_ = os.Remove(candidate)
		return fmt.Errorf("pubblicazione del PDF fallita: %w", rerr)
	}
	return nil
}

// renderPrintCore executes the common print rendering pipeline: running the CLI tool,
// injecting styles, running the headless PDF engine, and converging gutter bands based
// on the actual generated page count.
// It also runs the TOC page-number mechanism (pageCountMap style, see pagemap.go):
// when cachedMap is known the first pass already carries the numbers; otherwise a
// probe pass (hidden PCMAP markers) is followed by a numbered rewrite.
// allowProbe=false keeps the cost at the plain single-render pipeline (used by the
// publication preview): numbers are applied only when a cached map already exists,
// because a probe pass would roughly double the rendering time. Se durante la
// convergenza del gutter la banda cambia, la mappa in cache non è più
// affidabile: l'anteprima rende l'indice senza numeri (nessuna sonda, nessuna
// passata numerata) e non restituisce la vecchia mappa.
// It is used by both the export/Watch pipeline and the ephemeral publication preview pipeline.
func renderPrintCore(vDir string, b *Book, trim string, pages int, bleed bool, outDir, engine string, cachedMap tocPageMap, allowProbe bool) (html, pdf, output string, finalPages int, pageMap tocPageMap, err error) {
	if toolPath == "" {
		return "", "", "", 0, nil, fmt.Errorf("pubcraft non trovato")
	}
	// Il motore arriva dal chiamante: è lo stesso percorso usato per calcolare
	// l'identità. Nessuna seconda selezione qui (altrimenti identità e
	// rendering potrebbero riferirsi a motori diversi).
	if engine == "" {
		return "", "", "", 0, nil, fmt.Errorf("nessun motore PDF disponibile")
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return
	}
	if err = os.MkdirAll(outDir, 0700); err != nil {
		return
	}
	html = filepath.Join(outDir, "print.html")
	pdf = filepath.Join(outDir, printName(b)+".pdf")
	// Il render scrive su un candidato: il PDF definitivo viene sostituito solo
	// dopo la verifica geometrica, così un motore che produce uno snapshot non
	// impaginato non sovrascrive un artefatto valido precedente.
	candidatePdf := pdf + ".candidate.pdf"
	defer func() {
		if err == nil {
			err = publishPrintPDF(candidatePdf, pdf)
			return
		}
		_ = os.Remove(candidatePdf)
	}()
	browser := engine
	// numeri di pagina dell'indice: default attivo (config `toc.pagenumbers`)
	wantNums := b == nil || b.TOC == nil || b.TOC.PageNumbers == nil || *b.TOC.PageNumbers
	// senza cache e senza possibilita' di sonda (anteprima) i numeri non si
	// possono conoscere: meglio un indice pulito senza numeri che una doppia
	// renderizzazione o una colonna vuota con marcatori nascosti.
	if !allowProbe && len(cachedMap) == 0 {
		wantNums = false
	}
	used := cachedMap
	mapFile := ""
	writeMapFile := func(m tocPageMap) (string, error) {
		data, jerr := json.Marshal(m)
		if jerr != nil {
			return "", jerr
		}
		p := filepath.Join(outDir, ".pubcraft-pagemap.json")
		return p, os.WriteFile(p, data, 0600)
	}
	if wantNums && len(used) > 0 {
		mapFile, err = writeMapFile(used)
		if err != nil {
			return
		}
	}
	initialPages := pages
	// numbering governa la colonna dei numeri della prossima passata. In
	// anteprima (allowProbe=false) può essere disattivato senza sonda se la
	// banda del gutter cambia: la riconvergenza serve comunque e diventa
	// l'occasione per rendere l'indice senza numeri.
	numbering := wantNums
	converge := func(start int) (int, string, error) {
		var outAll string
		finalPages := start
		if finalPages <= 0 {
			finalPages = 200
		}
		for pass := 0; pass < 7; pass++ {
			args := []string{"build-print", vDir, outDir, "--trim", trim, "--pages", fmt.Sprint(finalPages)}
			if bleed {
				args = append(args, "--bleed")
			}
			if mapFile != "" {
				args = append(args, "--toc-pagemap", mapFile)
			} else if !numbering {
				// indice senza numeri: nessuna colonna vuota, nessun marcatore
				args = append(args, "--no-toc-pagenumbers")
			}
			var out string
			out, cerr := runTool(exec.Command(toolPath, args...))
			outAll += out + "\n"
			if cerr != nil {
				return 0, outAll, cerr
			}
			injectPrintStyles(html, vDir, b)
			if cerr = htmlToPDF(browser, html, candidatePdf); cerr != nil {
				return 0, outAll, cerr
			}
			var actual int
			actual, cerr = validatePDFStructure(candidatePdf)
			if cerr != nil {
				return 0, outAll, cerr
			}
			// Guardia geometrica: trim e abbondanza sono noti qui, dove si
			// conosce anche il motore. Un PDF con pagine fuori formato non
			// diventa un artefatto e non produce cache dell'indice.
			if cerr = checkPrintPageGeometry(candidatePdf, html, engineNameFromPath(browser), trim, bleed); cerr != nil {
				return 0, outAll, cerr
			}
			sameBand := kdpGutter(actual) == kdpGutter(finalPages)
			finalPages = actual
			if sameBand {
				return actual, outAll, nil
			}
			if !allowProbe && used != nil && kdpGutter(actual) != kdpGutter(initialPages) {
				// Anteprima: la banda del gutter è cambiata, quindi la mappa in
				// cache non è più affidabile. La passata di riconvergenza (che
				// serve comunque) diventa senza numeri: nessuna sonda PCMAP e
				// nessuna passata numerata in più.
				mapFile = ""
				numbering = false
			}
			outAll += fmt.Sprintf("Ricalcolo gutter su %d pagine effettive (%.3fin)\n", actual, kdpGutter(actual))
		}
		return 0, outAll, fmt.Errorf("il gutter non converge: esportazione interrotta")
	}
	finalPages, output, err = converge(initialPages)
	if err != nil {
		return
	}
	bandShifted := used != nil && kdpGutter(finalPages) != kdpGutter(initialPages)
	if !wantNums {
		return html, pdf, output, finalPages, nil, nil
	}
	if !allowProbe {
		// L'anteprima non sonda mai. Se la banda del gutter è cambiata i numeri
		// in cache non sono più affidabili: la mappa non va restituita come
		// valida (e non va scritta come se lo fosse). La riconvergenza dentro
		// converge() ha già reso l'indice senza numeri.
		if bandShifted {
			output += "Anteprima: banda gutter cambiata; mappa indice in cache non riutilizzata, indice senza numeri di pagina\n"
			return html, pdf, output, finalPages, nil, nil
		}
		pageMap = used
		return html, pdf, output, finalPages, pageMap, nil
	}
	if used == nil || bandShifted {
		// Numerazione indice (meccanismo pageCountMap di Vellum), solo per
		// esportazione/Watch (allowProbe=true):
		// - con mappa in cache validata (stesso stamp e stessa banda gutter) i
		//   numeri della prima passata sono già corretti: nessuna passata extra.
		// - altrimenti la passata di esplorazione (marcatori PCMAP) fornisce la
		//   mappa reale e una passata numerata la applica.
		if bandShifted {
			// banda cambiata: i numeri in cache non sono più affidabili,
			// ripeti l'esplorazione dal PDF senza numeri
			mapFile = ""
			var extra string
			var cerr error
			finalPages, extra, cerr = converge(finalPages)
			output += extra
			if cerr != nil {
				err = cerr
				return
			}
		}
		fresh, xerr := extractTocPageMap(candidatePdf)
		if xerr != nil {
			output += fmt.Sprintf("avviso: estrazione mappa indice fallita (%v); indice senza numeri di pagina\n", xerr)
			return html, pdf, output, finalPages, nil, nil
		}
		if len(fresh) == 0 {
			output += "avviso: nessun marcatore indice trovato nel PDF; indice senza numeri di pagina\n"
			return html, pdf, output, finalPages, nil, nil
		}
		pageMap = fresh
		mapFile, err = writeMapFile(fresh)
		if err != nil {
			return
		}
		output += "Aggiornamento numeri di pagina dell'indice...\n"
		var extra string
		var cerr error
		// si riparte dal conteggio appena misurato: la passata numerata non
		// paga un altro giro di convergenza del gutter (l'indice a celle fisse
		// non cambia la paginazione)
		finalPages, extra, cerr = converge(finalPages)
		output += extra
		if cerr != nil {
			err = cerr
			return
		}
		return html, pdf, output, finalPages, pageMap, nil
	}
	// cache valida: i numeri erano già nella prima passata
	pageMap = used
	return html, pdf, output, finalPages, pageMap, nil
}

// mapsEqual confronta due mappe pagina (stesse chiavi e stessi valori).
func mapsEqual(a, b tocPageMap) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// Caller owns buildMu. GUI and Watch use this identical pipeline, including
// bounded gutter convergence, error propagation, and persistent artifact settings.
//
// C1: la mappa si riusa solo con schema supportato, identità dell'ambiente
// corrente determinabile *e* coincidente con quella persistita, sorgenti e
// impostazioni compatibili. La configurazione viene salvata comunque (a build
// riuscita); la mappa solo se l'identità è affidabile e i font osservati dopo
// la build coincidono con quelli previsti.
func buildPrint(vDir string, b *Book, c printConfig) (html, pdf, output string, err error) {
	if toolPath == "" {
		return "", "", "", fmt.Errorf("pubcraft non trovato")
	}
	// Persistenza dell'uuid PRIMA della revisione/stamp: evita warning spuri e
	// garantisce un identificatore stabile anche al primo export.
	if err = ensureBookUUID(vDir, b); err != nil {
		return "", "", "", err
	}
	c.OutDir, err = filepath.Abs(c.OutDir)
	if err != nil {
		return
	}
	// Una sola selezione del motore per generazione: lo stesso percorso serve
	// per l'identità, per il rendering e per la verifica successiva.
	engine := selectPDFBrowser()
	if engine == "" {
		return "", "", "", fmt.Errorf("nessun motore PDF disponibile")
	}
	c.SourceStamp, err = printSourceStamp(vDir, b, c)
	if err != nil {
		return
	}
	// Identità dell'ambiente corrente, ricalcolata adesso e confrontata con
	// quella persistita (mai due valori letti dalla cache).
	identity := computeRenderIdentity(engine, toolPath, vDir, c.OutDir)
	var mapToUse tocPageMap
	if pageMapValid(c, identity, c.SourceStamp, c.Trim, c.Bleed) {
		mapToUse = copyTocPageMap(c.PageMap)
	}

	var finalPages int
	var pageMap tocPageMap
	html, pdf, output, finalPages, pageMap, err = renderPrintCore(vDir, b, c.Trim, c.Pages, c.Bleed, c.OutDir, engine, mapToUse, true)
	if err != nil {
		// Pipeline fallita: PDF precedente e .pubcraft-print.json restano
		// intatti (qui non si scrive nulla) e il candidato è già stato rimosso.
		return
	}
	setLastPrintHTML(vDir, html)
	c.Pages = finalPages

	// Verifica finale con digest ricalcolati DOPO la build: <outdir>/fonts può
	// contenere font preesistenti che il C non sovrascrive, quindi i soli
	// vincitori delle sorgenti non descrivono necessariamente il rendering.
	after := computeRenderIdentity(engine, toolPath, vDir, c.OutDir)
	c.CacheSchema = cacheSchema
	// Il Watch confronta EnvStamp con l'ambiente osservato *dopo* la build.
	// I font vengono copiati in <outdir>/fonts senza preservare l'mtime, quindi
	// lo stamp pre-build non coinciderebbe più e ogni build esplicita sarebbe
	// seguita da una rigenerazione superflua. Persisti il valore post-build.
	c.EnvStamp = after.EnvStamp
	c.RenderStamp = ""
	if identity.Known && after.Known && fontsCoherentAfterBuild(identity.Fonts, after.Fonts) && !identity.FontsStale && !after.FontsStale {
		c.RenderStamp = identity.Stamp
	}
	if len(pageMap) > 0 && c.RenderStamp != "" {
		c.PageMap = pageMap
		// lo stamp della mappa include anche le impostazioni che spostano
		// la paginazione (trim e bleed)
		c.PageMapStamp = pageMapStampOf(c)
	} else {
		c.PageMap = nil
		c.PageMapStamp = ""
	}
	if len(pageMap) > 0 && c.PageMap == nil {
		output += "Cache indice non salvata: " + cacheSkipReason(identity, after) + "\n"
	}
	var data []byte
	data, err = json.MarshalIndent(c, "", "  ")
	if err == nil {
		err = atomicWriteFile(filepath.Join(vDir, ".pubcraft-print.json"), data, 0600)
	}
	if err == nil {
		output += fmt.Sprintf("PDF generato: %s (%d pagine)\n", pdf, finalPages)
	}
	return
}

// cacheSkipReason spiega in output perché una mappa non è stata resa
// riutilizzabile (diagnosi, nessuna azione automatica sui font).
func cacheSkipReason(id, after renderIdentity) string {
	switch {
	case !id.Known:
		return id.Reason
	case id.FontsStale:
		return "font obsoleti in <outdir>/fonts (il renderer li usa e non vengono rimossi automaticamente); pulisci la cartella di esportazione per riabilitare la cache"
	case !after.Known:
		return "identità non più determinabile dopo la build: " + after.Reason
	case after.FontsStale:
		return "font obsoleti in <outdir>/fonts dopo la build"
	default:
		return "i font risolti dopo la build non coincidono con quelli previsti"
	}
}

func triggerPrintBuild(vDir string, b *Book) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	_, _, _, err := buildPrint(vDir, b, loadPrintConfig(vDir, b))
	return err
}

// cachedPageMap restituisce la mappa persistita solo se lo stamp coincide:
// altrimenti la passata di esplorazione ridefinira' la mappa.
// pageMapStampOf compone lo stamp sorgente con trim/bleed: cambia uno solo
// e la mappa pagine in cache va ricalcolata.
func pageMapStampOf(c printConfig) string {
	return c.SourceStamp + "|" + c.Trim + "|" + strconv.FormatBool(c.Bleed)
}

// previewPageMap restituisce la mappa persistita solo se è valida anche per
// l'ambiente dell'anteprima: stesso motore selezionato dal chiamante, sorgenti
// correnti, trim/bleed richiesti e font che l'anteprima userà davvero
// (l'anteprima renderizza in una cartella nuova, quindi senza i font già
// presenti in <outdir>/fonts dell'export).
// L'anteprima è in sola lettura: qui non si scrive nulla e non si sonda; se la
// mappa non è provata valida l'indice resta senza numeri.
func previewPageMap(vDir string, b *Book, trim string, bleed bool, engine, artDir string) tocPageMap {
	if engine == "" {
		return nil
	}
	c := loadPrintConfig(vDir, b)
	c.Trim = trim
	c.Bleed = bleed
	if len(c.PageMap) == 0 || c.PageMapStamp == "" {
		return nil
	}
	stamp, err := printSourceStamp(vDir, b, c)
	if err != nil {
		return nil
	}
	c.SourceStamp = stamp
	identity := computeRenderIdentity(engine, toolPath, vDir, artDir)
	if !pageMapValid(c, identity, stamp, trim, bleed) {
		return nil
	}
	return copyTocPageMap(c.PageMap)
}

func apiWatchPDF(w http.ResponseWriter, r *http.Request) {
	vDir := getVaultDir()
	if vDir == "" {
		http.Error(w, "Nessun vault aperto", 404)
		return
	}
	b, err := parseBookYAML(filepath.Join(vDir, "book.yaml"))
	if err != nil {
		http.Error(w, "Metadati libro non disponibili", 404)
		return
	}
	c := loadPrintConfig(vDir, b)
	pdf := filepath.Join(c.OutDir, printName(b)+".pdf")
	stamp, err := printSourceStamp(vDir, b, c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Punto in cui il Watch rileva il cambio di ambiente di rendering: confronto
	// di sola metadata (nessun digest, nessun probe) fra EnvStamp persistito e
	// quello corrente. Motore aggiornato, CLI ricompilata o font diversi
	// obbligano a rigenerare anche se le sorgenti non sono cambiate.
	envChanged := false
	if engine := selectPDFBrowser(); engine != "" {
		envChanged = c.EnvStamp == "" || c.EnvStamp != computeEnvStamp(engine, toolPath, vDir, c.OutDir)
	}
	if !fileExists(pdf) || c.SourceStamp != stamp || envChanged || r.URL.Query().Get("rebuild") == "1" {
		if err := triggerPrintBuild(vDir, b); err != nil {
			http.Error(w, "Errore rigenerazione PDF: "+err.Error(), 500)
			return
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	http.ServeFile(w, r, pdf)
}
