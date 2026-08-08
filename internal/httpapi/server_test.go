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
func (apiFakeDownloader) GetStatus(context.Context, string) (aria2.DownloadStatus, error) {
	return aria2.DownloadStatus{Status: "complete", CompletedLength: "1", TotalLength: "1"}, nil
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
		{ID: "failed-movie", GID: "gid-failed", DestinationID: "drive", TargetPath: "/movies", StagingPath: filepath.Join(stagingRoot, "failed-movie"), FinalFiles: []string{"movie.mkv"}, Status: domain.StatusFailed, CreatedAt: now, UpdatedAt: now},
		{ID: "completed-movie", GID: "gid-completed", DestinationID: "drive", TargetPath: "/movies", StagingPath: filepath.Join(stagingRoot, "completed-movie"), FinalFiles: []string{"movie.mkv"}, Status: domain.StatusCompleted, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
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
func TestDeleteTasksByGID(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
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
	task := domain.Task{
		ID:            "delete-by-gid",
		GID:           "gid-delete-by-gid",
		DestinationID: "drive",
		TargetPath:    "/",
		StagingPath:   filepath.Join(stagingRoot, "delete-by-gid"),
		Status:        domain.StatusDownloading,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(task.StagingPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.StagingPath, "partial.bin"), []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/delete-by-gid", strings.NewReader(`{"gids":["gid-delete-by-gid","gid-direct"]}`))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	responseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusAccepted {
		t.Fatalf("delete by GID status = %d, body = %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var response DeleteTasksByGIDResponse
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Deleted) != 1 || response.Deleted[0] != task.GID || len(response.NotFound) != 1 || response.NotFound[0] != "gid-direct" || len(response.Failed) != 0 {
		t.Fatalf("delete by GID response = %#v", response)
	}
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(task.StagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging path still exists: %v", err)
	}
}

func TestDestinationManagementRedactsSecretsAndFindsMagnetTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"openlist": apiFakeProvider{}, "rclone": apiFakeProvider{}},
		[]domain.Destination{{ID: "backup", Name: "Backup", Provider: "rclone", Remote: "backup"}},
		"backup",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()

	createDestination := httptest.NewRequest(http.MethodPost, "/api/v1/destinations", strings.NewReader(`{"id":"openlist","name":"OpenList","provider":"openlist","endpoint":"https://files.example.test","mount":"/drive","token":"destination-secret"}`))
	createDestination.Header.Set("Authorization", "Bearer secret")
	createdDestination := httptest.NewRecorder()
	handler.ServeHTTP(createdDestination, createDestination)
	if createdDestination.Code != http.StatusCreated {
		t.Fatalf("create destination status = %d, body = %s", createdDestination.Code, createdDestination.Body.String())
	}
	if strings.Contains(createdDestination.Body.String(), "destination-secret") || strings.Contains(createdDestination.Body.String(), `"token"`) {
		t.Fatalf("destination response exposed token: %s", createdDestination.Body.String())
	}
	if !strings.Contains(createdDestination.Body.String(), `"has_token":true`) {
		t.Fatalf("destination response did not report stored token: %s", createdDestination.Body.String())
	}

	setDefault := httptest.NewRequest(http.MethodPut, "/api/v1/destinations/openlist/default", nil)
	setDefault.Header.Set("Authorization", "Bearer secret")
	defaultResponse := httptest.NewRecorder()
	handler.ServeHTTP(defaultResponse, setDefault)
	if defaultResponse.Code != http.StatusNoContent {
		t.Fatalf("set default status = %d, body = %s", defaultResponse.Code, defaultResponse.Body.String())
	}

	magnetURI := "magnet:?xt=urn:btih:0123456789abcdef&dn=Exact%20Name&tr=https%3A%2F%2Ftracker.example.test"
	createTask := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"urls":["`+magnetURI+`"],"target_path":"/"}`))
	createTask.Header.Set("Authorization", "Bearer secret")
	createdTask := httptest.NewRecorder()
	handler.ServeHTTP(createdTask, createTask)
	if createdTask.Code != http.StatusCreated {
		t.Fatalf("create task status = %d, body = %s", createdTask.Code, createdTask.Body.String())
	}

	byGID := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/by-gid/gid-api", nil)
	byGID.Header.Set("Authorization", "Bearer secret")
	gotTask := httptest.NewRecorder()
	handler.ServeHTTP(gotTask, byGID)
	if gotTask.Code != http.StatusOK {
		t.Fatalf("get by GID status = %d, body = %s", gotTask.Code, gotTask.Body.String())
	}
	var taskView domain.TaskView
	if err := json.Unmarshal(gotTask.Body.Bytes(), &taskView); err != nil {
		t.Fatal(err)
	}
	if taskView.DestinationID != "openlist" || len(taskView.URLs) != 1 || taskView.URLs[0] != magnetURI {
		t.Fatalf("task view = %#v", taskView)
	}

	listDestinations := httptest.NewRequest(http.MethodGet, "/api/v1/destinations", nil)
	listDestinations.Header.Set("Authorization", "Bearer secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, listDestinations)
	if strings.Contains(listed.Body.String(), "destination-secret") || strings.Contains(listed.Body.String(), `"token"`) {
		t.Fatalf("destination list exposed token: %s", listed.Body.String())
	}
}
