package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/provider"
	"aria2-transfer-gateway/internal/store"
	"aria2-transfer-gateway/internal/transfer"
)

func newRPCService(t *testing.T) (*transfer.Service, *store.Store) {
	t.Helper()
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"drive",
		filepath.Join(t.TempDir(), "download"),
		1,
	)
	if err != nil {
		taskStore.Close()
		t.Fatal(err)
	}
	return service, taskStore
}

func TestRPCProxyCreatesManagedURIAndUsesGatewayOptions(t *testing.T) {
	service, taskStore := newRPCService(t)
	defer taskStore.Close()
	handler := NewServerWithRPCProxy(
		service,
		"gateway-secret",
		"http://127.0.0.1:1/jsonrpc",
		"aria-secret",
		[]string{"*"},
	).Handler()

	request := httptest.NewRequest(http.MethodPost, "/jsonrpc", strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":1,
		"method":"aria2.addUri",
		"params":["token:aria-secret",["http://example.test/file.bin"],{"dir":"/client-downloads","pause":"true"}]
	}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("RPC status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Result != "gid-api" {
		t.Fatalf("RPC result = %q, want gid-api", result.Result)
	}

	tasks := service.List()
	if len(tasks) != 1 {
		t.Fatalf("managed task count = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.GID != "gid-api" || task.DestinationID != "drive" || task.TargetPath != "/" || !task.Pause {
		t.Fatalf("managed task = %#v", task)
	}
	if _, ok := task.Options["dir"]; ok {
		t.Fatal("RPC dir option replaced the gateway staging directory")
	}
	if _, ok := task.Options["pause"]; ok {
		t.Fatal("pause was not normalized into the task")
	}
}

func TestRPCProxyForwardsOtherMethodsAndRewritesToken(t *testing.T) {
	service, taskStore := newRPCService(t)
	defer taskStore.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Fatalf("decode forwarded RPC = %v", err)
		}
		params, err := decodeRPCParams(call.Params)
		if err != nil {
			t.Fatalf("decode forwarded params = %v", err)
		}
		var token string
		if err := json.Unmarshal(params[0], &token); err != nil {
			t.Fatalf("decode forwarded token = %v", err)
		}
		if token != "token:aria-secret" {
			t.Errorf("forwarded token = %q, want token:aria-secret", token)
		}
		if call.Method != "aria2.tellStatus" {
			t.Errorf("forwarded method = %q, want aria2.tellStatus", call.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":7,"result":{"status":"active"}}`))
	}))
	defer upstream.Close()

	handler := NewServerWithRPCProxy(service, "gateway-secret", upstream.URL, "aria-secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/jsonrpc", strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":7,
		"method":"aria2.tellStatus",
		"params":["token:gateway-secret","gid-1"]
	}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("RPC status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Result.Status != "active" {
		t.Fatalf("RPC result status = %q, want active", result.Result.Status)
	}
}

func TestRPCProxyRejectsMissingRPCToken(t *testing.T) {
	service, taskStore := newRPCService(t)
	defer taskStore.Close()
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer upstream.Close()

	handler := NewServerWithRPCProxy(service, "gateway-secret", upstream.URL, "aria-secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/jsonrpc", strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":1,
		"method":"aria2.tellStatus",
		"params":["gid-1"]
	}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("RPC status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if called {
		t.Fatal("unauthorized RPC request reached aria2")
	}
}
