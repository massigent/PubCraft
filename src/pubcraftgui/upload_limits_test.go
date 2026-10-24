package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadImageBase64DecodedLimit(t *testing.T) {
	previous := getVaultDir()
	dir := t.TempDir()
	vaultMu.Lock()
	vaultDir = dir
	vaultMu.Unlock()
	defer func() {
		vaultMu.Lock()
		vaultDir = previous
		vaultMu.Unlock()
	}()

	// A valid PNG with padding: the limit applies to file bytes, not base64 bytes.
	pixel, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("extra_%d", extra), func(t *testing.T) {
			data := make([]byte, (25<<20)+extra)
			copy(data, pixel)
			body := bytes.NewBufferString(`{"name":"boundary.png","data":"`)
			encoder := base64.NewEncoder(base64.StdEncoding, body)
			if _, err := encoder.Write(data); err != nil {
				t.Fatal(err)
			}
			if err := encoder.Close(); err != nil {
				t.Fatal(err)
			}
			body.WriteString(`"}`)
			req := httptest.NewRequest(http.MethodPost, "/api/uploadimage", body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			apiUploadImage(w, req)
			want := http.StatusOK
			if extra > 0 {
				want = http.StatusRequestEntityTooLarge
			}
			if w.Code != want {
				t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
	st, err := os.Stat(filepath.Join(dir, "images", "boundary.png"))
	if err != nil || st.Size() != 25<<20 {
		t.Fatalf("boundary image was not saved intact: %v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", "boundary-2.png")); !os.IsNotExist(err) {
		t.Fatal("oversized image must not be saved")
	}
}
