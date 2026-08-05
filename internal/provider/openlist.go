package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"aria2-transfer-gateway/internal/domain"
)

type OpenList struct {
	Client *http.Client
}

func NewOpenList(client *http.Client) *OpenList {
	if client == nil {
		client = &http.Client{}
	}
	return &OpenList{Client: client}
}

type countedReader struct {
	io.Reader
	count int64
}

func (r *countedReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	r.count += int64(n)
	return n, err
}

func (p *OpenList) Transfer(ctx context.Context, request TransferRequest) error {
	if request.Destination.Endpoint == "" {
		return fmt.Errorf("openlist destination %q has no endpoint", request.Destination.ID)
	}
	if request.Destination.Token == "" {
		return fmt.Errorf("openlist destination %q has no token", request.Destination.ID)
	}
	target, err := domain.NormalizeTargetPath(request.TargetPath)
	if err != nil {
		return err
	}
	var allowed map[string]struct{}
	if request.Files != nil {
		allowed = make(map[string]struct{}, len(request.Files))
		for _, file := range request.Files {
			file = filepath.ToSlash(filepath.Clean(file))
			if file != "." {
				allowed[file] = struct{}{}
			}
		}
	}
	return filepath.WalkDir(request.SourceDir, func(localPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(request.SourceDir, localPath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if allowed != nil {
			if _, ok := allowed[relative]; !ok {
				return nil
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("openlist provider does not upload symlink %q", localPath)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("openlist provider cannot upload %q", localPath)
		}
		remotePath := joinOpenListPath(request.Destination.Mount, target, relative)
		return p.uploadFile(ctx, request.Destination, localPath, remotePath, info.Size())
	})
}

func (p *OpenList) uploadFile(ctx context.Context, destination domain.Destination, localPath, remotePath string, size int64) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %q: %w", localPath, err)
	}
	defer file.Close()

	endpoint := strings.TrimRight(destination.Endpoint, "/") + "/api/fs/put"
	countedBody := &countedReader{Reader: io.NewSectionReader(file, 0, size)}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, countedBody)
	if err != nil {
		return fmt.Errorf("create OpenList upload request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Authorization", destination.Token)
	req.Header.Set("File-Path", remotePath)
	req.Header.Set("As-Task", "false")
	req.Header.Set("Overwrite", "true")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("upload %q to OpenList: %w", localPath, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return fmt.Errorf("read OpenList response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenList upload returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if countedBody.count != size {
		return fmt.Errorf("OpenList upload sent %d bytes, want %d", countedBody.count, size)
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if len(responseBody) > 0 && json.Unmarshal(responseBody, &response) == nil && response.Code != 0 && response.Code != http.StatusOK {
		return fmt.Errorf("OpenList upload failed (%d): %s", response.Code, response.Message)
	}
	return nil
}

func joinOpenListPath(mount, target, relative string) string {
	parts := []string{mount, target, relative}
	result := make([]string, 0, len(parts))
	for _, value := range parts {
		value = strings.Trim(value, "/")
		if value != "" && value != "." {
			result = append(result, value)
		}
	}
	return "/" + strings.Join(result, "/")
}
