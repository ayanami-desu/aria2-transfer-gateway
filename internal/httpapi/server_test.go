package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aria2-transfer-gateway/internal/aria2"
	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/provider"
	"aria2-transfer-gateway/internal/store"
	"aria2-transfer-gateway/internal/transfer"
)

type apiFakeDownloader struct{}

func (apiFakeDownloader) AddURI(context.Context, []string, string, bool, map[string]string) (string, error) {
	return "gid-api", nil
}

func (apiFakeDownloader) AddTorrent(context.Context, string, string, bool, map[string]string) (string, error) {
	return "gid-api", nil
}

func (apiFakeDownloader) AddMetalink(context.Context, string, string, bool, map[string]string) (string, error) {
	return "gid-api", nil
}

func (apiFakeDownloader) GetFiles(context.Context, string) ([]aria2.DownloadFile, error) {
	return []aria2.DownloadFile{}, nil
}
func (apiFakeDownloader) GetFollowedBy(context.Context, string) ([]string, error) {
	return nil, nil
}

func (apiFakeDownloader) Remove(context.Context, string) error {
	return nil
}

type apiFakeProvider struct{}

func (apiFakeProvider) Transfer(context.Context, provider.TransferRequest) error { return nil }

func TestHandlerAuthenticatesAndCreatesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/destinations", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	body := `{"urls":["https://example.test/file"],"destination_id":"drive","target_path":"/movies"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var response domain.TaskView
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.GID != "gid-api" || response.Status != domain.StatusDownloading {
		t.Fatalf("unexpected task response: %#v", response)
	}
}

func TestTaskFilteringAndBatchRetry(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, task := range []domain.Task{
		{ID: "failed-movie", GID: "gid-failed", DestinationID: "drive", TargetPath: "/movies", StagingPath: filepath.Join(stagingRoot, "failed-movie"), Status: domain.StatusFailed, CreatedAt: now, UpdatedAt: now},
		{ID: "completed-movie", GID: "gid-completed", DestinationID: "drive", TargetPath: "/movies", StagingPath: filepath.Join(stagingRoot, "completed-movie"), Status: domain.StatusCompleted, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
	} {
		if err := taskStore.Create(task); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(task.StagingPath, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()

	filteredRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=failed&q=movie", nil)
	filteredRequest.Header.Set("Authorization", "Bearer secret")
	filtered := httptest.NewRecorder()
	handler.ServeHTTP(filtered, filteredRequest)
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, body = %s", filtered.Code, filtered.Body.String())
	}
	var filteredTasks []domain.TaskView
	if err := json.Unmarshal(filtered.Body.Bytes(), &filteredTasks); err != nil {
		t.Fatal(err)
	}
	if len(filteredTasks) != 1 || filteredTasks[0].ID != "failed-movie" {
		t.Fatalf("filtered tasks = %#v", filteredTasks)
	}

	retryRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry", strings.NewReader(`{"ids":["failed-movie","completed-movie","missing"]}`))
	retryRequest.Header.Set("Authorization", "Bearer secret")
	retryRequest.Header.Set("Content-Type", "application/json")
	retried := httptest.NewRecorder()
	handler.ServeHTTP(retried, retryRequest)
	if retried.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body = %s", retried.Code, retried.Body.String())
	}
	var retryResponse RetryTasksResponse
	if err := json.Unmarshal(retried.Body.Bytes(), &retryResponse); err != nil {
		t.Fatal(err)
	}
	if len(retryResponse.Succeeded) != 2 || len(retryResponse.Failed) != 1 {
		t.Fatalf("retry response = %#v", retryResponse)
	}
	updated, err := taskStore.Get("failed-movie")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.StatusTransferPending {
		t.Fatalf("retried status = %q, want %q", updated.Status, domain.StatusTransferPending)
	}
	completed, err := taskStore.Get("completed-movie")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.StatusTransferPending {
		t.Fatalf("completed task retried status = %q, want %q", completed.Status, domain.StatusTransferPending)
	}
	deleteRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/delete", strings.NewReader(`{"ids":["failed-movie","completed-movie"]}`))
	deleteRequest.Header.Set("Authorization", "Bearer secret")
	deleteRequest.Header.Set("Content-Type", "application/json")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	var deleteResponse DeleteTasksResponse
	if err := json.Unmarshal(deleted.Body.Bytes(), &deleteResponse); err != nil {
		t.Fatal(err)
	}
	if len(deleteResponse.Deleted) != 2 || len(deleteResponse.Failed) != 0 {
		t.Fatalf("delete response = %#v", deleteResponse)
	}
	if _, err := taskStore.Get("failed-movie"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted task lookup error = %v, want not found", err)
	}
}
