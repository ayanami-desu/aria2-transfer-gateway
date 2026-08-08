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
	status     func(string) aria2.DownloadStatus
	remove     func(string) error
	add        func(string)
}

func (d fakeDownloader) Remove(_ context.Context, gid string) error {
	if d.remove == nil {
		return nil
	}
	return d.remove(gid)
}

func (d fakeDownloader) AddURI(_ context.Context, _ []string, directory string, _ bool, _ map[string]string) (string, error) {
	if d.add != nil {
		d.add(directory)
	}
	return "gid-1", nil
}

func (d fakeDownloader) AddTorrent(_ context.Context, _ string, directory string, _ bool, _ map[string]string) (string, error) {
	if d.add != nil {
		d.add(directory)
	}
	return "gid-1", nil
}

func (d fakeDownloader) AddMetalink(_ context.Context, _ string, directory string, _ bool, _ map[string]string) (string, error) {
	if d.add != nil {
		d.add(directory)
	}
	return "gid-1", nil
}

func (d fakeDownloader) GetFiles(_ context.Context, gid string) ([]aria2.DownloadFile, error) {
	if d.getFiles == nil {
		return []aria2.DownloadFile{}, nil
	}
	return d.getFiles(gid), nil
}
func (d fakeDownloader) GetStatus(_ context.Context, gid string) (aria2.DownloadStatus, error) {
	if d.status == nil {
		return aria2.DownloadStatus{Status: "complete", CompletedLength: "1", TotalLength: "1"}, nil
	}
	return d.status(gid), nil
}

func (d fakeDownloader) GetFollowedBy(_ context.Context, gid string) ([]string, error) {
	if d.followedBy == nil {
		return nil, nil
	}
	return d.followedBy(gid), nil
}

type fakeProvider struct {
	mu        sync.Mutex
	requests  []provider.TransferRequest
	done      chan struct{}
	started   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
	progress  []provider.TransferProgress
}

func (p *fakeProvider) Transfer(ctx context.Context, request provider.TransferRequest) error {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.started != nil {
		close(p.started)
	}
	if request.OnProgress != nil {
		for _, progress := range p.progress {
			request.OnProgress(progress)
		}
	}
	if p.done != nil {
		close(p.done)
	}
	if p.release == nil {
		return nil
	}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		if p.cancelled != nil {
			close(p.cancelled)
		}
		return ctx.Err()
	}
}

func TestServiceDownloadsDirectlyIntoDownloadRoot(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "downloads")
	var aria2Directory string
	service, err := NewService(
		taskStore,
		fakeDownloader{add: func(directory string) { aria2Directory = directory }},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"drive",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), TaskInput{URLs: []string{"https://example.test/file.bin"}, TargetPath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(downloadRoot, task.ID)
	if task.DownloadPath != want || aria2Directory != want {
		t.Fatalf("download paths = task %q, aria2 %q; want %q", task.DownloadPath, aria2Directory, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("download directory stat = %v, info = %v", err, info)
	}
}

func TestServiceRetriesAnyTaskStatus(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
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
		task := domain.Task{ID: "retry-" + status,
			DestinationID: "drive",
			TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, status), FinalFiles: []string{"file"},
			Status:      status,
			Error:       "previous error",
			CreatedAt:   now,
			UpdatedAt:   now,
			CompletedAt: now}
		if err := taskStore.Create(task); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(task.DownloadPath, "file"), []byte("payload"), 0o640); err != nil {
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

func TestServiceRejectsUploadRetryWithoutFinalFiles(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := domain.Task{ID: "retry-without-final-files",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, "downloading"), Status: domain.StatusDownloading,
		CreatedAt: now,
		UpdatedAt: now}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Retry(context.Background(), task.ID, RetryModeUpload); err == nil {
		t.Fatal("upload retry succeeded without a final file snapshot")
	}
	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusDownloading {
		t.Fatalf("task status = %q, want downloading", current.Status)
	}
}

func TestServiceFullRetryReplacesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
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
		downloadRoot,
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
	oldDownloadPath := original.DownloadPath
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
	if _, err := os.Stat(oldDownloadPath); !os.IsNotExist(err) {
		t.Fatalf("old download path still exists: %v", err)
	}
	if info, err := os.Stat(retried.DownloadPath); err != nil || !info.IsDir() {
		t.Fatalf("new download path = %q, stat error = %v", retried.DownloadPath, err)
	}
}

func TestServiceDeleteCancelsAndRemovesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
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
		downloadRoot,
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
	if err := os.WriteFile(filepath.Join(task.DownloadPath, "file"), []byte("data"), 0o600); err != nil {
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
	if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
		t.Fatalf("download path still exists: %v", err)
	}
}

func TestServiceDeleteIgnoresMissingTaskData(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{remove: func(string) error {
			return aria2.ErrGIDNotFound
		}},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "orphaned-task",
		GID:           "missing-gid",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(t.TempDir(), "already-gone"), Status: domain.StatusFailed,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC()}
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

