package mobilecore

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type collectingListener struct {
	events []string
}

func TestOldISOURLChainsToCurrentCatalog(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "nbiso")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	const placeholder = "#!ipxe\necho Waiting for the old catalog\n"
	if err := os.WriteFile(filepath.Join(old, "menu.ipxe"), []byte(placeholder), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config{Root: root, AdvertiseIP: "192.168.50.1", HTTPPort: 8080, SMBPassword: "automatic"}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/nbiso/menu.ipxe", nil)
	newHTTPServer(cfg, &eventSink{}).Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "http://192.168.50.1:8080/iso-menu/menu.ipxe") {
		t.Fatalf("legacy URL did not chain to the app catalog: %d %q", response.Code, response.Body.String())
	}
}

func (l *collectingListener) OnEvent(value string) {
	l.events = append(l.events, value)
}

func TestHTTPServesRangesAndIPXEScript(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{Root: root, IPXEScript: "#!ipxe\necho ok\n"}
	server := httptest.NewServer(newHTTPServer(cfg, &eventSink{}).Handler)
	defer server.Close()

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/image.bin", nil)
	request.Header.Set("Range", "bytes=2-5")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("unexpected range response %d %q", response.StatusCode, body)
	}

	// iPXE asks for the script under its own reserved name; both must serve the same content.
	for _, path := range []string{"/boot.ipxe", "/autoexec.ipxe"} {
		response, err = http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if string(body) != cfg.IPXEScript {
			t.Fatalf("unexpected script at %s: %q", path, body)
		}
	}
}

func TestHTTPRejectsTraversal(t *testing.T) {
	server := httptest.NewServer(newHTTPServer(config{Root: t.TempDir()}, &eventSink{}).Handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/..%2Fsecret")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
}

func TestHTTPLogsRequestMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener := &collectingListener{}
	server := httptest.NewServer(newHTTPServer(config{Root: root}, &eventSink{listener: listener}).Handler)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/image.bin", nil)
	request.Header.Set("Range", "bytes=2-5")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if len(listener.events) != 1 {
		t.Fatalf("expected one event, got %d", len(listener.events))
	}
	var received event
	if err := json.Unmarshal([]byte(listener.events[0]), &received); err != nil {
		t.Fatal(err)
	}
	if received.Code != "http_request" || received.Arguments["status"] != "206" || received.Arguments["bytes"] != "4" {
		t.Fatalf("unexpected event: %+v", received)
	}
}
