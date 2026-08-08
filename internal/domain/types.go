package domain

import (
	"fmt"
	"net/url"
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
	Proxy        string `json:"-"`
	Token        string `json:"-"`
}

type Task struct {
	ID                 string            `json:"id"`
	GID                string            `json:"gid,omitempty"`
	Type               string            `json:"type"`
	URLs               []string          `json:"urls,omitempty"`
	Content            string            `json:"content,omitempty"`
	Options            map[string]string `json:"options,omitempty"`
	DestinationID      string            `json:"destination_id"`
	TargetPath         string            `json:"target_path"`
	DownloadPath       string            `json:"download_path"`
	FinalFiles         []string          `json:"final_files"`
	Status             string            `json:"status"`
	Error              string            `json:"error,omitempty"`
	RetryCount         int               `json:"retry_count"`
	Cleanup            bool              `json:"cleanup"`
	Pause              bool              `json:"pause"`
	TransferTotalBytes int64             `json:"transfer_total_bytes"`
	TransferredBytes   int64             `json:"transferred_bytes"`
	TransferSpeed      int64             `json:"transfer_speed"`
	TransferUpdatedAt  time.Time         `json:"transfer_updated_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	CompletedAt        time.Time         `json:"completed_at,omitempty"`
}

type TaskView struct {
	ID                 string    `json:"id"`
	GID                string    `json:"gid,omitempty"`
	Type               string    `json:"type"`
	URLs               []string  `json:"urls,omitempty"`
	FileNames          []string  `json:"file_names"`
	DestinationID      string    `json:"destination_id"`
	Destination        string    `json:"destination"`
	TargetPath         string    `json:"target_path"`
	Status             string    `json:"status"`
	Error              string    `json:"error,omitempty"`
	RetryCount         int       `json:"retry_count"`
	Cleanup            bool      `json:"cleanup"`
	Pause              bool      `json:"pause"`
	TransferTotalBytes int64     `json:"transfer_total_bytes"`
	TransferredBytes   int64     `json:"transferred_bytes"`
	TransferProgress   int       `json:"transfer_progress"`
	TransferSpeed      int64     `json:"transfer_speed"`
	TransferUpdatedAt  time.Time `json:"transfer_updated_at,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
}

func (t Task) View(destinationName string) TaskView {
	return TaskView{
		ID:                 t.ID,
		GID:                t.GID,
		Type:               t.Type,
		URLs:               append([]string(nil), t.URLs...),
		FileNames:          taskFileNames(t),
		DestinationID:      t.DestinationID,
		Destination:        destinationName,
		TargetPath:         t.TargetPath,
		Status:             t.Status,
		Error:              t.Error,
		RetryCount:         t.RetryCount,
		Cleanup:            t.Cleanup,
		TransferTotalBytes: t.TransferTotalBytes,
		TransferredBytes:   t.TransferredBytes,
		TransferProgress:   transferPercent(t.TransferredBytes, t.TransferTotalBytes),
		TransferSpeed:      t.TransferSpeed,
		TransferUpdatedAt:  t.TransferUpdatedAt,
		Pause:              t.Pause,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		CompletedAt:        t.CompletedAt,
	}
}

func transferPercent(transferred, total int64) int {
	if total <= 0 || transferred <= 0 {
		return 0
	}
	if transferred >= total {
		return 100
	}
	return int(float64(transferred) / float64(total) * 100)
}

func taskFileNames(task Task) []string {
	values := append([]string(nil), task.FinalFiles...)
	if len(values) == 0 && task.Options["out"] != "" {
		values = append(values, task.Options["out"])
	}
	if len(values) == 0 {
		for _, rawURL := range task.URLs {
			parsed, err := url.Parse(rawURL)
			if err == nil && parsed.Scheme != "magnet" {
				values = append(values, parsed.Path)
			}
		}
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := path.Base(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")))
		if name == "" || name == "." || name == "/" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func NormalizeProxyURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid proxy URL: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" {
		return "", fmt.Errorf("proxy URL scheme must be http, https, or socks5")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("proxy URL requires a host")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("proxy URL cannot contain a path, query, or fragment")
	}
	parsed.Path = ""
	return parsed.String(), nil
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
