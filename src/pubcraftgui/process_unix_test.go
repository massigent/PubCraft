//go:build !windows

package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcessExists(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatalf("processExists(getpid) = false; atteso true")
	}
	if processExists(0) || processExists(-1) {
		t.Errorf("processExists deve restituire false per pid non validi")
	}
	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("binario 'true' non disponibile")
	}
	cmd := exec.Command(trueBin)
	if err := cmd.Run(); err != nil {
		t.Fatalf("esecuzione del processo figlio fallita: %v", err)
	}
	if processExists(cmd.Process.Pid) {
		t.Errorf("processExists(%d) = true per un processo già terminato", cmd.Process.Pid)
	}
}
