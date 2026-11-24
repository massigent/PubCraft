package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVaultFingerprintInvalidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pubcraft-fp-test-*")
	if err != nil {
		t.Fatalf("errore creazione tmpDir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	f1 := filepath.Join(tmpDir, "01.md")
	f2 := filepath.Join(tmpDir, "02.md")

	if err := os.WriteFile(f1, []byte("Capitolo 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("Capitolo 2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Imposta date fisse: f1 più vecchio (ieri), f2 più recente (oggi)
	t0 := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	tMax := time.Now().Truncate(time.Second)
	_ = os.Chtimes(f1, t0, t0)
	_ = os.Chtimes(f2, tMax, tMax)

	fpInitial := computeVaultFingerprint(tmpDir)
	if fpInitial.FileCount != 2 {
		t.Fatalf("attesi 2 file, trovati %d", fpInitial.FileCount)
	}
	if !fpInitial.MaxModTime.Equal(tMax) {
		t.Fatalf("MaxModTime errato: %v vs %v", fpInitial.MaxModTime, tMax)
	}
	if len(fpInitial.Hash) != 16 {
		t.Fatalf("attesa stringa esadecimale a 16 caratteri, ottenuta: %q", fpInitial.Hash)
	}

	// CASO 1: Modifica di un file più vecchio (f1) con modtime T1 < tMax.
	// Il massimo mtime resta invariato (tMax), ma il fingerprint composito DEVE cambiare.
	t1 := t0.Add(2 * time.Hour) // t0 < t1 < tMax
	_ = os.WriteFile(f1, []byte("Capitolo 1 modificato"), 0644)
	_ = os.Chtimes(f1, t1, t1)

	fpAfterOlderMod := computeVaultFingerprint(tmpDir)
	if !fpAfterOlderMod.MaxModTime.Equal(tMax) {
		t.Errorf("MaxModTime non doveva cambiare: atteso %v, ottenuto %v", tMax, fpAfterOlderMod.MaxModTime)
	}
	if fpAfterOlderMod.Hash == fpInitial.Hash {
		t.Errorf("Fingerprint hash DEVE cambiare su modifica di un file più vecchio anche se MaxModTime è invariato")
	}

	// CASO 2: Cancellazione di un file.
	_ = os.Remove(f1)
	fpAfterDelete := computeVaultFingerprint(tmpDir)
	if fpAfterDelete.FileCount != 1 {
		t.Errorf("atteso 1 file dopo cancellazione, trovati %d", fpAfterDelete.FileCount)
	}
	if fpAfterDelete.Hash == fpAfterOlderMod.Hash || fpAfterDelete.Hash == fpInitial.Hash {
		t.Errorf("Fingerprint hash DEVE cambiare dopo cancellazione di un file")
	}

	// CASO 3: Aggiunta file nuovo.
	f3 := filepath.Join(tmpDir, "03.md")
	_ = os.WriteFile(f3, []byte("Capitolo 3"), 0644)
	fpAfterAdd := computeVaultFingerprint(tmpDir)
	if fpAfterAdd.FileCount != 2 {
		t.Errorf("attesi 2 file dopo aggiunta, trovati %d", fpAfterAdd.FileCount)
	}
	if fpAfterAdd.Hash == fpAfterDelete.Hash {
		t.Errorf("Fingerprint hash DEVE cambiare dopo aggiunta di un file")
	}
}

type cParsedBook struct {
	Title      string
	Author     string
	Language   string
	UUID       string
	Style      string
	Opening    string
	Font       string
	SceneBreak string
	Cover      string
	Trim       string
	Pages      int
	Bleed      string
	TocSubs    bool
	TocPages   bool
	TocSeen    bool
	Chapters   []Chapter
}

func parseCInfoOutput(out string) cParsedBook {
	var res cParsedBook
	scanner := bufio.NewScanner(strings.NewReader(out))
	inChapters := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "capitoli:") {
			inChapters = true
			continue
		}
		if !inChapters {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				v := strings.TrimSpace(parts[1])
				switch k {
				case "titolo":
					res.Title = v
				case "autore":
					res.Author = v
				case "lingua":
					res.Language = v
				case "uuid":
					res.UUID = v
				case "stile":
					res.Style = v
				case "apertura":
					res.Opening = v
				case "font":
					res.Font = v
				case "scenebreak":
					res.SceneBreak = v
				case "cover":
					res.Cover = v
				case "trim":
					res.Trim = v
				case "pages":
					res.Pages, _ = strconv.Atoi(v)
				case "bleed":
					res.Bleed = v
				case "toc":
					// "subheads=true pagenumbers=false"
					res.TocSeen = true
					for _, f := range strings.Fields(v) {
						kv := strings.SplitN(f, "=", 2)
						if len(kv) != 2 {
							continue
						}
						switch kv[0] {
						case "subheads":
							res.TocSubs = kv[1] == "true"
						case "pagenumbers":
							res.TocPages = kv[1] == "true"
						}
					}
				}
			}
		} else {
			// Riga capitolo: "  1. file=01.md | id=ch-1 | title=First Chapter | type=chapter"
			if !strings.Contains(line, "file=") {
				continue
			}
			parts := strings.Split(line, "|")
			var ch Chapter
			for _, p := range parts {
				kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
				if len(kv) == 2 {
					val := strings.TrimSpace(kv[1])
					switch {
					case strings.HasSuffix(kv[0], "file"):
						ch.File = val
					case kv[0] == "id":
						ch.ID = val
					case kv[0] == "title":
						ch.Title = val
					case kv[0] == "type":
						ch.Type = val
					}
				}
			}
			if ch.File != "" {
				res.Chapters = append(res.Chapters, ch)
			}
		}
	}
	return res
}

