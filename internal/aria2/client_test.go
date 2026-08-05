package aria2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientAddURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Method != "aria2.addUri" {
			t.Errorf("method = %q", request.Method)
		}
		if len(request.Params) != 3 {
			t.Fatalf("params length = %d, want 3", len(request.Params))
		}
		var token string
		if err := json.Unmarshal(request.Params[0], &token); err != nil {
			t.Fatal(err)
		}
		if token != "token:secret" {
			t.Errorf("token = %q", token)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"gid-1"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())

	gid, err := client.AddURI(context.Background(), []string{"https://example.test/file"}, "/staging/task-1", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gid != "gid-1" {
		t.Fatalf("gid = %q, want gid-1", gid)
	}
}

func TestClientGetFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Method != "aria2.getFiles" {
			t.Errorf("method = %q", request.Method)
		}
		if len(request.Params) != 2 {
			t.Fatalf("params length = %d, want 2", len(request.Params))
		}
		var gid string
		if err := json.Unmarshal(request.Params[1], &gid); err != nil {
			t.Fatal(err)
		}
		if gid != "gid-1" {
			t.Errorf("gid = %q", gid)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[{"path":"/staging/task-1/file.mkv","length":"34896138","completedLength":"34896138","selected":"true"},{"path":"/staging/task-1/state.aria2","length":"0","completedLength":"0","selected":"false"}]}`))
	}))
	defer server.Close()

	files, err := NewClient(server.URL, "secret", server.Client()).GetFiles(context.Background(), "gid-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "/staging/task-1/file.mkv" || !files[0].Selected || files[1].Selected {
		t.Fatalf("files = %#v", files)
	}
}
func TestClientGetStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "aria2.tellStatus" {
			t.Errorf("method = %q", request.Method)
		}
		if len(request.Params) != 3 {
			t.Fatalf("params length = %d, want 3", len(request.Params))
		}
		var gid string
		if err := json.Unmarshal(request.Params[1], &gid); err != nil {
			t.Fatal(err)
		}
		if gid != "gid-1" {
			t.Errorf("gid = %q", gid)
		}
		var keys []string
		if err := json.Unmarshal(request.Params[2], &keys); err != nil {
			t.Fatal(err)
		}
		if len(keys) != 3 || keys[0] != "status" || keys[1] != "completedLength" || keys[2] != "totalLength" {
			t.Fatalf("keys = %#v", keys)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"complete","completedLength":"7","totalLength":"7"}}`))
	}))
	defer server.Close()

	status, err := NewClient(server.URL, "secret", server.Client()).GetStatus(context.Background(), "gid-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "complete" || status.CompletedLength != "7" || status.TotalLength != "7" {
		t.Fatalf("status = %#v", status)
	}
}

func TestClientGetFollowedBy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "aria2.tellStatus" {
			t.Errorf("method = %q", request.Method)
		}
		if len(request.Params) != 3 {
			t.Fatalf("params length = %d, want 3", len(request.Params))
		}
		var gid string
		if err := json.Unmarshal(request.Params[1], &gid); err != nil {
			t.Fatal(err)
		}
		if gid != "gid-1" {
			t.Errorf("gid = %q", gid)
		}
		var keys []string
		if err := json.Unmarshal(request.Params[2], &keys); err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0] != "followedBy" {
			t.Fatalf("keys = %#v", keys)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"followedBy":["gid-2"]}}`))
	}))
	defer server.Close()

	followedBy, err := NewClient(server.URL, "secret", server.Client()).GetFollowedBy(context.Background(), "gid-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(followedBy) != 1 || followedBy[0] != "gid-2" {
		t.Fatalf("followedBy = %#v", followedBy)
	}
}

func TestClientRemove(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Method != "aria2.forceRemove" {
			t.Errorf("method = %q", request.Method)
		}
		if len(request.Params) != 2 {
			t.Fatalf("params length = %d, want 2", len(request.Params))
		}
		var gid string
		if err := json.Unmarshal(request.Params[1], &gid); err != nil {
			t.Fatal(err)
		}
		if gid != "gid-1" {
			t.Errorf("gid = %q", gid)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":true}`))
	}))
	defer server.Close()
	if err := NewClient(server.URL, "secret", server.Client()).Remove(context.Background(), "gid-1"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRemoveClassifiesMissingGID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"GID 580c0d3136b5b709 is not found"}}`))
	}))
	defer server.Close()
	err := NewClient(server.URL, "secret", server.Client()).Remove(context.Background(), "missing-gid")
	if !errors.Is(err, ErrGIDNotFound) {
		t.Fatalf("error = %v, want ErrGIDNotFound", err)
	}
}
