package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// La matrice attesa è la specifica editoriale: la tabella TOC_POLICY in
// src/pubcraft.c deve produrre esattamente questo, senza cambiare i TOC reali.
var tocPolicyFixture = filepath.Join("..", "..", "tests", "fixtures", "toc_policy")

// Titoli delle sezioni incluse nell'indice, per formato.
var wantPrintToc = []string{"Prefazione", "Prologo", "Capitolo", "Epilogo", "Ringraziamenti", "Sull'Autore", "Tipo Ignoto"}
var wantEbookToc = []string{"Copyright", "Colophon", "Dedica", "Epigrafe", "Prefazione", "Prologo",
	"Capitolo", "Epilogo", "Ringraziamenti", "Sull'Autore", "Tipo Ignoto"}

func cliBin(t *testing.T) string {
	t.Helper()
	bin := "../../build/pubcraft"
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binario C non trovato in %s: compila prima con make", bin)
	}
	return bin
}

func runCLI(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v fallito: %v\n%s", bin, args, err, out)
	}
	return string(out)
}

// entriesFromHTML estrae le voci dell'indice di un documento HTML/XHTML
// nell'ordine in cui compaiono: coppie (href, testo).
var htmlTocEntryRe = regexp.MustCompile(`<a href="(#[^"]+|chap[0-9]+\.xhtml(?:#[^"]*)?)">(.*?)</a>`)

func tocEntriesFromHTML(html string) (hrefs, titles []string) {
	// Limita l'analisi al blocco dell'indice per non catturare altri link.
	start := strings.Index(html, `class="toc"`)
	if start < 0 {
		start = strings.Index(html, `epub:type="toc"`)
	}
	if start < 0 {
		return nil, nil
	}
	block := html[start:]
	if end := strings.Index(block, `epub:type="landmarks"`); end > 0 {
		block = block[:end]
	} else if end := strings.Index(block, "</nav>"); end > 0 {
		block = block[:end]
	} else if end := strings.Index(block, `class="chapter"`); end > 0 {
		block = block[:end]
	}
	for _, m := range htmlTocEntryRe.FindAllStringSubmatch(block, -1) {
		hrefs = append(hrefs, m[1])
		titles = append(titles, htmlTextToPlain(m[2]))
	}
	return hrefs, titles
}

// htmlTextToPlain rimuove i tag inline lasciando il testo visibile.
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func htmlTextToPlain(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`)
	return strings.TrimSpace(r.Replace(s))
}

var ncxTextRe = regexp.MustCompile(`<navLabel><text>(.*?)</text></navLabel>`)

func tocEntriesFromNCX(ncx string) []string {
	var out []string
	for _, m := range ncxTextRe.FindAllStringSubmatch(ncx, -1) {
		r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`)
		out = append(out, strings.TrimSpace(r.Replace(m[1])))
	}
	return out
}