func TestYAMLParityGoAndC(t *testing.T) {
	cBin := "../../build/pubcraft"
	if _, err := os.Stat(cBin); err != nil {
		t.Skipf("binario C non trovato in %s: compila prima con make", cBin)
	}

	fixturesDir := "../../tests/fixtures/yaml"
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("impossibile leggere cartella fixtures: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fixtureName := entry.Name()
		fixturePath := filepath.Join(fixturesDir, fixtureName)
		bookYAMLPath := filepath.Join(fixturePath, "book.yaml")

		t.Run(fixtureName, func(t *testing.T) {
			// 1. Parsing lato Go
			data, err := os.ReadFile(bookYAMLPath)
			if err != nil {
				t.Fatalf("errore lettura book.yaml: %v", err)
			}
			goBook, err := decodeBookYAML(data)
			if err != nil {
				t.Fatalf("Go decodeBookYAML fallito: %v", err)
			}

			// 2. Parsing lato C tramite `pubcraft info <fixturePath>`
			cmd := exec.Command(cBin, "info", fixturePath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C pubcraft info fallito (err=%v): %s", err, string(out))
			}
			cBook := parseCInfoOutput(string(out))

			// 3. Confronto metadati primari
			if goBook.Title != cBook.Title {
				t.Errorf("[%s] Discrepanza Title: Go=%q, C=%q", fixtureName, goBook.Title, cBook.Title)
			}
			if goBook.Author != cBook.Author {
				t.Errorf("[%s] Discrepanza Author: Go=%q, C=%q", fixtureName, goBook.Author, cBook.Author)
			}
			if goBook.Language != cBook.Language {
				t.Errorf("[%s] Discrepanza Language: Go=%q, C=%q", fixtureName, goBook.Language, cBook.Language)
			}
			if goBook.Style != "" && goBook.Style != cBook.Style {
				t.Errorf("[%s] Discrepanza Style: Go=%q, C=%q", fixtureName, goBook.Style, cBook.Style)
			}
			if goBook.Opening != "" && goBook.Opening != cBook.Opening {
				t.Errorf("[%s] Discrepanza Opening: Go=%q, C=%q", fixtureName, goBook.Opening, cBook.Opening)
			}
			if goBook.Font != "" && goBook.Font != cBook.Font {
				t.Errorf("[%s] Discrepanza Font: Go=%q, C=%q", fixtureName, goBook.Font, cBook.Font)
			}
			if goBook.SceneBreak != "" && goBook.SceneBreak != cBook.SceneBreak {
				t.Errorf("[%s] Discrepanza SceneBreak: Go=%q, C=%q", fixtureName, goBook.SceneBreak, cBook.SceneBreak)
			}
			if goBook.Cover != "" && goBook.Cover != cBook.Cover {
				t.Errorf("[%s] Discrepanza Cover: Go=%q, C=%q", fixtureName, goBook.Cover, cBook.Cover)
			}
			if goBook.Trim != "" && goBook.Trim != cBook.Trim {
				t.Errorf("[%s] Discrepanza Trim: Go=%q, C=%q", fixtureName, goBook.Trim, cBook.Trim)
			}
			if goBook.Pages > 0 && goBook.Pages != cBook.Pages {
				t.Errorf("[%s] Discrepanza Pages: Go=%d, C=%d", fixtureName, goBook.Pages, cBook.Pages)
			}

			// Config indice (book.yaml `toc:`): il C deve applicare gli stessi
			// default del Go (subheads=false, pagenumbers=true), anche quando il
			// blocco toc: e' scritto dopo chapters:.
			if !cBook.TocSeen {
				t.Errorf("[%s] pubcraft info non riporta la riga toc:", fixtureName)
			} else {
				goSubs, goTocPages := false, true
				if goBook.TOC != nil {
					if goBook.TOC.Subheads != nil {
						goSubs = *goBook.TOC.Subheads
					}
					if goBook.TOC.PageNumbers != nil {
						goTocPages = *goBook.TOC.PageNumbers
					}
				}
				if goSubs != cBook.TocSubs || goTocPages != cBook.TocPages {
					t.Errorf("[%s] Discrepanza toc: Go={subheads:%t pagenumbers:%t} C={subheads:%t pagenumbers:%t}",
						fixtureName, goSubs, goTocPages, cBook.TocSubs, cBook.TocPages)
				}
			}

			// 4. Confronto capitoli
			if len(goBook.Chapters) != len(cBook.Chapters) {
				t.Fatalf("[%s] Numero capitoli diverso: Go=%d, C=%d", fixtureName, len(goBook.Chapters), len(cBook.Chapters))
			}
			for i := range goBook.Chapters {
				gCh := goBook.Chapters[i]
				cCh := cBook.Chapters[i]
				if gCh.File != cCh.File {
					t.Errorf("[%s][Cap %d] Discrepanza File: Go=%q, C=%q", fixtureName, i, gCh.File, cCh.File)
				}
				if gCh.Title != "" && gCh.Title != cCh.Title {
					t.Errorf("[%s][Cap %d] Discrepanza Title: Go=%q, C=%q", fixtureName, i, gCh.Title, cCh.Title)
				}
				if gCh.ID != "" && gCh.ID != cCh.ID {
					t.Errorf("[%s][Cap %d] Discrepanza ID: Go=%q, C=%q", fixtureName, i, gCh.ID, cCh.ID)
				}
				if gCh.Type != "" && gCh.Type != cCh.Type {
					t.Errorf("[%s][Cap %d] Discrepanza Type: Go=%q, C=%q", fixtureName, i, gCh.Type, cCh.Type)
				}
			}
		})
	}
}

