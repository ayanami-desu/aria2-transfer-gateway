package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAML(t *testing.T) {
	t.Setenv("TEST_GATEWAY_API_TOKEN", "api-token")
	t.Setenv("TEST_ARIA2_RPC_SECRET", "aria2-secret")
	t.Setenv("GATEWAY_CORS_ORIGINS", "")

	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`listen_addr: 0.0.0.0:9090
data_file: /tmp/tasks.db
download_dir: /tmp/downloads
worker_count: 4
default_destination_id: ignored
destinations:
  - id: ignored
    name: Ignored
    provider: rclone
    remote: ignored
cors_origins:
  - https://example.test
api:
  token_env: TEST_GATEWAY_API_TOKEN
aria2:
  endpoint: http://aria2.test/jsonrpc
  secret_env: TEST_ARIA2_RPC_SECRET
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
}

func TestDefaultConfigFields(t *testing.T) {
	defaults := Default()
	if defaults.DataFile != "./data/tasks.db" || defaults.DownloadDir != "./data/downloads" || defaults.WorkerCount != 2 {
		t.Fatalf("default config = %#v", defaults)
	}
	if defaults.API.TokenEnv != "GATEWAY_API_TOKEN" || defaults.Aria2.Endpoint != "http://127.0.0.1:6800/jsonrpc" || defaults.Aria2.SecretEnv != "ARIA2_RPC_SECRET" {
		t.Fatalf("default integrations = %#v", defaults)
	}
}

func TestLoadMissingFileUsesCodeDefaults(t *testing.T) {
	t.Setenv("GATEWAY_CORS_ORIGINS", "https://environment.example")
	cfg, err := Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8787" || cfg.DataFile != "./data/tasks.db" || cfg.DownloadDir != "./data/downloads" || cfg.WorkerCount != 2 {
		t.Fatalf("missing-file defaults = %#v", cfg)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "https://environment.example" {
		t.Fatalf("missing-file CORS origins = %#v", cfg.CORSOrigins)
	}
}

func TestLoadUsesCORSOriginsFromEnvironment(t *testing.T) {
	t.Setenv("GATEWAY_CORS_ORIGINS", " https://public.example , http://localhost:6880, ")

	path := filepath.Join(t.TempDir(), "config.yaml")
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
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want malformed YAML error")
	}
}
