package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DiagEvent rappresenta un evento diagnostico registrato nel sistema.
type DiagEvent struct {
	ErrorID       string    `json:"error_id"`
	Timestamp     time.Time `json:"timestamp"`
	Severity      string    `json:"severity"`  // "error", "warning", "info"
	Component     string    `json:"component"` // "save", "export", "connection", "mcp", "ui", "import", "build"
	Code          string    `json:"code"`      // es. "SAVE_FAILED", "EXPORT_FAILED", "CONNECTION_ERROR"
	Operation     string    `json:"operation"` // es. "apiSave", "buildPrint", "apiOpenAt"
	RequestID     string    `json:"request_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Message       string    `json:"message"`
	Details       string    `json:"details,omitempty"`
	Count         int       `json:"count"`
}

// BugReport rappresenta una segnalazione strutturata pronta per essere esaminata ed esportata.
type BugReport struct {
	ReportID        string      `json:"report_id"`
	CreatedAt       time.Time   `json:"created_at"`
	AppVersion      string      `json:"app_version"`
	OSArch          string      `json:"os_arch"`
	GoVersion       string      `json:"go_version"`
	Description     string      `json:"description"`
	Expected        string      `json:"expected,omitempty"`
	Observed        string      `json:"observed,omitempty"`
	SelectedEvents  []DiagEvent `json:"events,omitempty"`
	MarkdownContent string      `json:"markdown_content"`
}

type diagRegistry struct {
	mu            sync.RWMutex
	sessionStart  time.Time
	events        []*DiagEvent
	maxEvents     int
	filePath      string
	reports       map[string]*BugReport
	sessionEvents int
}

var globalDiag = newDiagRegistry()

func newDiagRegistry() *diagRegistry {
	home, _ := os.UserHomeDir()
	pDir := filepath.Join(home, ".pubcraft")
	_ = os.MkdirAll(pDir, 0700)
	fPath := filepath.Join(pDir, "diagnostics.json")

	r := &diagRegistry{
		sessionStart: time.Now().UTC(),
		events:       make([]*DiagEvent, 0, 100),
		maxEvents:    1000,
		filePath:     fPath,
		reports:      make(map[string]*BugReport),
	}
	r.loadFromDisk()
	return r
}

func (r *diagRegistry) loadFromDisk() {
	if r.filePath == "" || !fileExists(r.filePath) {
		return
	}
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return
	}
	var loaded []*DiagEvent
	if err := json.Unmarshal(data, &loaded); err == nil && len(loaded) > 0 {
		// Mantieni solo gli eventi degli ultimi 7 giorni
		cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
		var fresh []*DiagEvent
		for _, ev := range loaded {
			if ev != nil && ev.Timestamp.After(cutoff) {
				fresh = append(fresh, ev)
			}
		}
		if len(fresh) > r.maxEvents {
			fresh = fresh[len(fresh)-r.maxEvents:]
		}
		r.events = fresh
	}
}

func (r *diagRegistry) saveToDiskLocked() {
	if r.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(r.events, "", "  ")
	if err != nil {
		return
	}
	_ = atomicWriteFile(r.filePath, data, 0600)
}

func generateDiagID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UTC().Unix(), hex.EncodeToString(b))
}

// sanitizeDiagText rimuove percorsi utente assoluti e credenziali sensibili
func sanitizeDiagText(s string) string {
	if s == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	reUsers := regexp.MustCompile(`(/Users/[a-zA-Z0-9_\.\-]+|/home/[a-zA-Z0-9_\.\-]+|[a-zA-Z]:\\Users\\[a-zA-Z0-9_\.\-]+)`)
	s = reUsers.ReplaceAllString(s, "~")

	// Maschera Bearer token
	reBearer := regexp.MustCompile(`(?i)\b(Bearer)\s+[^\s"'>]+`)
	s = reBearer.ReplaceAllString(s, "$1 [REDACTED]")

	// Maschera Basic auth
	reBasic := regexp.MustCompile(`(?i)\b(Basic)\s+[A-Za-z0-9+/=]+`)
	s = reBasic.ReplaceAllString(s, "$1 [REDACTED]")

	// Maschera coppie chiave-valore sensibili
	reKV := regexp.MustCompile(`(?i)\b(password|passwd|api_key|apikey|secret|token|auth|access_token|refresh_token)\s*([=:])\s*([^\s&"'>]+)`)
	s = reKV.ReplaceAllString(s, "$1$2[REDACTED]")

	return s
}

// RecordDiagEvent registra un nuovo evento diagnostico o ne incrementa il contatore se duplicato.
func RecordDiagEvent(severity, component, code, operation, message, details, reqID, correlationID string) *DiagEvent {
	reg := globalDiag
	reg.mu.Lock()
	defer reg.mu.Unlock()

	message = sanitizeDiagText(message)
	details = sanitizeDiagText(details)

	// Deduplicazione: se negli ultimi 10 eventi ne esiste uno con stesso componente, codice, op e messaggio
	limit := len(reg.events)
	start := 0
	if limit > 10 {
		start = limit - 10
	}
	for i := limit - 1; i >= start; i-- {
		ev := reg.events[i]
		if ev.Component == component && ev.Code == code && ev.Operation == operation && ev.Message == message {
			ev.Count++
			ev.Timestamp = time.Now().UTC()
			if details != "" && ev.Details == "" {
				ev.Details = details
			}
			reg.saveToDiskLocked()
			return ev
		}
	}

	ev := &DiagEvent{
		ErrorID:       generateDiagID("err"),
		Timestamp:     time.Now().UTC(),
		Severity:      severity,
		Component:     component,
		Code:          code,
		Operation:     operation,
		RequestID:     reqID,
		CorrelationID: correlationID,
		Message:       message,
		Details:       details,
		Count:         1,
	}

	reg.events = append(reg.events, ev)
	if len(reg.events) > reg.maxEvents {
		reg.events = reg.events[len(reg.events)-reg.maxEvents:]
	}
	reg.sessionEvents++
	reg.saveToDiskLocked()
	return ev
}

// RecordDiagError registra un errore con severità "error".
func RecordDiagError(component, code, operation, message, details, reqID string) *DiagEvent {
	return RecordDiagEvent("error", component, code, operation, message, details, reqID, "")
}

// GetRecentDiagEvents restituisce gli eventi recenti filtrati opzionalmente per componente.
// Restituisce copie per valore create sotto lock per evitare data race durante la serializzazione JSON concorrente.
func GetRecentDiagEvents(limit int, component string) []DiagEvent {
	reg := globalDiag
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var res []DiagEvent
	for i := len(reg.events) - 1; i >= 0 && len(res) < limit; i-- {
		ev := reg.events[i]
		if ev == nil {
			continue
		}
		if component == "" || strings.EqualFold(ev.Component, component) {
			res = append(res, *ev)
		}
	}
	return res
}

// GetDiagStatusSummary fornisce il riassunto sintetico alla connessione.
func GetDiagStatusSummary() map[string]interface{} {
	reg := globalDiag
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	var recentIDs []string
	currentSessionCount := 0
	previousSessionCount := 0

	for i := len(reg.events) - 1; i >= 0; i-- {
		ev := reg.events[i]
		if ev.Severity == "error" {
			if len(recentIDs) < 5 {
				recentIDs = append(recentIDs, ev.ErrorID)
			}
			if ev.Timestamp.After(reg.sessionStart) {
				currentSessionCount++
			} else {
				previousSessionCount++
			}
		}
	}

	return map[string]interface{}{
		"recent_errors_count":     currentSessionCount + previousSessionCount,
		"current_session_errors":  currentSessionCount,
		"previous_session_errors": previousSessionCount,
		"recent_error_ids":        recentIDs,
		"session_start_utc":       reg.sessionStart.Format(time.RFC3339),
	}
}

// PrepareBugReport genera una bozza di segnalazione strutturata senza inviare nulla.
func PrepareBugReport(errorIDs []string, description, expected, observed string) (*BugReport, error) {
	reg := globalDiag
	reg.mu.Lock()
	defer reg.mu.Unlock()

	reportID := generateDiagID("rep")
	now := time.Now().UTC()

	var selectedEvents []DiagEvent
	idMap := make(map[string]bool)
	for _, id := range errorIDs {
		idMap[strings.TrimSpace(id)] = true
	}

	for _, ev := range reg.events {
		if ev == nil {
			continue
		}
		if idMap[ev.ErrorID] || (len(errorIDs) == 0 && ev.Severity == "error" && ev.Timestamp.After(now.Add(-2*time.Hour))) {
			selectedEvents = append(selectedEvents, *ev)
		}
	}

	// Costruisci il Markdown del rapporto (100% pulito, nessun testo del libro o credenziali)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Segnalazione PubCraft — %s\n\n", reportID))
	sb.WriteString(fmt.Sprintf("- **Data e ora UTC:** %s\n", now.Format(time.RFC3339)))
	sb.WriteString("- **Versione PubCraft:** 1.0.0-alpha.1\n")
	sb.WriteString(fmt.Sprintf("- **Ambiente:** %s / %s (runtime: %s)\n\n", runtime.GOOS, runtime.GOARCH, runtime.Version()))

	sb.WriteString("## 1. Descrizione del problema\n")
	desc := strings.TrimSpace(description)
	if desc == "" {
		desc = "(Nessuna descrizione specificata)"
	}
	sb.WriteString(sanitizeDiagText(desc) + "\n\n")

	if strings.TrimSpace(expected) != "" {
		sb.WriteString("### Risultato atteso\n")
		sb.WriteString(sanitizeDiagText(strings.TrimSpace(expected)) + "\n\n")
	}

	if strings.TrimSpace(observed) != "" {
		sb.WriteString("### Risultato osservato\n")
		sb.WriteString(sanitizeDiagText(strings.TrimSpace(observed)) + "\n\n")
	}

	sb.WriteString("## 2. Eventi diagnostici correlati\n")
	if len(selectedEvents) == 0 {
		sb.WriteString("*Nessun errore automatico registrato in correlazione a questa segnalazione.*\n\n")
	} else {
		sb.WriteString("| ID Errore | Data/Ora UTC | Componente | Codice | Operazione | Messaggio |\n")
		sb.WriteString("|---|---|---|---|---|---|\n")
		for _, ev := range selectedEvents {
			sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | `%s` | `%s` | %s |\n",
				ev.ErrorID,
				ev.Timestamp.Format("15:04:05"),
				ev.Component,
				ev.Code,
				ev.Operation,
				ev.Message,
			))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 3. Note di riservatezza e conformità\n")
	sb.WriteString("- Nessun contenuto di manoscritto, testo di capitoli, file PDF o screenshot è incluso automaticamente nel rapporto.\n")
	sb.WriteString("- I percorsi del filesystem sono stati normalizzati senza nomi utente personali.\n")
	sb.WriteString("- Questo rapporto è generato localmente per il controllo dell'utente prima dell'eventuale condivisione con lo sviluppatore.\n")

	rep := &BugReport{
		ReportID:        reportID,
		CreatedAt:       now,
		AppVersion:      "1.0.0-alpha.1",
		OSArch:          fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:       runtime.Version(),
		Description:     sanitizeDiagText(description),
		Expected:        sanitizeDiagText(expected),
		Observed:        sanitizeDiagText(observed),
		SelectedEvents:  selectedEvents,
		MarkdownContent: sb.String(),
	}

	reg.reports[reportID] = rep
	return rep, nil
}

// ExportBugReport scrive il rapporto su file locale esplicito con protezione contro sovrascritture accidentali.
func ExportBugReport(reportID, destPath string, overwrite bool) (string, error) {
	reg := globalDiag
	reg.mu.RLock()
	rep, ok := reg.reports[reportID]
	reg.mu.RUnlock()

	if !ok || rep == nil {
		return "", fmt.Errorf("rapporto '%s' non trovato", reportID)
	}

	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		home, _ := os.UserHomeDir()
		destPath = filepath.Join(home, fmt.Sprintf("pubcraft-report-%s.md", reportID))
	}
	absDest, err := filepath.Abs(destPath)
	if err != nil {
		return "", err
	}

	// Protezione: non consentire esportazione all'interno del vault del manoscritto
	vDir := getVaultDir()
	if vDir != "" {
		absVault, _ := filepath.Abs(vDir)
		rel, err := filepath.Rel(absVault, absDest)
		if err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return "", fmt.Errorf("impossibile esportare il rapporto all'interno della cartella del manoscritto: scegliere un percorso esterno")
		}
	}

	// Protezione sovrascrittura senza consenso esplicito
	if fileExists(absDest) && !overwrite {
		return "", fmt.Errorf("OUTPUT_EXISTS: il file di destinazione '%s' esiste già. Specificare conferma di sovrascrittura.", absDest)
	}

	if err := os.MkdirAll(filepath.Dir(absDest), 0755); err != nil {
		return "", err
	}

	if err := atomicWriteFile(absDest, []byte(rep.MarkdownContent), 0644); err != nil {
		return "", err
	}

	return absDest, nil
}

// jsonErrWithDiag restituisce una risposta di errore JSON arricchita con error_id univoco.
func jsonErrWithDiag(w http.ResponseWriter, status int, msg, component, code, operation string) {
	ev := RecordDiagError(component, code, operation, msg, "", "")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":    msg,
		"error_id": ev.ErrorID,
	})
}