func TestInstanceCacheShutdownCleanup(t *testing.T) {
	// 1. Verifica che cleanAllPreviews rimuova la directory dell'istanza
	cacheDir := previewCacheBaseDir()
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(cacheDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("ephemeral"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("file temporaneo non creato: %v", err)
	}

	cleanAllPreviews()

	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Errorf("cleanAllPreviews non ha rimosso la directory di cache dell'istanza: %s", cacheDir)
	}

	// 2. Verifica che cleanOrphanedPreviewDirs rimuova cartelle di PID defunti ma preservi PID attivi
	base := filepath.Join(os.TempDir(), "pubcraft-previews")
	_ = os.MkdirAll(base, 0700)

	// Cartella orfana con PID inesistente (es. 99999999)
	deadPIDDir := filepath.Join(base, "instance-99999999-12345")
	_ = os.MkdirAll(deadPIDDir, 0700)
	_ = os.WriteFile(filepath.Join(deadPIDDir, "dead.txt"), []byte("dead"), 0600)

	// Cartella con PID attivo del processo di test corrente
	myPIDDir := filepath.Join(base, "instance-"+strconv.Itoa(os.Getpid())+"-67890")
	_ = os.MkdirAll(myPIDDir, 0700)
	defer os.RemoveAll(myPIDDir)

	cleanOrphanedPreviewDirs()

	if _, err := os.Stat(deadPIDDir); !os.IsNotExist(err) {
		t.Errorf("cleanOrphanedPreviewDirs avrebbe dovuto rimuovere la cartella del PID defunto %s", deadPIDDir)
	}
	if _, err := os.Stat(myPIDDir); err != nil {
		t.Errorf("cleanOrphanedPreviewDirs NON deve rimuovere la cartella di un PID attivo: %v", err)
	}
}

