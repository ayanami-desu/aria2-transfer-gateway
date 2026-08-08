package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAML(t *testing.T) {
	t.Setenv("TEST_GATEWAY_API_TOKEN", "api-token")
	t.Setenv("TEST_ARIA2_RPC_SECRET", "aria2-secret")
	t.Setenv("TEST_OPENLIST_TOKEN", "openlist-token")
	t.Setenv("TEST_TRANSFER_PROXY", "socks5://proxy.example:1080")
	t.Setenv("GATEWAY_CORS_ORIGINS", "")

	path := filepath.Join(t.TempDir(), "gateway.yaml")
	data := []byte(`listen_addr: 0.0.0.0:9090
data_file: /tmp/tasks.db
download_dir: /tmp/downloads
worker_count: 4
default_destination_id: files
cors_origins:
  - https://example.test
api:
  token_env: TEST_GATEWAY_API_TOKEN
aria2:
  endpoint: http://aria2.test/jsonrpc
  secret_env: TEST_ARIA2_RPC_SECRET
destinations:
  - id: files
    name: Files
    provider: openlist
    endpoint: http://openlist.test
    mount: /files
    token_env: TEST_OPENLIST_TOKEN
    proxy_env: TEST_TRANSFER_PROXY
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:9090" || cfg.DownloadDir != "/tmp/downloads" || cfg.WorkerCount != 4 {
		t.Fatalf("config fields = %#v", cfg)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "https://example.test" {
		t.Fatalf("CORSOrigins = %#v", cfg.CORSOrigins)
	}

	runtime, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if runtime.APIToken != "api-token" || runtime.Aria2Secret != "aria2-secret" {
		t.Fatalf("resolved secrets = %#v", runtime)
	}
	if runtime.DefaultDestinationID != "files" {
		t.Fatalf("default destination = %q", runtime.DefaultDestinationID)
	}
	if len(runtime.Destinations) != 1 || runtime.Destinations[0].Token != "openlist-token" || runtime.Destinations[0].Proxy != "socks5://proxy.example:1080" {
		t.Fatalf("resolved destinations = %#v", runtime.Destinations)
	}
}

func TestDefaultUsesSQLiteDatabase(t *testing.T) {
	if got := Default().DataFile; got != "./data/tasks.db" {
		t.Fatalf("default data file = %q, want SQLite database path", got)
	}
	if got := Default().DownloadDir; got != "./data/downloads" {
		t.Fatalf("default download directory = %q", got)
	}
}

func TestLoadUsesCORSOriginsFromEnvironment(t *testing.T) {
	t.Setenv("GATEWAY_CORS_ORIGINS", " https://public.example , http://localhost:6880, ")

	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("cors_origins:\n  - https://yaml.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://public.example" || cfg.CORSOrigins[1] != "http://localhost:6880" {
		t.Fatalf("CORSOrigins = %#v", cfg.CORSOrigins)
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want malformed YAML error")
	}
}
