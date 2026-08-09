package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aria2-transfer-gateway/internal/domain"
)

func TestRcloneTransferBuildsArgumentVector(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	filesFile := filepath.Join(dir, "files")
	envFile := filepath.Join(dir, "env")
	binary := filepath.Join(dir, "rclone-fake")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$RCLONE_TEST_ARGS"
printf '%s\n' "$http_proxy" "$https_proxy" "$HTTP_PROXY" "$HTTPS_PROXY" > "$RCLONE_TEST_ENV"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--files-from0" ]; then
    cat "$2" > "$RCLONE_TEST_FILES"
    shift 2
  else
    shift
  fi
done
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RCLONE_TEST_ARGS", argsFile)
	t.Setenv("RCLONE_TEST_FILES", filesFile)
	t.Setenv("RCLONE_TEST_ENV", envFile)

	err := NewRclone(binary).Transfer(context.Background(), TransferRequest{
		SourceDir:  "/tmp/downloads/task-1",
		TargetPath: "/movies/2026",
		Files:      []string{"movie.mkv", " subtitles.srt", "#notes.srt"},
		Destination: domain.Destination{
			ID:           "drive",
			Remote:       "remote",
			Root:         "/library",
			RcloneConfig: "/etc/rclone.conf",
			Proxy:        "socks5://proxy-user:proxy-pass@proxy.example:1080",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) != 7 || args[0] != "copy" || args[1] != "/tmp/downloads/task-1" || args[2] != "remote:library/movies/2026" || args[3] != "--files-from0" || args[5] != "--config" || args[6] != "/etc/rclone.conf" {
		t.Fatalf("args = %#v", args)
	}
	fileList, err := os.ReadFile(filesFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(fileList) != "movie.mkv\x00 subtitles.srt\x00#notes.srt\x00" {
		t.Fatalf("file list = %q", fileList)
	}
	proxyEnvironment, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	wantProxyEnvironment := strings.Repeat("socks5://proxy-user:proxy-pass@proxy.example:1080\n", 4)
	if string(proxyEnvironment) != wantProxyEnvironment {
		t.Fatalf("proxy environment = %q", proxyEnvironment)
	}
}
func TestRcloneTransferRequiresConfigPath(t *testing.T) {
	err := NewRclone(filepath.Join(t.TempDir(), "missing-rclone")).Transfer(context.Background(), TransferRequest{
		TargetPath: "/movies/2026",
		Destination: domain.Destination{
			ID:     "drive",
			Remote: "remote",
		},
	})
	if err == nil || err.Error() != "rclone destination \"drive\" has no config path" {
		t.Fatalf("error = %v, want missing config path", err)
	}
}
