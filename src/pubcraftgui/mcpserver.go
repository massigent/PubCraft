package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// MCP JSON-RPC 2.0 Structures

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// RunMCPServer avvia il server MCP su stdio (compatibile con Claude, Cursor, Inspector e agenti AI).
func RunMCPServer() {
	// Tutti i messaggi di log devono andare esclusivamente su stderr.
	inst := getActiveGUIInstance()
	if inst != nil {
		fmt.Fprintf(os.Stderr, "PubCraft MCP bridge collegato alla GUI attiva su %s (PID %d)\n", inst.URL, inst.PID)
	} else {
		fmt.Fprintf(os.Stderr, "PubCraft MCP server avviato su stdio (v1.0.0-alpha.1, %s/%s)\n", runtime.GOOS, runtime.GOARCH)
	}

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Errore di lettura stdio: %v\n", err)
			break
		}

		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}

		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendMCPError(writer, nil, -32700, "Parse error: JSON non valido")
			continue
		}

		handleMCPRequest(writer, &req)
	}
}

func sendMCPResponse(w *bufio.Writer, id interface{}, result interface{}) {
	resp := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
	_ = w.Flush()
}

func sendMCPError(w *bufio.Writer, id interface{}, code int, message string) {
	resp := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcpError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
	_ = w.Flush()
}

func handleMCPRequest(w *bufio.Writer, req *mcpRequest) {
	switch req.Method {
	case "initialize":
		sendMCPResponse(w, req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "pubcraft",
				"version": "1.0.0-alpha.1",
			},
		})

	case "notifications/initialized", "initialized":
		// Notifica di conferma client, nessuna risposta richiesta se id è nullo
		if req.ID != nil {
			sendMCPResponse(w, req.ID, map[string]interface{}{"status": "ok"})
		}

	case "ping":
		sendMCPResponse(w, req.ID, map[string]interface{}{})

	case "tools/list":
		tools := []map[string]interface{}{
			{
				"name":        "pubcraft_get_status",
				"description": "Restituisce lo stato dell'applicazione, il libro attivo e il riepilogo degli errori recenti con i relativi identificativi (error_id).",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			{
				"name":        "pubcraft_get_diagnostics",
				"description": "Restituisce l'elenco degli eventi diagnostici e degli errori recenti registrati localmente nel sistema (filtrabili per componente).",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Numero massimo di eventi da restituire (default 20)",
						},
						"component": map[string]interface{}{
							"type":        "string",
							"description": "Filtra per componente (es. save, export, connection, import)",
						},
					},
				},
			},
			{
				"name":        "pubcraft_prepare_bug_report",
				"description": "Prepara un rapporto diagnostico strutturato per la segnalazione di un problema allo sviluppatore, escludendo per sicurezza i contenuti del libro.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"description": map[string]interface{}{
							"type":        "string",
							"description": "Descrizione del problema riscontrato",
						},
						"expected": map[string]interface{}{
							"type":        "string",
							"description": "Comportamento o risultato atteso dall'utente",
						},
						"observed": map[string]interface{}{
							"type":        "string",
							"description": "Comportamento o errore effettivamente osservato",
						},
						"error_ids": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Identificativi degli errori recenti da collegare alla segnalazione",
						},
					},
					"required": []string{"description"},
				},
			},
			{
				"name":        "pubcraft_export_bug_report",
				"description": "Esporta su file locale il testo del rapporto diagnostico preparato in precedenza.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"report_id": map[string]interface{}{
							"type":        "string",
							"description": "Identificativo del rapporto generato da pubcraft_prepare_bug_report",
						},
						"destination": map[string]interface{}{
							"type":        "string",
							"description": "Percorso del file di destinazione (opzionale, default nella home)",
						},
					},
					"required": []string{"report_id"},
				},
			},
			{
				"name":        "pubcraft_open_book",
				"description": "Apre un libro specificato dal percorso della sua cartella o manoscritto (aggiornando l'interfaccia se la GUI è aperta).",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Percorso del vault o del manoscritto da aprire",
						},
					},
					"required": []string{"path"},
				},
			},
		}
		sendMCPResponse(w, req.ID, map[string]interface{}{"tools": tools})

	case "tools/call":
		handleMCPToolCall(w, req)

	default:
		sendMCPError(w, req.ID, -32601, fmt.Sprintf("Metodo non trovato: %s", req.Method))
	}
}

