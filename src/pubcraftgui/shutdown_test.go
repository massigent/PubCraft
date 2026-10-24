package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGracefulShutdownWithRealSIGTERM(t *testing.T) {
	// Costruisce un eseguibile temporaneo per testare il subprocess reale
	tmpDir, err := os.MkdirTemp("", "pubcraft-shutdown-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp fallito: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "pubcraftgui-test")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build fallito: %v\n%s", err, string(out))
	}

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"PUBCRAFTGUI_LISTEN=127.0.0.1:0",
		"PUBCRAFTGUI_NO_BROWSER=1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start fallito: %v", err)
	}
	pid := cmd.Process.Pid

	serverURLChan := make(chan string, 1)
	urlRe := regexp.MustCompile(`(http://127\.0\.0\.1:\d+)`)

	scanOutput := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if m := urlRe.FindString(line); m != "" {
				select {
				case serverURLChan <- m:
				default:
				}
			}
		}
	}
	go scanOutput(stdout)
	go scanOutput(stderr)

	var serverURL string
	select {
	case serverURL = <-serverURLChan:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("timeout in attesa dell'avvio del server")
	}

	// Verifica che il server risponda ed esegua richieste attive
	resp, err := http.Get(serverURL + "/api/state")
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("richiesta HTTP fallita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = cmd.Process.Kill()
		t.Fatalf("status HTTP inatteso: %d", resp.StatusCode)
	}

	// Verifica che la cartella della cache dell'istanza esista in /tmp/pubcraft-previews/
	previewBase := filepath.Join(os.TempDir(), "pubcraft-previews")
	var instanceDir string
	for i := 0; i < 20; i++ {
		entries, _ := os.ReadDir(previewBase)
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), fmt.Sprintf("instance-%d-", pid)) {
				instanceDir = filepath.Join(previewBase, e.Name())
				break
			}
		}
		if instanceDir != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Creiamo anche una cartella di test per simulare un'altra istanza attiva concomitante
	otherInstanceDir := filepath.Join(previewBase, fmt.Sprintf("instance-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.MkdirAll(otherInstanceDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(otherInstanceDir)

	// Invia SIGTERM reale al subprocess
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("invio SIGTERM fallito: %v", err)
	}

	// Attendi completamento controllato tramite Shutdown
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case waitErr := <-done:
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				// ExitCode 0 indica uscita pulita tramite Shutdown
				if exitErr.ExitCode() != 0 {
					t.Errorf("Processo terminato con codice diverso da 0: %v", exitErr.ExitCode())
				}
			} else {
				t.Errorf("Errore Wait inatteso: %v", waitErr)
			}
		}
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("processo non terminato entro il timeout dopo SIGTERM (blocco di Shutdown)")
	}

	// Verifica che la cache della SOLA istanza terminata sia stata cancellata
	if instanceDir != "" {
		if _, err := os.Stat(instanceDir); !os.IsNotExist(err) {
			t.Errorf("la cache dell'istanza terminata (%s) non è stata rimossa dopo Shutdown", instanceDir)
		}
	}

	// Verifica che la cache dell'altra istanza attiva sia rimasta intatta
	if _, err := os.Stat(otherInstanceDir); os.IsNotExist(err) {
		t.Errorf("la cache dell'altra istanza attiva (%s) è stata erroneamente rimossa!", otherInstanceDir)
	}
}
