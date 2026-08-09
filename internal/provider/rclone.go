package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"aria2-transfer-gateway/internal/domain"
)

type Rclone struct {
	Binary string
}

func NewRclone(binary string) *Rclone {
	if binary == "" {
		binary = "rclone"
	}
	return &Rclone{Binary: binary}
}

func (p *Rclone) Transfer(ctx context.Context, request TransferRequest) error {
	if request.Destination.Remote == "" {
		return fmt.Errorf("rclone destination %q has no remote", request.Destination.ID)
	}
	target, err := domain.NormalizeTargetPath(request.TargetPath)
	if err != nil {
		return err
	}
	if request.Files != nil && len(request.Files) == 0 {
		return nil
	}
	remotePath := joinRemotePath(request.Destination.Root, target)
	destination := request.Destination.Remote + ":" + remotePath
	args := []string{"copy", request.SourceDir, destination}
	if request.Files != nil {
		list, err := os.CreateTemp("", "aria2-transfer-files-*.list")
		if err != nil {
			return fmt.Errorf("create rclone file list: %w", err)
		}
		listPath := list.Name()
		defer os.Remove(listPath)
		for _, file := range request.Files {
			file = path.Clean(strings.ReplaceAll(file, "\\", "/"))
			if file == "." || file == ".." || strings.HasPrefix(file, "../") || strings.HasPrefix(file, "/") {
				_ = list.Close()
				return fmt.Errorf("rclone transfer file %q is outside source directory", file)
			}
			if _, err := list.WriteString(file); err != nil {
				_ = list.Close()
				return fmt.Errorf("write rclone file list: %w", err)
			}
			if _, err := list.Write([]byte{0}); err != nil {
				_ = list.Close()
				return fmt.Errorf("write rclone file list: %w", err)
			}
		}
		if err := list.Close(); err != nil {
			return fmt.Errorf("close rclone file list: %w", err)
		}
		args = append(args, "--files-from0", listPath)
	}
	if request.Destination.RcloneConfig != "" {
		args = append(args, "--config", request.Destination.RcloneConfig)
	}
	cmd := exec.CommandContext(ctx, p.Binary, args...)
	if request.Destination.Proxy != "" {
		cmd.Env = rcloneProxyEnvironment(os.Environ(), request.Destination.Proxy)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rclone copy failed: %w: %s", err, truncateOutput(output))
	}
	return nil
}

func rcloneProxyEnvironment(environment []string, proxy string) []string {
	proxyKeys := map[string]struct{}{
		"http_proxy":  {},
		"https_proxy": {},
	}
	result := make([]string, 0, len(environment)+4)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if _, replace := proxyKeys[strings.ToLower(key)]; !replace {
			result = append(result, value)
		}
	}
	result = append(result,
		"http_proxy="+proxy,
		"https_proxy="+proxy,
		"HTTP_PROXY="+proxy,
		"HTTPS_PROXY="+proxy,
	)
	return result
}

func joinRemotePath(root, target string) string {
	parts := make([]string, 0, 2)
	for _, value := range []string{root, target} {
		value = strings.Trim(value, "/")
		if value != "" && value != "." {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return path.Join(parts...)
}

func truncateOutput(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[:limit]
	}
	return strings.TrimSpace(string(output))
}
