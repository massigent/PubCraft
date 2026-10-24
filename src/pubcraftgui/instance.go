package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// GUIInstance descrive un'istanza GUI attiva dell'applicazione.
type GUIInstance struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	StartedAt string `json:"started_at"`
}

func getInstanceDescriptorPath() string {
	return filepath.Join(getPubCraftConfigDir(), "instance.json")
}

func writeInstanceDescriptor(port int, url string) error {
	dir := getPubCraftConfigDir()
	_ = os.MkdirAll(dir, 0700)

	inst := GUIInstance{
		PID:       os.Getpid(),
		Port:      port,
		URL:       url,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return err
	}

	targetPath := getInstanceDescriptorPath()
	tmpPath := filepath.Join(dir, fmt.Sprintf("instance.%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	_ = os.Chmod(tmpPath, 0600)

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Chmod(targetPath, 0600)
	return nil
}

func removeInstanceDescriptor() {
	targetPath := getInstanceDescriptorPath()
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return
	}
	var inst GUIInstance
	if err := json.Unmarshal(data, &inst); err == nil && inst.PID == os.Getpid() {
		_ = os.Remove(targetPath)
	}
}

func getActiveGUIInstance() *GUIInstance {
	paths := []string{getInstanceDescriptorPath()}
	if os.Getenv("PUBCRAFT_CONFIG_DIR") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			salusPath := filepath.Join(home, ".salus", ".pubcraft", "instance.json")
			if salusPath != paths[0] {
				paths = append(paths, salusPath)
			}
		}
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var inst GUIInstance
		if err := json.Unmarshal(data, &inst); err != nil {
			continue
		}
		if inst.PID <= 0 || inst.URL == "" {
			continue
		}
		if !processExists(inst.PID) {
			_ = os.Remove(p)
			continue
		}

		// Ping rapido per verificare che l'endpoint risponda
		client := &http.Client{Timeout: 600 * time.Millisecond}
		resp, err := client.Get(inst.URL + "/api/state")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return &inst
		}
	}
	return nil
}
