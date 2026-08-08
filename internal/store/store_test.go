package store

import (
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
	task := domain.Task{
		ID:            "task-1",
		GID:           "gid-1",
		Type:          "urls",
		URLs:          []string{"https://example.test/file"},
		Options:       map[string]string{"out": "file"},
		DestinationID: "drive",
		TargetPath:    "/movies",
		StagingPath:   "/tmp/task-1",
		FinalFiles:    []string{"file"},
		Status:        domain.StatusDownloading,
		Cleanup:       true,
		Pause:         true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
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
	initial := domain.Destination{ID: "openlist", Name: "OpenList", Provider: "openlist", Endpoint: "https://files.example.test", Mount: "/drive", Token: "secret-token"}
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
	if stored.Token != initial.Token {
		t.Fatalf("stored token = %q, want persisted token", stored.Token)
	}
}
