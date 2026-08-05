package domain

import (
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	StatusQueued          = "queued"
	StatusDownloading     = "downloading"
	StatusTransferPending = "transfer_pending"
	StatusTransferring    = "transferring"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusDeleting        = "deleting"
)

type Destination struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Endpoint     string `json:"endpoint,omitempty"`
	Mount        string `json:"mount,omitempty"`
	Remote       string `json:"remote,omitempty"`
	Root         string `json:"root,omitempty"`
	RcloneConfig string `json:"-"`
	Token        string `json:"-"`
}

type Task struct {
	ID            string            `json:"id"`
	GID           string            `json:"gid,omitempty"`
	Type          string            `json:"type"`
	URLs          []string          `json:"urls,omitempty"`
	Content       string            `json:"content,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
	DestinationID string            `json:"destination_id"`
	TargetPath    string            `json:"target_path"`
	StagingPath   string            `json:"staging_path"`
	FinalFiles    []string          `json:"final_files"`
	Status        string            `json:"status"`
	Error         string            `json:"error,omitempty"`
	RetryCount    int               `json:"retry_count"`
	Cleanup       bool              `json:"cleanup"`
	Pause         bool              `json:"pause"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	CompletedAt   time.Time         `json:"completed_at,omitempty"`
}

type TaskView struct {
	ID            string    `json:"id"`
	GID           string    `json:"gid,omitempty"`
	Type          string    `json:"type"`
	URLs          []string  `json:"urls,omitempty"`
	DestinationID string    `json:"destination_id"`
	Destination   string    `json:"destination"`
	TargetPath    string    `json:"target_path"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	RetryCount    int       `json:"retry_count"`
	Cleanup       bool      `json:"cleanup"`
	Pause         bool      `json:"pause"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
}

func (t Task) View(destinationName string) TaskView {
	return TaskView{
		ID:            t.ID,
		GID:           t.GID,
		Type:          t.Type,
		URLs:          append([]string(nil), t.URLs...),
		DestinationID: t.DestinationID,
		Destination:   destinationName,
		TargetPath:    t.TargetPath,
		Status:        t.Status,
		Error:         t.Error,
		RetryCount:    t.RetryCount,
		Cleanup:       t.Cleanup,
		Pause:         t.Pause,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		CompletedAt:   t.CompletedAt,
	}
}

func NormalizeTargetPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "/" {
		return "/", nil
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("target path contains NUL")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("target path cannot contain ..")
		}
	}
	clean := path.Clean("/" + strings.TrimPrefix(value, "/"))
	if clean == "/.." || strings.HasPrefix(clean, "/../") {
		return "", fmt.Errorf("target path escapes destination root")
	}
	return clean, nil
}