func TestServiceUploadRetryRequiresDownloadDirectory(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "missing-download",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, "missing-download"), Status: domain.StatusFailed,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC()}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Retry(context.Background(), task.ID, RetryModeUpload); err == nil {
		t.Fatal("upload retry succeeded without a download directory")
	}
	current, err := taskStore.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusFailed {
		t.Fatalf("task status = %q, want failed", current.Status)
	}
}

func TestServiceTransfersCompletedTaskAndCleansDownload(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeProvider{done: make(chan struct{}), progress: []provider.TransferProgress{{TotalBytes: 7}, {TotalBytes: 7, TransferredBytes: 3}, {TotalBytes: 7, TransferredBytes: 7}}}
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": backend},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "download"),
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
	filePath := filepath.Join(task.DownloadPath, "file.bin")
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
			if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
				t.Fatalf("download path still exists: %v", err)
			}
			if current.TransferTotalBytes != 7 || current.TransferredBytes != 7 || current.TransferSpeed != 0 || current.TransferUpdatedAt.IsZero() {
				t.Fatalf("transfer progress = total %d, transferred %d, speed %d, updated %v", current.TransferTotalBytes, current.TransferredBytes, current.TransferSpeed, current.TransferUpdatedAt)
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
		filepath.Join(t.TempDir(), "download"),
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
	for _, name := range []string{"movie.mkv", "metadata.torrent", "state.aria2", "partial.bin"} {
		if err := os.WriteFile(filepath.Join(task.DownloadPath, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{
			{Path: filepath.Join(task.DownloadPath, "movie.mkv"), Length: "9", CompletedLength: "9", Selected: true},
			{Path: "[METADATA]metadata", Length: "0", CompletedLength: "0", Selected: true},
			{Path: filepath.Join(task.DownloadPath, "state.aria2"), Length: "10", CompletedLength: "0", Selected: false},
			{Path: filepath.Join(task.DownloadPath, "partial.bin"), Length: "10", CompletedLength: "0", Selected: false},
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
	if _, err := os.Stat(filepath.Join(task.DownloadPath, "metadata.torrent")); !os.IsNotExist(err) {
		t.Fatalf("metadata.torrent stat error = %v, want file removed", err)
	}
	if _, err := os.Stat(filepath.Join(task.DownloadPath, "state.aria2")); !os.IsNotExist(err) {
		t.Fatalf("state.aria2 stat error = %v, want file removed", err)
	}
	if _, err := os.Stat(filepath.Join(task.DownloadPath, "movie.mkv")); err != nil {
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
		filepath.Join(t.TempDir(), "download"),
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
		filepath.Join(t.TempDir(), "download"),
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
	filePath := filepath.Join(task.DownloadPath, "episode.mkv")
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
	downloadRoot := filepath.Join(t.TempDir(), "download")
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
	filePath := filepath.Join(task.DownloadPath, "selected.mkv")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(gid string) []aria2.DownloadFile {
		if gid == "gid-1" {
			return []aria2.DownloadFile{{Path: "[METADATA]example", Length: "0", CompletedLength: "0", Selected: true}}
		}
		return []aria2.DownloadFile{
			{Path: filepath.Join(task.DownloadPath, "canceled.bin"), Length: "10", CompletedLength: "0", Selected: false},
			{Path: filePath, Length: "7", CompletedLength: "7", Selected: true},
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

	deadline := time.Now().Add(2 * time.Second)
	var current domain.Task
	for time.Now().Before(deadline) {
		current, err = service.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == domain.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
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
		filepath.Join(t.TempDir(), "download"),
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
	filePath := filepath.Join(task.DownloadPath, "movie.mkv")
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
func TestServiceAcceptsCompletedSelectedFileWithZeroFileProgress(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": &fakeProvider{done: make(chan struct{})}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
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
	filePath := filepath.Join(task.DownloadPath, "selected.mkv")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "7", CompletedLength: "0", Selected: true}}
	}
	downloader.status = func(string) aria2.DownloadStatus {
		return aria2.DownloadStatus{Status: "complete", CompletedLength: "7", TotalLength: "7"}
	}

	if err := service.HandleCompleted(context.Background(), task.GID, ""); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusTransferPending || len(current.FinalFiles) != 1 || current.FinalFiles[0] != "selected.mkv" {
		t.Fatalf("task after completion = %#v", current)
	}
}

func TestServiceRejectsPreallocatedIncompleteFinalFile(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloader := &fakeDownloader{}
	service, err := NewService(
		taskStore,
		downloader,
		map[string]provider.Provider{"fake": &fakeProvider{done: make(chan struct{})}},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		filepath.Join(t.TempDir(), "download"),
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
	filePath := filepath.Join(task.DownloadPath, "movie.mkv")
	if err := os.WriteFile(filePath, make([]byte, 10), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "10", CompletedLength: "5", Selected: true}}
	}
	downloader.status = func(string) aria2.DownloadStatus {
		return aria2.DownloadStatus{Status: "active", CompletedLength: "5", TotalLength: "10"}
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
		filepath.Join(t.TempDir(), "download"),
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
func TestServiceDeleteByGIDCancelsAndRemovesTask(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := domain.Task{ID: "gid-delete-task",
		GID:           "gid-delete",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, "gid-delete-task"), Status: domain.StatusDownloading,
		CreatedAt: now,
		UpdatedAt: now}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.DownloadPath, "partial.bin"), []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteByGID(context.Background(), task.GID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
		t.Fatalf("download path still exists: %v", err)
	}
}

func TestServiceDeleteCancelsActiveTransferBeforeCleanup(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	downloader := &fakeDownloader{}
	backend := &fakeProvider{
		done:      make(chan struct{}),
		started:   make(chan struct{}),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}),
	}
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
		URLs:          []string{"https://example.test/file"},
		DestinationID: "drive",
		TargetPath:    "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(task.DownloadPath, "file.bin")
	if err := os.WriteFile(filePath, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	downloader.getFiles = func(string) []aria2.DownloadFile {
		return []aria2.DownloadFile{{Path: filePath, Length: "7", CompletedLength: "7", Selected: true}}
	}
	if err := service.HandleCompleted(ctx, task.GID, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.started:
	case <-time.After(2 * time.Second):
		t.Fatal("transfer worker did not start")
	}
	if err := service.Delete(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("transfer provider was not cancelled")
	}
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
		t.Fatalf("download path still exists: %v", err)
	}
}

func TestServiceRecoversDeletingTaskOnStart(t *testing.T) {
	taskStore, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	downloadRoot := filepath.Join(t.TempDir(), "download")
	service, err := NewService(
		taskStore,
		fakeDownloader{},
		map[string]provider.Provider{},
		[]domain.Destination{{ID: "drive", Name: "Drive", Provider: "fake"}},
		"",
		downloadRoot,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "deleting-on-start",
		GID:           "gid-deleting-on-start",
		DestinationID: "drive",
		TargetPath:    "/", DownloadPath: filepath.Join(downloadRoot, "deleting-on-start"), Status: domain.StatusDeleting,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC()}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(task.DownloadPath, 0o750); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	if _, err := taskStore.Get(task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("task lookup error = %v, want not found", err)
	}
	if _, err := os.Stat(task.DownloadPath); !os.IsNotExist(err) {
		t.Fatalf("download path still exists: %v", err)
	}
}

func TestServiceManagesPersistentDestinationsAndDynamicDefault(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.db")
	taskStore, err := store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	downloadRoot := filepath.Join(t.TempDir(), "download")
	legacy := domain.Destination{ID: "legacy", Name: "Legacy", Provider: "rclone", Remote: "legacy"}
	service, err := NewService(taskStore, fakeDownloader{}, map[string]provider.Provider{"openlist": &fakeProvider{}, "rclone": &fakeProvider{}}, []domain.Destination{legacy}, legacy.ID, downloadRoot, 1)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := service.CreateDestination(domain.Destination{ID: "managed", Name: "Managed", Provider: "openlist", Endpoint: "https://files.example.test/", Mount: "/drive", Token: "secret-token", Proxy: "socks5://proxy-user:proxy-pass@proxy.example:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if managed.Endpoint != "https://files.example.test" {
		t.Fatalf("normalized endpoint = %q", managed.Endpoint)
	}
	if err := service.SetDefaultDestination(managed.ID); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), TaskInput{URLs: []string{"https://example.test/media/movie.mkv"}, TargetPath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if created.DestinationID != managed.ID {
		t.Fatalf("default destination = %q, want %q", created.DestinationID, managed.ID)
	}
	view := service.View(created)
	if len(view.FileNames) != 1 || view.FileNames[0] != "movie.mkv" {
		t.Fatalf("task file names = %#v", view.FileNames)
	}
	explicit, err := service.Create(context.Background(), TaskInput{URLs: []string{"https://example.test/explicit.bin"}, DestinationID: legacy.ID, TargetPath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.DestinationID != legacy.ID {
		t.Fatalf("explicit destination = %q, want %q", explicit.DestinationID, legacy.ID)
	}
	if _, err := service.UpdateDestination(managed.ID, domain.Destination{Name: "Managed Updated", Provider: "openlist", Endpoint: "https://files.example.test", Mount: "/drive"}, false); err != nil {
		t.Fatal(err)
	}
	stored, err := taskStore.GetDestination(managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token != "secret-token" || stored.Proxy != managed.Proxy {
		t.Fatalf("blank update replaced secrets: token %q, proxy %q", stored.Token, stored.Proxy)
	}
	if err := service.DeleteDestination(managed.ID); !errors.Is(err, ErrDefaultDestination) {
		t.Fatalf("delete default error = %v", err)
	}
	if err := taskStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted, err := NewService(reopened, fakeDownloader{}, map[string]provider.Provider{"openlist": &fakeProvider{}, "rclone": &fakeProvider{}}, nil, "", downloadRoot, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.DefaultDestinationID() != managed.ID || len(restarted.Destinations()) != 2 {
		t.Fatalf("restarted destinations = %#v, default = %q", restarted.Destinations(), restarted.DefaultDestinationID())
	}
}
