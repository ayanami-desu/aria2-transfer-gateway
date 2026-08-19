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

type apiFakeDownloader struct {
	addURI     func(string, map[string]string) (string, error)
	addTorrent func(string, map[string]string) (string, error)
	getFiles   func(string) ([]aria2.DownloadFile, error)
}

func (d apiFakeDownloader) AddURI(_ context.Context, _ []string, directory string, _ bool, options map[string]string) (string, error) {
	if d.addURI != nil {
		return d.addURI(directory, options)
	}
	return "gid-api", nil
}

func (d apiFakeDownloader) AddTorrent(_ context.Context, _ string, directory string, _ bool, options map[string]string) (string, error) {
	if d.addTorrent != nil {
		return d.addTorrent(directory, options)
	}
	return "gid-api", nil
}

func (d apiFakeDownloader) AddMetalink(context.Context, string, string, bool, map[string]string) (string, error) {
	return "gid-api", nil
}

func (d apiFakeDownloader) GetFiles(_ context.Context, gid string) ([]aria2.DownloadFile, error) {
	if d.getFiles != nil {
		return d.getFiles(gid)
	}
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

type blockingPreviewDownloader struct {
	apiFakeDownloader
	started chan struct{}
	done    chan struct{}
}

func (d blockingPreviewDownloader) AddTorrent(ctx context.Context, _ string, _ string, _ bool, _ map[string]string) (string, error) {
	close(d.started)
	<-ctx.Done()
	close(d.done)
	return "", ctx.Err()
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
		filepath.Join(t.TempDir(), "download"),
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
	if response.GID != "gid-api" || response.Status != domain.StatusDownloading || response.TaskName != "file" {
		t.Fatalf("unexpected task response: %#v", response)
	}
}
func TestHandlerCreatesSelectedTorrentTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
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
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"type":"torrent","content":"torrent-content","select_files":[3,1,3],"destination_id":"drive","target_path":"/"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var view domain.TaskView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	stored, err := taskStore.Get(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Options["select-file"] != "1,3" {
		t.Fatalf("stored select-file option = %q, want 1,3", stored.Options["select-file"])
	}
}
func waitForPreviewJob(t *testing.T, handler http.Handler, token, id string) domain.MagnetPreview {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/torrents/preview/"+id, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
		}
		var job previewJobResponse
		if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		switch job.Status {
		case previewJobCompleted:
			if job.Preview == nil {
				t.Fatal("completed preview job has no preview")
			}
			return *job.Preview
		case previewJobFailed, previewJobCancelled:
			t.Fatalf("preview job failed: %q", job.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("preview job did not complete")
	return domain.MagnetPreview{}
}

func TestHandlerPreviewsMagnetMetadata(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloader := apiFakeDownloader{
		addURI: func(directory string, options map[string]string) (string, error) {
			if options["bt-metadata-only"] != "true" || options["bt-save-metadata"] != "true" {
				t.Fatalf("metadata options = %#v", options)
			}
			if err := os.WriteFile(filepath.Join(directory, "abcdef.torrent"), []byte("torrent-content"), 0o640); err != nil {
				return "", err
			}
			return "metadata-gid", nil
		},
		addTorrent: func(string, map[string]string) (string, error) {
			return "torrent-gid", nil
		},
		getFiles: func(gid string) ([]aria2.DownloadFile, error) {
			if gid != "torrent-gid" {
				return nil, nil
			}
			return []aria2.DownloadFile{{Index: "1", Path: "movie.mkv", Length: "10"}}, nil
		},
	}
	service, err := transfer.NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"drive",
		filepath.Join(t.TempDir(), "download"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", strings.NewReader(`{"url":"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	var job previewJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || response.Header().Get("Location") == "" {
		t.Fatalf("preview job response = %#v, headers = %#v", job, response.Header())
	}
	preview := waitForPreviewJob(t, handler, "secret", job.ID)
	if preview.Content == "" || len(preview.Files) != 1 || preview.Files[0].Index != 1 {
		t.Fatalf("preview = %#v", preview)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", strings.NewReader(`{"content":"dG9ycmVudC1jb250ZW50"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("torrent preview status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	preview = waitForPreviewJob(t, handler, "secret", job.ID)
	if len(preview.Files) != 1 || preview.Files[0].Path != "movie.mkv" {
		t.Fatalf("torrent preview = %#v", preview)
	}
}

func TestHandlerPreviewIsAsynchronous(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloader := blockingPreviewDownloader{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	service, err := transfer.NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"drive",
		filepath.Join(t.TempDir(), "download"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", strings.NewReader(`{"content":"dG9ycmVudC1jb250ZW50"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	var job previewJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	select {
	case <-downloader.started:
	case <-time.After(time.Second):
		t.Fatal("preview job did not start")
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	cancel := httptest.NewRequest(http.MethodDelete, "/api/v1/torrents/preview/"+job.ID, nil)
	cancel.Header.Set("Authorization", "Bearer secret")
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, body = %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	select {
	case <-downloader.done:
	case <-time.After(time.Second):
		t.Fatal("preview job did not cancel")
	}
}
func TestHandlerSetsConfiguredCORS(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "download"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"https://ariang.example"}).Handler()
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	request.Header.Set("Origin", "https://ariang.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://ariang.example" {
		t.Fatalf("allow origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Methods") == "" || response.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("missing CORS capability headers: %#v", response.Header())
	}
}

func TestTaskFilteringAndBatchRetry(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, task := range []domain.Task{
		{ID: "failed-movie", GID: "gid-failed", DestinationID: "drive", TargetPath: "/movies", DownloadPath: filepath.Join(downloadRoot, "failed-movie"), FinalFiles: []string{"movie.mkv"}, Status: domain.StatusFailed, CreatedAt: now, UpdatedAt: now},
		{ID: "completed-movie", GID: "gid-completed", DestinationID: "drive", TargetPath: "/movies", DownloadPath: filepath.Join(downloadRoot, "completed-movie"), FinalFiles: []string{"movie.mkv"}, Status: domain.StatusCompleted, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
	} {
		if err := taskStore.Create(task); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
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
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := transfer.NewService(
		taskStore,
		apiFakeDownloader{},
		map[string]provider.Provider{"fake": apiFakeProvider{}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "delete-by-gid",
		GID:           "gid-delete-by-gid",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, "delete-by-gid"), Status: domain.StatusDownloading,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC()}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.DownloadPath, "partial.bin"), []byte("partial"), 0o640); err != nil {
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
	if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
		t.Fatalf("download path still exists: %v", err)
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
		[]domain.Destination{{ID: "backup", Name: "Backup", Provider: "rclone", Remote: "backup", RcloneConfig: "/rclone/rclone.conf"}},
		"backup",
		filepath.Join(t.TempDir(), "download"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(service, "secret", []string{"*"}).Handler()

	createDestination := httptest.NewRequest(http.MethodPost, "/api/v1/destinations", strings.NewReader(`{"id":"openlist","name":"OpenList","provider":"openlist","endpoint":"https://files.example.test","mount":"/drive","token":"destination-secret","proxy":"socks5://proxy-user:proxy-secret@proxy.example:1080"}`))
	createDestination.Header.Set("Authorization", "Bearer secret")
	createdDestination := httptest.NewRecorder()
	handler.ServeHTTP(createdDestination, createDestination)
	if createdDestination.Code != http.StatusCreated {
		t.Fatalf("create destination status = %d, body = %s", createdDestination.Code, createdDestination.Body.String())
	}
	if strings.Contains(createdDestination.Body.String(), "destination-secret") || strings.Contains(createdDestination.Body.String(), `"token"`) || strings.Contains(createdDestination.Body.String(), "proxy-user") || strings.Contains(createdDestination.Body.String(), "proxy-secret") {
		t.Fatalf("destination response exposed credentials: %s", createdDestination.Body.String())
	}
	if !strings.Contains(createdDestination.Body.String(), `"has_token":true`) || !strings.Contains(createdDestination.Body.String(), `"proxy":"socks5://proxy.example:1080"`) || !strings.Contains(createdDestination.Body.String(), `"has_proxy_credentials":true`) {
		t.Fatalf("destination response did not report redacted secrets: %s", createdDestination.Body.String())
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
	if strings.Contains(listed.Body.String(), "destination-secret") || strings.Contains(listed.Body.String(), `"token"`) || strings.Contains(listed.Body.String(), "proxy-user") || strings.Contains(listed.Body.String(), "proxy-secret") {
		t.Fatalf("destination list exposed credentials: %s", listed.Body.String())
	}

	clearProxy := httptest.NewRequest(http.MethodPut, "/api/v1/destinations/openlist", strings.NewReader(`{"name":"OpenList","provider":"openlist","endpoint":"https://files.example.test","mount":"/drive","clear_proxy":true}`))
	clearProxy.Header.Set("Authorization", "Bearer secret")
	cleared := httptest.NewRecorder()
	handler.ServeHTTP(cleared, clearProxy)
	if cleared.Code != http.StatusOK || strings.Contains(cleared.Body.String(), `"has_proxy":true`) {
		t.Fatalf("clear proxy status = %d, body = %s", cleared.Code, cleared.Body.String())
	}
	storedDestination, err := taskStore.GetDestination("openlist")
	if err != nil {
		t.Fatal(err)
	}
	if storedDestination.Proxy != "" || storedDestination.Token != "destination-secret" {
		t.Fatalf("cleared destination secrets = token %q, proxy %q", storedDestination.Token, storedDestination.Proxy)
	}
}