// TestTocPolicyMatrix confronta la politica dichiarata (TOC_POLICY in C) con i
// TOC realmente generati: stampa, nav EPUB e toc.ncx. nav e NCX devono
// coincidere perché condividono TOC_FMT_EBOOK.
//
// Copertura end-to-end parziale: il formato HTML non è incluso qui perché
// `export-html` accetta solo archivi e i capitoli importati dagli archivi non
// portano ancora il tipo di sezione. La colonna HTML della tabella è verificata
// internamente da tests/check_core.c (funzione check_toc_policy), sul codice
// C con i tipi tipizzati. La conservazione dei tipi nell'importazione è un
// intervento separato.
func TestTocPolicyMatrix(t *testing.T) {
	bin := cliBin(t)
	outDir := t.TempDir()

	runCLI(t, bin, "build-print", tocPolicyFixture, filepath.Join(outDir, "print"), "--trim", "6x9", "--pages", "100")
	epub := filepath.Join(outDir, "book.epub")
	runCLI(t, bin, "build", tocPolicyFixture, epub)

	printHTML, err := os.ReadFile(filepath.Join(outDir, "print", "print.html"))
	if err != nil {
		t.Fatal(err)
	}
	_, printTitles := tocEntriesFromHTML(string(printHTML))

	navHTML := unzipEntry(t, epub, "OEBPS/nav.xhtml")
	navHrefs, navTitles := tocEntriesFromHTML(navHTML)
	ncx := unzipEntry(t, epub, "OEBPS/toc.ncx")
	ncxTitles := tocEntriesFromNCX(ncx)

	// livelli 0 = titoli dei capitoli; i sottocapi seguono con "Sezione/Nota …"
	isSubhead := func(s string) bool { return strings.HasPrefix(s, "Sezione ") || strings.HasPrefix(s, "Nota ") }

	var printCh, navCh []string
	for _, s := range printTitles {
		if !isSubhead(s) {
			printCh = append(printCh, s)
		}
	}
	for _, s := range navTitles {
		if !isSubhead(s) {
			navCh = append(navCh, s)
		}
	}

	if strings.Join(printCh, "|") != strings.Join(wantPrintToc, "|") {
		t.Errorf("indice di stampa inatteso:\n  ottenuto: %s\n  atteso:   %s",
			strings.Join(printCh, "|"), strings.Join(wantPrintToc, "|"))
	}
	if strings.Join(navCh, "|") != strings.Join(wantEbookToc, "|") {
		t.Errorf("indice nav EPUB inatteso:\n  ottenuto: %s\n  atteso:   %s",
			strings.Join(navCh, "|"), strings.Join(wantEbookToc, "|"))
	}

	// nav e NCX devono contenere le stesse voci, nello stesso ordine
	if strings.Join(navTitles, "|") != strings.Join(ncxTitles, "|") {
		t.Errorf("nav e NCX divergono:\n  nav: %s\n  ncx: %s",
			strings.Join(navTitles, "|"), strings.Join(ncxTitles, "|"))
	}

	// ogni capitolo incluso porta con sé i propri sottocapi; il copyright
	// mantiene il suo sottocapo nel corpo e nell'indice ebook (non nascosto)
	if !strings.Contains(string(printHTML), "<h3 id=\"ch1-s1\">Nota copyright</h3>") {
		t.Error("il sottocapo del copyright non deve essere nascosto nel corpo di stampa")
	}
	if !contains(navTitles, "Nota copyright") {
		t.Error("il sottocapo del copyright deve restare nell'indice ebook")
	}
	// i sottocapi dei capitoli esclusi dalla stampa non devono comparire
	for _, s := range []string{"Nota copyright", "Nota colophon", "Nota dedica", "Nota epigrafe"} {
		if contains(printTitles, s) {
			t.Errorf("sottocapo %q di una sezione esclusa non deve comparire nell'indice di stampa", s)
		}
	}
	// i sottocapi dei capitoli inclusi devono esserci in tutti i formati
	for _, s := range []string{"Sezione prefazione", "Sezione capitolo", "Sezione ignota"} {
		if !contains(printTitles, s) || !contains(navTitles, s) || !contains(ncxTitles, s) {
			t.Errorf("sottocapo %q mancante in uno dei formati (print=%t nav=%t ncx=%t)",
				s, contains(printTitles, s), contains(navTitles, s), contains(ncxTitles, s))
		}
	}
	// l'alias colophon segue la politica di copyright: fuori dalla stampa
	if contains(printCh, "Colophon") {
		t.Error("il colophon segue copyright: non deve comparire nell'indice di stampa")
	}
	// coerenza dei link nav con i capitoli dichiarati
	if len(navHrefs) != len(navTitles) {
		t.Errorf("nav: %d link per %d voci", len(navHrefs), len(navTitles))
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// unzipEntry legge un file da un EPUB usando unzip di sistema (nessuna
// dipendenza nuova).
func unzipEntry(t *testing.T, zipPath, entry string) string {
	t.Helper()
	out, err := exec.Command("unzip", "-p", zipPath, entry).Output()
	if err != nil {
		t.Fatalf("unzip %s %s: %v", zipPath, entry, err)
	}
	return string(out)
}

// TestSupportedTypesParity confronta il vocabolario dei tipi usato dalla CLI
// (generato da TOC_POLICY e stampato nell'avviso) con quello del GUI
// (knownBookTypes in booktypes.go): due tabelle manuali non devono divergere.
func TestSupportedTypesParity(t *testing.T) {
	bin := cliBin(t)
	dir := t.TempDir()
	yaml := "title: Tipi\nauthor: A\nlanguage: it\nchapters:\n" +
		"- file: 01.md\n  id: c1\n  title: Uno\n  type: parties\n"
	if err := os.WriteFile(filepath.Join(dir, "book.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01.md"), []byte("Testo.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "info", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("pubcraft info fallito: %v\n%s", err, out)
	}
	marker := "Tipi supportati: "
	idx := strings.Index(string(out), marker)
	if idx < 0 {
		t.Fatalf("l'avviso non elenca i tipi supportati:\n%s", out)
	}
	list := string(out)[idx+len(marker):]
	if end := strings.Index(list, "."); end >= 0 {
		list = list[:end]
	}
	cliTypes := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		name := strings.TrimSpace(part)
		if i := strings.Index(name, " ("); i > 0 {
			name = strings.TrimSpace(name[:i])
		}
		if name != "" {
			cliTypes[name] = true
		}
	}
	// la CLI elenca i tipi canonici; colophon è un alias accettato
	cliTypes["colophon"] = true
	want := map[string]bool{}
	for k := range knownBookTypes {
		want[k] = true
	}
	want["colophon"] = true
	for k := range want {
		if !cliTypes[k] {
			t.Errorf("tipo %q noto alla GUI ma non elencato dalla CLI", k)
		}
	}
	for k := range cliTypes {
		if !want[k] {
			t.Errorf("tipo %q elencato dalla CLI ma non noto alla GUI", k)
		}
	}
}
