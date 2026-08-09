package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aria2-transfer-gateway/internal/domain"
)

func TestOpenListTransferStreamsFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "token-value" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("File-Path"); got != "/google-drive/movies/file.txt" {
			t.Errorf("file path = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "hello" {
			t.Errorf("body = %q", body)
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	provider := NewOpenList(server.Client())
	err := provider.Transfer(context.Background(), TransferRequest{
		SourceDir:  source,
		TargetPath: "/movies",
		Destination: domain.Destination{
			ID:       "drive",
			Endpoint: server.URL,
			Mount:    "/google-drive",
			Token:    "token-value",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenListTransferUsesFileWhitelist(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Header.Get("File-Path"))
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	source := t.TempDir()
	for _, name := range []string{"file.txt", "metadata.torrent", "state.aria2"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	provider := NewOpenList(server.Client())
	err := provider.Transfer(context.Background(), TransferRequest{
		SourceDir:  source,
		TargetPath: "/movies",
		Files:      []string{"file.txt"},
		Destination: domain.Destination{
			ID:       "drive",
			Endpoint: server.URL,
			Mount:    "/google-drive",
			Token:    "token-value",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/google-drive/movies/file.txt" {
		t.Fatalf("uploaded paths = %#v, want only the final file", paths)
	}
}

type partialUploadTransport struct{}

func (partialUploadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	buffer := make([]byte, 1)
	_, _ = req.Body.Read(buffer)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"code":200,"message":"success"}`)),
		Request:    req,
	}, nil
}

func TestOpenListTransferRejectsShortUpload(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "payload.bin"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	provider := NewOpenList(&http.Client{Transport: partialUploadTransport{}})
	err := provider.Transfer(context.Background(), TransferRequest{
		SourceDir:  source,
		TargetPath: "/movies",
		Files:      []string{"payload.bin"},
		Destination: domain.Destination{
			ID:       "drive",
			Endpoint: "http://openlist.test",
			Token:    "token-value",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "sent 1 bytes, want 5") {
		t.Fatalf("error = %v, want short upload error", err)
	}
}

func TestOpenListTransferUsesConfiguredHTTPProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "openlist.invalid" || r.URL.Path != "/api/fs/put" {
			t.Errorf("proxied URL = %s", r.URL.String())
		}
		if got := r.Header.Get("Proxy-Authorization"); got != "Basic cHJveHktdXNlcjpwcm94eS1wYXNz" {
			t.Errorf("proxy authorization = %q", got)
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read proxied body: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer proxy.Close()

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	proxyURL := strings.Replace(proxy.URL, "http://", "http://proxy-user:proxy-pass@", 1)
	err := NewOpenList(&http.Client{}).Transfer(context.Background(), TransferRequest{
		SourceDir: source,
		Destination: domain.Destination{
			ID: "drive", Endpoint: "http://openlist.invalid", Token: "token-value", Proxy: proxyURL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}