// TestTocBooleanParityGoAndC verifica che GUI e CLI accettino e interpretino
// allo stesso modo i valori booleani di `toc:` in book.yaml: true/false,
// yes/no, on/off, 1/0 (maiuscole indifferenti), valori vuoti/`~`/`null` come
// "non impostato" (default) e valori non validi come errore in entrambe.
func TestTocBooleanParityGoAndC(t *testing.T) {
	cBin := "../../build/pubcraft"
	if _, err := os.Stat(cBin); err != nil {
		t.Skipf("binario C non trovato in %s: compila prima con make", cBin)
	}
	cases := []struct {
		name      string
		toc       string
		wantSubs  string
		wantPages string
		wantErr   bool
	}{
		{"on-off", "toc:\n  subheads: on\n  pagenumbers: off\n", "true", "false", false},
		{"yes-no", "toc:\n  subheads: yes\n  pagenumbers: no\n", "true", "false", false},
		{"uppercase", "toc:\n  subheads: TRUE\n  pagenumbers: FALSE\n", "true", "false", false},
		{"numeric", "toc:\n  subheads: 1\n  pagenumbers: 0\n", "true", "false", false},
		{"quoted", "toc:\n  subheads: \"no\"\n  pagenumbers: \"yes\"\n", "false", "true", false},
		{"null values", "toc:\n  subheads: ~\n  pagenumbers: null\n", "false", "true", false},
		{"empty values", "toc:\n  subheads:\n  pagenumbers:\n", "false", "true", false},
		{"unknown key ignored", "toc:\n  subheads: yes\n  depth: 3\n", "true", "true", false},
		{"invalid subheads", "toc:\n  subheads: forse\n", "", "", true},
		{"invalid pagenumbers", "toc:\n  pagenumbers: 2\n", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			yaml := "title: Parity\nauthor: A\nlanguage: it\nchapters:\n- file: 01.md\n  id: c1\n  title: Uno\n" + tc.toc
			if err := os.WriteFile(filepath.Join(dir, "book.yaml"), []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "01.md"), []byte("Testo.\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Lato Go (GUI)
			goBook, goErr := decodeBookYAML([]byte(yaml))
			goSubs, goPages := false, true
			if goErr == nil && goBook.TOC != nil {
				if goBook.TOC.Subheads != nil {
					goSubs = *goBook.TOC.Subheads
				}
				if goBook.TOC.PageNumbers != nil {
					goPages = *goBook.TOC.PageNumbers
				}
			}

			// Lato C (CLI)
			out, cErr := exec.Command(cBin, "info", dir).CombinedOutput()
			lines := strings.Split(string(out), "\n")

			if tc.wantErr {
				if goErr == nil {
					t.Fatalf("Go ha accettato un valore non valido (%s)", tc.toc)
				}
				if cErr == nil {
					t.Fatalf("CLI ha accettato un valore non valido (%s):\n%s", tc.toc, out)
				}
				return
			}
			if goErr != nil {
				t.Fatalf("Go ha rifiutato un valore valido: %v", goErr)
			}
			if cErr != nil {
				t.Fatalf("CLI ha rifiutato un valore valido: %v\n%s", cErr, out)
			}
			cSubs, cPages := "", ""
			for _, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), "toc:") {
					for _, f := range strings.Fields(strings.TrimSpace(l))[1:] {
						kv := strings.SplitN(f, "=", 2)
						if len(kv) != 2 {
							continue
						}
						if kv[0] == "subheads" {
							cSubs = kv[1]
						}
						if kv[0] == "pagenumbers" {
							cPages = kv[1]
						}
					}
				}
			}
			gotSubs := fmt.Sprintf("%t", goSubs)
			gotPages := fmt.Sprintf("%t", goPages)
			if gotSubs != cSubs || gotPages != cPages {
				t.Fatalf("divergenza Go/C: Go={subheads:%s pagenumbers:%s} C={subheads:%s pagenumbers:%s}",
					gotSubs, gotPages, cSubs, cPages)
			}
			if gotSubs != tc.wantSubs || gotPages != tc.wantPages {
				t.Fatalf("atteso {subheads:%s pagenumbers:%s}, ottenuto Go={subheads:%s pagenumbers:%s}",
					tc.wantSubs, tc.wantPages, gotSubs, gotPages)
			}
		})
	}
}
