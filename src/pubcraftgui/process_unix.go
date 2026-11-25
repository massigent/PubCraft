//go:build !windows

package main

import (
	"os"
	"syscall"
)

// processExists verifica se il processo esiste inviando il segnale 0
// (nessun effetto: controlla solo esistenza e permessi).
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
