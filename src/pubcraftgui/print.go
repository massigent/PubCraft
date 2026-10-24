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

// renderPrintCore executes the common print rendering pipeline: running the CLI tool,
// injecting styles, running the headless PDF engine, and converging gutter bands based
// on the actual generated page count.
// It is used by both the export/Watch pipeline and the ephemeral publication preview pipeline.
func renderPrintCore(vDir string, b *Book, trim string, pages int, bleed bool, outDir string) (html, pdf, output string, finalPages int, err error) {
	if toolPath == "" {
		return "", "", "", 0, fmt.Errorf("pubcraft non trovato")
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
	browser := findPDFBrowser()
	if browser == "" {
		err = fmt.Errorf("nessun motore PDF disponibile")
		return
	}
	finalPages = pages
	if finalPages <= 0 {
		finalPages = 200
	}
	for pass := 0; pass < 7; pass++ {
		args := []string{"build-print", vDir, outDir, "--trim", trim, "--pages", fmt.Sprint(finalPages)}
		if bleed {
			args = append(args, "--bleed")
		}
		var out string
		out, err = runTool(exec.Command(toolPath, args...))
		output += out + "\n"
		if err != nil {
			return
		}
		injectPrintStyles(html, vDir, b)
		if err = htmlToPDF(browser, html, pdf); err != nil {
			return
		}
		var actual int
		actual, err = validatePDFStructure(pdf)
		if err != nil {
			return
		}
		sameBand := kdpGutter(actual) == kdpGutter(finalPages)
		finalPages = actual
		if sameBand {
			return html, pdf, output, finalPages, nil
		}
		output += fmt.Sprintf("Ricalcolo gutter su %d pagine effettive (%.3fin)\n", actual, kdpGutter(actual))
	}
	err = fmt.Errorf("il gutter non converge: esportazione interrotta")
	return
}

// Caller owns buildMu. GUI and Watch use this identical pipeline, including
// bounded gutter convergence, error propagation, and persistent artifact settings.
func buildPrint(vDir string, b *Book, c printConfig) (html, pdf, output string, err error) {
	if toolPath == "" {
		return "", "", "", fmt.Errorf("pubcraft non trovato")
	}
	c.OutDir, err = filepath.Abs(c.OutDir)
	if err != nil {
		return
	}
	c.SourceStamp, err = printSourceStamp(vDir, b, c)
	if err != nil {
		return
	}
	var finalPages int
	html, pdf, output, finalPages, err = renderPrintCore(vDir, b, c.Trim, c.Pages, c.Bleed, c.OutDir)
	if err != nil {
		return
	}
	setLastPrintHTML(vDir, html)
	c.Pages = finalPages
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

func triggerPrintBuild(vDir string, b *Book) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	_, _, _, err := buildPrint(vDir, b, loadPrintConfig(vDir, b))
	return err
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
	if !fileExists(pdf) || c.SourceStamp != stamp || r.URL.Query().Get("rebuild") == "1" {
		if err := triggerPrintBuild(vDir, b); err != nil {
			http.Error(w, "Errore rigenerazione PDF: "+err.Error(), 500)
			return
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	http.ServeFile(w, r, pdf)
}
