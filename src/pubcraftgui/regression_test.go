package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestChapterVersionsReopenAndConflict(t *testing.T) {
	d := t.TempDir()
	setVault(d)
	p := filepath.Join(d, "01.md")
	os.WriteFile(p, []byte("initial"), 0600)
	read := func() string {
		w := httptest.NewRecorder()
		apiChapter(w, httptest.NewRequest("GET", "/api/chapter?file=01.md", nil))
		var r map[string]string
		json.Unmarshal(w.Body.Bytes(), &r)
		return r["version"]
	}
	save := func(base, text string) int {
		body, _ := json.Marshal(map[string]string{"file": "01.md", "vault": d, "base_version": base, "content": text})
		w := httptest.NewRecorder()
		apiSave(w, httptest.NewRequest("POST", "/api/save", bytes.NewReader(body)))
		return w.Code
	}
	initial := read()
	if initial == "" {
		t.Fatal("missing server version")
	}
	if save(initial, "one") != 200 || save(read(), "two") != 200 {
		t.Fatal("save failed")
	}
	// A fresh read, as on chapter reopen or page reload, needs no local counter.
	if save(read(), "after reopen") != 200 {
		t.Fatal("reopened chapter cannot be saved")
	}
	if save(initial, "obsolete") != 409 {
		t.Fatal("obsolete contents overwrote current file")
	}
	base := read()
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, text := range []string{"tab A", "tab B"} {
		wg.Add(1)
		go func(s string) { defer wg.Done(); codes <- save(base, s) }(text)
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		if c == 200 {
			ok++
		}
		if c == 409 {
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("CAS results success=%d conflict=%d", ok, conflict)
	}
}

func TestNewProjectRejectsPathTraversalInFolder(t *testing.T) {
	parent := t.TempDir()
	setVault("")
	for _, folder := range []string{"../escaped", `..\\escaped`, ".", ".."} {
		body, _ := json.Marshal(map[string]string{"parentDir": parent, "folder": folder, "title": "Synthetic"})
		w := httptest.NewRecorder()
		apiNewProject(w, httptest.NewRequest("POST", "/api/newproject", bytes.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("folder %q status=%d body=%s", folder, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "escaped")); !os.IsNotExist(err) {
		t.Fatal("path traversal created a directory outside selected parent")
	}
}

func TestAPIPrintConfigReflectsSavedExportSettings(t *testing.T) {
	v := t.TempDir()
	setVault(v)
	if err := os.WriteFile(filepath.Join(v, "book.yaml"), []byte("title: Print settings\nchapters:\n- file: 01.md\n  title: One\n"), 0600); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "export with #")
	cfg := printConfig{Trim: "5x8", Pages: 123, Bleed: true, OutDir: custom}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v, ".pubcraft-print.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	apiPrintConfig(w, httptest.NewRequest("GET", "/api/printconfig", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["trim"] != "5x8" || got["pages"] != float64(123) || got["bleed"] != true || got["outdir"] != custom || got["default_outdir"] != filepath.Join(v, "out-stampa") {
		t.Fatalf("unexpected print configuration: %#v", got)
	}
	w = httptest.NewRecorder()
	apiPrintConfig(w, httptest.NewRequest("POST", "/api/printconfig", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", w.Code)
	}
}

func TestPrintSettingsExternalPreviewAndWatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	v := filepath.Join(root, "vault")
	os.Mkdir(v, 0755)
	setVault(v)
	yaml := "title: Regression\ntrim: 6x9\nchapters:\n- file: 01.md\n  title: One\n"
	os.WriteFile(filepath.Join(v, "book.yaml"), []byte(yaml), 0600)
	os.WriteFile(filepath.Join(v, "01.md"), []byte("original"), 0600)
	oldTool := toolPath
	t.Cleanup(func() { toolPath = oldTool })
	log := filepath.Join(root, "args.log")
	toolPath = filepath.Join(root, "tool")
	os.WriteFile(toolPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nmkdir -p \"$3\"\nprintf '<html><head></head><body>regression</body></html>' > \"$3/print.html\"\n"), 0700)
	fixture := filepath.Join(root, "fixture.pdf")
	os.WriteFile(fixture, generateTestPDF(1), 0600)
	browser := filepath.Join(root, "renderer")
	os.WriteFile(browser, []byte("#!/bin/sh\nfor a in \"$@\";do case \"$a\" in --print-to-pdf=*) cp '"+fixture+"' \"${a#--print-to-pdf=}\" ;; esac;done\n"), 0700)
	t.Setenv("CHROMIUM_BIN", browser)
	outside := filepath.Join(root, "export")
	body, _ := json.Marshal(map[string]any{"kind": "print", "trim": "5x8", "bleed": true, "outdir": outside})
	w := httptest.NewRecorder()
	apiBuild(w, httptest.NewRequest("POST", "/api/build", bytes.NewReader(body)))
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["ok"] != true {
		t.Fatalf("build failed: %s", w.Body.String())
	}
	cfg := loadPrintConfig(v, nil)
	if cfg.Trim != "5x8" || !cfg.Bleed || cfg.OutDir != outside {
		t.Fatalf("settings lost: %+v", cfg)
	}
	setLastPrintHTML(v, "") // Reload/restart: recover the registered external output.
	preview := httptest.NewRecorder()
	apiViewPrint(preview, httptest.NewRequest("GET", "/api/viewprint/", nil))
	if preview.Code != 200 {
		t.Fatal("external preview rejected", preview.Code)
	}
	before, _ := os.ReadFile(log)
	watch := httptest.NewRecorder()
	apiWatchPDF(watch, httptest.NewRequest("GET", "/api/watch/pdf", nil))
	after, _ := os.ReadFile(log)
	if watch.Code != 200 || !bytes.Equal(before, after) {
		t.Fatal("unchanged Watch rebuilt itself", watch.Code)
	}
	os.WriteFile(filepath.Join(v, "01.md"), []byte("modified content"), 0600)
	watch = httptest.NewRecorder()
	apiWatchPDF(watch, httptest.NewRequest("GET", "/api/watch/pdf", nil))
	after, _ = os.ReadFile(log)
	if watch.Code != 200 || bytes.Equal(before, after) {
		t.Fatal("Watch failed to rebuild")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(after)), "\n") {
		if !strings.Contains(line, "--trim 5x8") || !strings.Contains(line, "--bleed") {
			t.Fatalf("format drift: %s", line)
		}
	}
	got, _ := os.ReadFile(filepath.Join(v, "book.yaml"))
	if string(got) != yaml {
		t.Fatal("export modified user YAML")
	}
	b := t.TempDir()
	setVault(b)
	preview = httptest.NewRecorder()
	apiViewPrint(preview, httptest.NewRequest("GET", "/api/viewprint/", nil))
	if preview.Code != 404 {
		t.Fatal("cross-project preview leak")
	}
}

