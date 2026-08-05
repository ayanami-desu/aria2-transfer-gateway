package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aria2-transfer-gateway/internal/aria2"
	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/provider"
	"aria2-transfer-gateway/internal/store"
)

type fakeDownloader struct {
	getFiles   func(string) []aria2.DownloadFile
	followedBy func(string) []string
	remove     func(string) error
}

func (d fakeDownloader) Remove(_ context.Context, gid string) error {
	if d.remove == nil {
		return nil
	}
	return d.remove(gid)
}

func (fakeDownloader) AddURI(context.Context, []string, string, bool, map[string]string) (string, error) {
	return "gid-1", nil
}

func (fakeDownloader) AddTorrent(context.Context, string, string, bool, map[string]string) (string, error) {
	return "gid-1", nil
}

func (fakeDownloader) AddMetalink(context.Context, string, string, bool, map[string]string) (string, error) {
	return "gid-1", nil
}

func (d fakeDownloader) GetFiles(_ context.Context, gid string) ([]aria2.DownloadFile, error) {
	if d.getFiles == nil {
		return []aria2.DownloadFile{}, nil
	}
	return d.getFiles(gid), nil
}
func (d fakeDownloader) GetFollowedBy(_ context.Context, gid string) ([]string, error) {
	if d.followedBy == nil {
		return nil, nil
	}
	return d.followedBy(gid), nil
}

type fakeProvider struct {
	mu       sync.Mutex
	requests []provider.TransferRequest
	done     chan struct{}
}

func (p *fakeProvider) Transfer(_ context.Context, request provider.TransferRequest) error {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	close(p.done)
	return nil
}

func TestServiceRetriesAnyTaskStatus(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	statuses := []string{
		domain.StatusQueued,
		domain.StatusDownloading,
		domain.StatusTransferPending,
		domain.StatusTransferring,
		domain.StatusCompleted,
		domain.StatusFailed,
	}
	now := time.Now().UTC()
	for _, status := range statuses {
		task := domain.Task{
			ID:            "retry-" + status,
			DestinationID: "drive",
			TargetPath:    "/",
			StagingPath:   filepath.Join(stagingRoot, status),
			Status:        status,
			Error:         "previous error",
			CreatedAt:     now,
			UpdatedAt:     now,
			CompletedAt:   now,
		}
		if err := taskStore.Create(task); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(task.StagingPath, 0o750); err != nil {
			t.Fatal(err)
		}
		updated, err := service.Retry(context.Background(), task.ID, RetryModeUpload)
		if err != nil {
			t.Fatalf("retry %s: %v", status, err)
		}
		if updated.Status != domain.StatusTransferPending || updated.Error != "" || !updated.CompletedAt.IsZero() {
			t.Fatalf("updated %s task = %#v", status, updated)
		}
	}
}

func TestServiceFullRetryReplacesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	removedGID := ""
	downloader := fakeDownloader{remove: func(gid string) error {
		removedGID = gid
		return nil
	}}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	original, err := service.Create(context.Background(), TaskInput{
		URLs:          []string{"https://example.test/file"},
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldStagingPath := original.StagingPath
	retried, err := service.Retry(context.Background(), original.ID, RetryModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if removedGID != original.GID {
		t.Fatalf("removed GID = %q, want %q", removedGID, original.GID)
	}
	if retried.ID == original.ID || retried.Status != domain.StatusDownloading || retried.URLs[0] != original.URLs[0] {
		t.Fatalf("replacement task = %#v", retried)
	}
	if _, err := taskStore.Get(original.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(oldStagingPath); !os.IsNotExist(err) {
		t.Fatalf("old staging path still exists: %v", err)
	}
	if info, err := os.Stat(retried.StagingPath); err != nil || !info.IsDir() {
		t.Fatalf("new staging path = %q, stat error = %v", retried.StagingPath, err)
	}
}

func TestServiceDeleteCancelsAndRemovesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	removedGID := ""
	service, err := NewService(
		taskStore,
		fakeDownloader{remove: func(gid string) error {
			removedGID = gid
			return nil
		}},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), TaskInput{
		URLs:          []string{"https://example.test/file"},
		DestinationID: "drive",
		TargetPath:    "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.StagingPath, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if removedGID != task.GID {
		t.Fatalf("removed GID = %q, want %q", removedGID, task.GID)
	}
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(task.StagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging path still exists: %v", err)
	}
}

func TestServiceDeleteIgnoresMissingTaskData(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	service, err := NewService(
		taskStore,
		fakeDownloader{remove: func(string) error {
			return aria2.ErrGIDNotFound
		}},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID:            "orphaned-task",
		GID:           "missing-gid",
		DestinationID: "drive",
		TargetPath:    "/",
		StagingPath:   filepath.Join(t.TempDir(), "already-gone"),
		Status:        domain.StatusFailed,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("orphaned task lookup error = %v, want not found", err)
	}
}

func TestServiceUploadRetryRequiresStagingDirectory(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	stagingRoot := filepath.Join(t.TempDir(), "staging")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		stagingRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID:            "missing-staging",
		DestinationID: "drive",
		TargetPath:    "/",
		StagingPath:   filepath.Join(stagingRoot, "missing-staging"),
		Status:        domain.StatusFailed,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Retry(context.Background(), task.ID, RetryModeUpload); err == nil {
		t.Fatal("upload retry succeeded without a staging directory")
	}
	current, err := taskStore.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusFailed {
		t.Fatalf("task status = %q, want failed", current.Status)
	}
}

func TestServiceTransfersCompletedTaskAndCleansStaging(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeProvider{done: make(chan struct{})}
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": backend},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	task, err := service.Create(ctx, TaskInput{
		URLs:          []string{"https://example.test/file"},
		DestinationID: "drive",
		TargetPath:    "/movies",
		Cleanup:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(task.StagingPath, "file.bin")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "7", CompletedLength: "7", Selected: true}}
	}
	if err := service.HandleCompleted(context.Background(), task.GID, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.done:
	case <-time.After(2 * time.Second):
		t.Fatal("transfer worker did not run")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := service.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == domain.StatusCompleted {
			if _, err := os.Stat(task.StagingPath); !os.IsNotExist(err) {
				t.Fatalf("staging path still exists: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ = service.Get(task.ID)
	t.Fatalf("task status = %q, want completed", task.Status)
}

func TestServiceUsesAria2FileWhitelist(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	downloader := &fakeDownloader{}
	backend := &fakeProvider{done: make(chan struct{})}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": backend},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	task, err := service.Create(ctx, TaskInput{
		Type:          "torrent",
		Content:       "torrent-content",
		Cleanup:       false,
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"movie.mkv", "metadata.torrent", "state.aria2"} {
		if err := os.WriteFile(filepath.Join(task.StagingPath, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{
			{Path: filepath.Join(task.StagingPath, "movie.mkv"), Length: "9", CompletedLength: "9", Selected: true},
			{Path: "[METADATA]metadata", Length: "0", CompletedLength: "0", Selected: true},
			{Path: filepath.Join(task.StagingPath, "state.aria2"), Length: "10", CompletedLength: "0", Selected: false},
		}
	}
	if err := service.HandleCompleted(ctx, task.GID, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.done:
	case <-time.After(2 * time.Second):
		t.Fatal("transfer worker did not run")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := service.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == domain.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusCompleted {
		t.Fatalf("task status = %q, want completed", current.Status)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.requests) != 1 {
		t.Fatalf("transfer requests = %d, want 1", len(backend.requests))
	}
	got := backend.requests[0].Files
	if len(got) != 1 || got[0] != "movie.mkv" {
		t.Fatalf("final files = %#v, want [movie.mkv]", got)
	}
	if _, err := os.Stat(filepath.Join(task.StagingPath, "metadata.torrent")); !os.IsNotExist(err) {
		t.Fatalf("metadata.torrent stat error = %v, want file removed", err)
	}
	if _, err := os.Stat(filepath.Join(task.StagingPath, "state.aria2")); !os.IsNotExist(err) {
		t.Fatalf("state.aria2 stat error = %v, want file removed", err)
	}
	if _, err := os.Stat(filepath.Join(task.StagingPath, "movie.mkv")); err != nil {
		t.Fatalf("movie.mkv stat error = %v, want final file retained", err)
	}
}

func TestServiceIgnoresMetadataCompletion(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": &fakeProvider{done: make(chan struct{})}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), TaskInput{
		Type:          "torrent",
		Content:       "torrent-content",
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: "[METADATA]metadata", Length: "0", CompletedLength: "0", Selected: true}}
	}
	if err := service.HandleCompleted(context.Background(), task.GID, ""); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusDownloading {
		t.Fatalf("task status = %q, want downloading", current.Status)
	}
}

func TestServiceResolvesCompletionByFilePath(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": &fakeProvider{done: make(chan struct{})}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), TaskInput{
		URLs:          []string{"magnet:?xt=urn:btih:test"},
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(task.StagingPath, "episode.mkv")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "7", CompletedLength: "7", Selected: true}}
	}
	if err := service.HandleCompleted(context.Background(), "actual-gid", filePath); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.GID != "actual-gid" || current.Status != domain.StatusTransferPending || len(current.FinalFiles) != 1 || current.FinalFiles[0] != "episode.mkv" {
		t.Fatalf("task after completion = %#v", current)
	}
}
func TestServiceResolvesFollowedTorrentFiles(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "staging")
	downloader := &fakeDownloader{}
	backend := &fakeProvider{done: make(chan struct{})}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": backend},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	task, err := service.Create(ctx, TaskInput{
		URLs:          []string{"https://example.test/example.torrent"},
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(task.StagingPath, "selected.mkv")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(gid string) []aria2.DownloadFile {
		if gid == "gid-1" {
			return []aria2.DownloadFile{{Path: "[METADATA]example", Length: "0", CompletedLength: "0", Selected: true}}
		}
		return []aria2.DownloadFile{
			{Path: filepath.Join(task.StagingPath, "canceled.bin"), Length: "10", CompletedLength: "0", Selected: false},
			{Path: filePath, Length: "7", CompletedLength: "5", Selected: true},
		}
	}
	downloader.followedBy = func(gid string) []string {
		if gid == "gid-1" {
			return []string{"actual-gid"}
		}
		return nil
	}
	if err := service.HandleCompleted(ctx, task.GID, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.done:
	case <-time.After(2 * time.Second):
		t.Fatal("transfer worker did not run")
	}

	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusCompleted || current.GID != "actual-gid" {
		t.Fatalf("task after completion = %#v", current)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.requests) != 1 || len(backend.requests[0].Files) != 1 || backend.requests[0].Files[0] != "selected.mkv" {
		t.Fatalf("transfer requests = %#v", backend.requests)
	}
}

func TestServiceRejectsIncompleteFinalFile(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": &fakeProvider{done: make(chan struct{})}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), TaskInput{
		Type:          "torrent",
		Content:       "torrent-content",
		DestinationID: "drive",
		TargetPath:    "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(task.StagingPath, "movie.mkv")
	if err := os.WriteFile(filePath, []byte("short"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "10", CompletedLength: "5", Selected: true}}
	}
	if err := service.HandleCompleted(context.Background(), task.GID, ""); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusDownloading {
		t.Fatalf("task status = %q, want downloading", current.Status)
	}
}
func TestServiceUsesDefaultDestination(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{
			{ID: "archive", Name: "Archive", Provider: "fake"},
			{ID: "drive", Name: "Drive", Provider: "fake"},
		},
		"drive",
		filepath.Join(t.TempDir(), "staging"),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}

	task, err := service.Create(context.Background(), TaskInput{
		URLs:       []string{"https://example.test/file"},
		TargetPath: "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.DestinationID != "drive" {
		t.Fatalf("destination = %q, want drive", task.DestinationID)
	}
	destinations := service.Destinations()
	if len(destinations) != 2 || destinations[0].ID != "drive" {
		t.Fatalf("destinations = %#v, want drive first", destinations)
	}
}