func handleMCPToolCall(w *bufio.Writer, req *mcpRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		sendMCPError(w, req.ID, -32602, "Parametri non validi")
		return
	}

	switch params.Name {
	case "pubcraft_get_status":
		inst := getActiveGUIInstance()
		if inst != nil {
			status := make(map[string]interface{})
			client := &http.Client{Timeout: 3 * time.Second}
			if resp, err := client.Get(inst.URL + "/api/diagnostics/status"); err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					_ = json.NewDecoder(resp.Body).Decode(&status)
				}
			}
			status["app_version"] = "1.0.0-alpha.1"
			status["gui_connected"] = true
			status["gui_pid"] = inst.PID
			status["gui_url"] = inst.URL

			if resp, err := client.Get(inst.URL + "/api/state"); err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var st struct {
						Vault string `json:"vault"`
						Book  *struct {
							Title    string        `json:"title"`
							Chapters []interface{} `json:"chapters"`
						} `json:"book"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&st); err == nil {
						status["vault"] = st.Vault
						if st.Book != nil {
							status["book_title"] = st.Book.Title
							status["chapters_count"] = len(st.Book.Chapters)
						} else {
							status["book_title"] = ""
							status["chapters_count"] = 0
						}
					}
				}
			}
			jsonBytes, _ := json.MarshalIndent(status, "", "  ")
			sendMCPToolResult(w, req.ID, string(jsonBytes), false)
			return
		}

		// Fallback locale headless
		status := GetDiagStatusSummary()
		vDir := getVaultDir()
		status["app_version"] = "1.0.0-alpha.1"
		status["vault"] = vDir
		b := loadBook()
		if b != nil {
			status["book_title"] = b.Title
			status["chapters_count"] = len(b.Chapters)
		} else {
			status["book_title"] = ""
			status["chapters_count"] = 0
		}
		jsonBytes, _ := json.MarshalIndent(status, "", "  ")
		sendMCPToolResult(w, req.ID, string(jsonBytes), false)

	case "pubcraft_get_diagnostics":
		limit := 20
		if l, ok := params.Arguments["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		component := ""
		if c, ok := params.Arguments["component"].(string); ok {
			component = c
		}

		inst := getActiveGUIInstance()
		if inst != nil {
			client := &http.Client{Timeout: 3 * time.Second}
			u := fmt.Sprintf("%s/api/diagnostics/events?limit=%d", inst.URL, limit)
			if component != "" {
				u += "&component=" + url.QueryEscape(component)
			}
			resp, err := client.Get(u)
			if err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore comunicazione con la GUI: %v", err), true)
				return
			}
			defer resp.Body.Close()
			var res struct {
				Events []interface{} `json:"events"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore decodifica risposta diagnostica: %v", err), true)
				return
			}
			jsonBytes, _ := json.MarshalIndent(res.Events, "", "  ")
			sendMCPToolResult(w, req.ID, string(jsonBytes), false)
			return
		}

		// Fallback locale headless
		events := GetRecentDiagEvents(limit, component)
		jsonBytes, _ := json.MarshalIndent(events, "", "  ")
		sendMCPToolResult(w, req.ID, string(jsonBytes), false)

	case "pubcraft_prepare_bug_report":
		desc, _ := params.Arguments["description"].(string)
		if strings.TrimSpace(desc) == "" {
			sendMCPToolResult(w, req.ID, "Errore: 'description' è obbligatorio", true)
			return
		}
		expected, _ := params.Arguments["expected"].(string)
		observed, _ := params.Arguments["observed"].(string)

		var errorIDs []string
		if rawIDs, ok := params.Arguments["error_ids"].([]interface{}); ok {
			for _, id := range rawIDs {
				if s, ok := id.(string); ok {
					errorIDs = append(errorIDs, s)
				}
			}
		}

		inst := getActiveGUIInstance()
		if inst != nil {
			reqData := map[string]interface{}{
				"description": desc,
				"expected":    expected,
				"observed":    observed,
				"error_ids":   errorIDs,
			}
			reqBytes, _ := json.Marshal(reqData)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(inst.URL+"/api/diagnostics/report", "application/json", bytes.NewReader(reqBytes))
			if err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore comunicazione con la GUI: %v", err), true)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore generazione rapporto nella GUI: %s", string(bodyBytes)), true)
				return
			}
			var rep BugReport
			if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore decodifica rapporto: %v", err), true)
				return
			}
			resultMap := map[string]interface{}{
				"report_id": rep.ReportID,
				"created":   rep.CreatedAt.Format("2006-01-02 15:04:05 UTC"),
				"markdown":  rep.MarkdownContent,
			}
			jsonBytes, _ := json.MarshalIndent(resultMap, "", "  ")
			sendMCPToolResult(w, req.ID, string(jsonBytes), false)
			return
		}

		// Fallback locale headless
		rep, err := PrepareBugReport(errorIDs, desc, expected, observed)
		if err != nil {
			sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore generazione rapporto: %v", err), true)
			return
		}

		resultMap := map[string]interface{}{
			"report_id": rep.ReportID,
			"created":   rep.CreatedAt.Format("2006-01-02 15:04:05 UTC"),
			"markdown":  rep.MarkdownContent,
		}
		jsonBytes, _ := json.MarshalIndent(resultMap, "", "  ")
		sendMCPToolResult(w, req.ID, string(jsonBytes), false)

	case "pubcraft_export_bug_report":
		reportID, _ := params.Arguments["report_id"].(string)
		if strings.TrimSpace(reportID) == "" {
			sendMCPToolResult(w, req.ID, "Errore: 'report_id' è obbligatorio", true)
			return
		}
		dest, _ := params.Arguments["destination"].(string)
		ow, _ := params.Arguments["overwrite"].(bool)

		inst := getActiveGUIInstance()
		if inst != nil {
			reqData := map[string]interface{}{
				"report_id":   reportID,
				"destination": dest,
				"overwrite":   ow,
			}
			reqBytes, _ := json.Marshal(reqData)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(inst.URL+"/api/diagnostics/export", "application/json", bytes.NewReader(reqBytes))
			if err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore comunicazione con la GUI: %v", err), true)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore esportazione nella GUI: %s", string(bodyBytes)), true)
				return
			}
			var res struct {
				Ok   bool   `json:"ok"`
				Path string `json:"path"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&res)
			sendMCPToolResult(w, req.ID, fmt.Sprintf("Rapporto esportato con successo in: %s", res.Path), false)
			return
		}

		// Fallback locale headless
		path, err := ExportBugReport(reportID, dest, ow)
		if err != nil {
			sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore esportazione: %v", err), true)
			return
		}
		sendMCPToolResult(w, req.ID, fmt.Sprintf("Rapporto esportato con successo in: %s", path), false)

	case "pubcraft_open_book":
		p, _ := params.Arguments["path"].(string)
		p = strings.TrimSpace(p)
		if p == "" {
			sendMCPToolResult(w, req.ID, "Errore: 'path' è obbligatorio", true)
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}

		inst := getActiveGUIInstance()
		if inst != nil {
			reqBody, _ := json.Marshal(map[string]string{"path": p})
			httpReq, err := http.NewRequest(http.MethodPost, inst.URL+"/api/openat", bytes.NewReader(reqBody))
			if err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore preparazione richiesta: %v", err), true)
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(httpReq)
			if err != nil {
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore comunicazione con la GUI: %v", err), true)
				return
			}
			defer resp.Body.Close()
			bodyBytes, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				var errResp struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal(bodyBytes, &errResp)
				errMsg := errResp.Error
				if errMsg == "" {
					errMsg = string(bodyBytes)
				}
				sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore apertura libro nella GUI: %s", errMsg), true)
				return
			}
			var st struct {
				Vault string `json:"vault"`
				Book  *struct {
					Title    string        `json:"title"`
					Chapters []interface{} `json:"chapters"`
				} `json:"book"`
			}
			_ = json.Unmarshal(bodyBytes, &st)
			title := ""
			count := 0
			if st.Book != nil {
				title = st.Book.Title
				count = len(st.Book.Chapters)
			}
			sendMCPToolResult(w, req.ID, fmt.Sprintf("Libro aperto con successo nella GUI: %q (%d capitoli) in %s", title, count, st.Vault), false)
			return
		}

		// Fallback locale headless
		b, err := importAnyManuscript(p)
		if err != nil {
			sendMCPToolResult(w, req.ID, fmt.Sprintf("Errore apertura libro: %v", err), true)
			return
		}
		sendMCPToolResult(w, req.ID, fmt.Sprintf("Libro aperto con successo: %q (%d capitoli) in %s", b.Title, len(b.Chapters), getVaultDir()), false)

	default:
		sendMCPError(w, req.ID, -32601, fmt.Sprintf("Strumento non riconosciuto: %s", params.Name))
	}
}

func sendMCPToolResult(w *bufio.Writer, id interface{}, text string, isError bool) {
	sendMCPResponse(w, id, map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
		"isError": isError,
	})
}
