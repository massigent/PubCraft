//go:build windows

package main

import (
	"syscall"
)

// Su Windows Process.Signal non supporta il segnale 0 e restituisce sempre
// errore: si interroga il kernel con OpenProcess, che non tocca il processo.
// La libreria standard non espone NewLazySystemDLL: kernel32.dll è una KnownDLL
// e LoadLibrary la risolve sempre dalla directory di sistema.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess  = kernel32.NewProc("OpenProcess")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	processQueryInfo = uintptr(0x1000) // PROCESS_QUERY_LIMITED_INFORMATION
)

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	// LazyProc.Call restituisce sempre un err non nil: discrimina l'esito
	// dall'handle (e dal codice errore), non dal solo err.
	h, _, callErr := procOpenProcess.Call(processQueryInfo, 0, uintptr(pid))
	if h != 0 {
		_, _, _ = procCloseHandle.Call(h)
		return true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.ERROR_ACCESS_DENIED {
		// Il processo esiste ma appartiene a un altro utente.
		return true
	}
	return false
}