func compressedPDFFixture(predictor, badOffset bool) []byte {
	zip := func(b []byte) []byte {
		var out bytes.Buffer
		z := zlib.NewWriter(&out)
		z.Write(b)
		z.Close()
		return out.Bytes()
	}
	objects := []string{"<< /Type /Catalog /Pages 3 0 R >>", "<< /Type /Pages /Kids [4 0 R] /Count 1 >>", "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 432 648] >>"}
	header := fmt.Sprintf("2 0 3 %d 4 %d ", len(objects[0])+1, len(objects[0])+len(objects[1])+2)
	stream := zip([]byte(header + strings.Join(objects, "\n")))
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.5\n")
	offset := pdf.Len()
	fmt.Fprintf(&pdf, "1 0 obj\n<< /Type /ObjStm /N 3 /First %d /Length %d /Filter /FlateDecode >>\nstream\n", len(header), len(stream))
	pdf.Write(stream)
	pdf.WriteString("\nendstream\nendobj\n")
	xrefOffset := pdf.Len()
	if badOffset {
		offset = 999999
	}
	entries := [][3]uint32{{0, 0, 65535}, {1, uint32(offset), 0}, {2, 1, 0}, {2, 1, 1}, {2, 1, 2}, {1, uint32(xrefOffset), 0}}
	var raw []byte
	prev := make([]byte, 7)
	for _, e := range entries {
		row := make([]byte, 7)
		row[0] = byte(e[0])
		binary.BigEndian.PutUint32(row[1:5], e[1])
		binary.BigEndian.PutUint16(row[5:7], uint16(e[2]))
		if predictor {
			raw = append(raw, 2)
			for i, x := range row {
				raw = append(raw, x-prev[i])
			}
			prev = row
		} else {
			raw = append(raw, row...)
		}
	}
	xref := zip(raw)
	dp := ""
	if predictor {
		dp = " /DecodeParms << /Predictor 12 /Columns 7 /Colors 1 /BitsPerComponent 8 >>"
	}
	fmt.Fprintf(&pdf, "5 0 obj\n<< /Type /XRef /Size 6 /Root 2 0 R /W [1 4 2] /Length %d /Filter /FlateDecode%s >>\nstream\n", len(xref), dp)
	pdf.Write(xref)
	fmt.Fprintf(&pdf, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xrefOffset)
	return pdf.Bytes()
}

func TestPDFParserRegressionFixtures(t *testing.T) {
	for _, pred := range []bool{false, true} {
		n, e := ParsePDFData(compressedPDFFixture(pred, false))
		if e != nil || n != 1 {
			t.Fatalf("predictor=%v n=%d err=%v", pred, n, e)
		}
	}
	if _, e := ParsePDFData(compressedPDFFixture(false, true)); e == nil {
		t.Fatal("out-of-range ObjStm offset accepted")
	}
	for _, bad := range [][]byte{
		bytes.Replace(generateTestPDF(1), []byte("/Count 1"), []byte("/Count 7"), 1),
		bytes.Replace(generateTestPDF(1), []byte("/Kids [3 0 R]"), []byte("/Kids [2 0 R]"), 1),
	} {
		if _, e := ParsePDFData(bad); e == nil {
			t.Fatal("invalid page tree accepted")
		}
	}
}

func FuzzParsePDFData(f *testing.F) {
	f.Add(generateTestPDF(1))
	f.Add(compressedPDFFixture(true, false))
	f.Add(compressedPDFFixture(false, true))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		ParsePDFData(data)
	})
}
