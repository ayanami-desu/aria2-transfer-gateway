package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"aria2-transfer-gateway/internal/domain"
)

func TestStorePersistsAndUpdatesTasks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.db")
	taskStore, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := domain.Task{ID: "task-1",
		GID:           "gid-1",
		Type:          "urls",
		URLs:          []string{"https://example.test/file"},
		Options:       map[string]string{"out": "file"},
		DestinationID: "drive",
		TargetPath:    "/movies", DownloadPath: "/tmp/task-1", FinalFiles: []string{"file"},
		Status:    domain.StatusDownloading,
		Cleanup:   true,
		Pause:     true,
		CreatedAt: now,
		UpdatedAt: now}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	created, err := taskStore.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Options["out"] != "file" {
		t.Fatalf("created options = %#v", created.Options)
	}
	if _, err := taskStore.Update(task.ID, func(current *domain.Task) error {
		current.Status = domain.StatusTransferPending
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.FindByGID("gid-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusTransferPending {
		t.Fatalf("status = %q, want %q", got.Status, domain.StatusTransferPending)
	}
	if got.URLs[0] != task.URLs[0] || got.Options["out"] != "file" || !got.Cleanup || !got.Pause {
		t.Fatalf("task fields were not persisted: %#v", got)
	}
	if got.FinalFiles[0] != "file" {
		t.Fatalf("final files were not persisted: %#v", got.FinalFiles)
	}
}

func TestStoreFiltersTasks(t *testing.T) {
	taskStore, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	now := time.Now().UTC()
	tasks := []domain.Task{
		{ID: "failed-movie", DestinationID: "drive", TargetPath: "/movies", Status: domain.StatusFailed, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
		{ID: "failed-book", DestinationID: "drive", TargetPath: "/books", Status: domain.StatusFailed, CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "completed-movie", DestinationID: "backup", TargetPath: "/movies", Status: domain.StatusCompleted, CreatedAt: now, UpdatedAt: now},
	}
	for _, task := range tasks {
		if err := taskStore.Create(task); err != nil {
			t.Fatal(err)
		}
	}

	filtered, err := taskStore.ListFiltered(TaskFilter{
		Statuses:      []string{domain.StatusFailed},
		DestinationID: "drive",
		Query:         "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "failed-movie" {
		t.Fatalf("filtered tasks = %#v", filtered)
	}
	limited, err := taskStore.ListFiltered(TaskFilter{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited tasks = %d, want 2", len(limited))
	}
}

func TestStorePersistsDestinationSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.db")
	taskStore, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	initial := domain.Destination{ID: "openlist", Name: "OpenList", Provider: "openlist", Endpoint: "https://files.example.test", Mount: "/drive", Token: "secret-token", Proxy: "socks5://proxy-user:proxy-pass@proxy.example:1080"}
	if err := taskStore.InitializeDestinations([]domain.Destination{initial}, initial.ID); err != nil {
		t.Fatal(err)
	}
	second := domain.Destination{ID: "rclone", Name: "Rclone", Provider: "rclone", Remote: "backup", Root: "/archive", RcloneConfig: "/config/rclone.conf"}
	if err := taskStore.CreateDestination(second); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.SetDefaultDestination(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	destinations, defaultID, err := reopened.DestinationSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(destinations) != 2 || defaultID != second.ID {
		t.Fatalf("destination settings = %#v, default = %q", destinations, defaultID)
	}
	stored, err := reopened.GetDestination(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token != initial.Token || stored.Proxy != initial.Proxy {
		t.Fatalf("stored secrets = token %q, proxy %q", stored.Token, stored.Proxy)
	}
}

func TestStoreMigratesLegacySchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.db")
	database, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
CREATE TABLE destinations (id TEXT PRIMARY KEY, name TEXT NOT NULL, provider TEXT NOT NULL, endpoint TEXT NOT NULL DEFAULT '', mount TEXT NOT NULL DEFAULT '', remote TEXT NOT NULL DEFAULT '', root TEXT NOT NULL DEFAULT '', rclone_config TEXT NOT NULL DEFAULT '', token TEXT NOT NULL DEFAULT '');
CREATE TABLE tasks (id TEXT PRIMARY KEY, gid TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT '', urls TEXT, content TEXT NOT NULL DEFAULT '', options TEXT, destination_id TEXT NOT NULL DEFAULT '', target_path TEXT NOT NULL DEFAULT '/', staging_path TEXT NOT NULL DEFAULT '', final_files TEXT, status TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', retry_count INTEGER NOT NULL DEFAULT 0, cleanup INTEGER NOT NULL DEFAULT 0, pause INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, completed_at TEXT);
`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO tasks (id, staging_path, created_at, updated_at) VALUES ('legacy-path', '/staging/legacy-path', '2026-08-08T00:00:00Z', '2026-08-08T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	taskStore, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer taskStore.Close()
	legacyTask, err := taskStore.Get("legacy-path")
	if err != nil {
		t.Fatal(err)
	}
	if legacyTask.DownloadPath != "/staging/legacy-path" {
		t.Fatalf("migrated download path = %q", legacyTask.DownloadPath)
	}
	destination := domain.Destination{ID: "proxy", Name: "Proxy", Provider: "rclone", Remote: "backup", Proxy: "https://proxy.example:8443"}
	if err := taskStore.CreateDestination(destination); err != nil {
		t.Fatal(err)
	}
	stored, err := taskStore.GetDestination(destination.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Proxy != destination.Proxy {
		t.Fatalf("migrated proxy = %q, want %q", stored.Proxy, destination.Proxy)
	}
	now := time.Now().UTC()
	task := domain.Task{ID: "progress", GID: "gid-progress", Type: "urls", DestinationID: destination.ID, TargetPath: "/", Status: domain.StatusTransferring, TransferTotalBytes: 100, TransferredBytes: 40, TransferSpeed: 10, TransferUpdatedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := taskStore.Create(task); err != nil {
		t.Fatal(err)
	}
	storedTask, err := taskStore.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedTask.TransferTotalBytes != 100 || storedTask.TransferredBytes != 40 || storedTask.TransferSpeed != 10 || !storedTask.TransferUpdatedAt.Equal(now) {
		t.Fatalf("migrated task progress = %#v", storedTask)
	}
}
