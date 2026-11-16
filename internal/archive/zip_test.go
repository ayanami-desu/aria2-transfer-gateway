package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractZIPsKeepsSingleExtractedFolder(t *testing.T) {
	root := t.TempDir()
	inner := zipFixture(t, map[string]string{"bonus.txt": "bonus"})
	outer := zipFixture(t, map[string]string{
		"show/episode.txt": "episode",
		"show/inner.zip":   string(inner),
	})
	writeTestFile(t, filepath.Join(root, "show.zip"), string(outer))

	got, err := ExtractZIPs(context.Background(), root, []string{"show.zip"}, "show.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	want := []string{"show/bonus.txt", "show/episode.txt"}
	if !equalStrings(got, want) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, want)
	}
	for relative, content := range map[string]string{
		"show/bonus.txt":   "bonus",
		"show/episode.txt": "episode",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %q: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("%q content = %q, want %q", relative, data, content)
		}
	}
}

func TestExtractZIPsWrapsLooseFilesInTaskFolder(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "pack.zip"), string(zipFixture(t, map[string]string{
		"movie.mkv": "movie",
		"subs.srt":  "subs",
	})))

	got, err := ExtractZIPs(context.Background(), root, []string{"pack.zip"}, "pack.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	want := []string{"pack/movie.mkv", "pack/subs.srt"}
	if !equalStrings(got, want) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, want)
	}
	for relative, content := range map[string]string{
		"pack/movie.mkv": "movie",
		"pack/subs.srt":  "subs",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %q: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("%q content = %q, want %q", relative, data, content)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "pack.zip")); err != nil {
		t.Fatalf("archive stat error = %v, want archive kept", err)
	}
}

func TestExtractZIPsWrapsOtherDownloadedFilesToo(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeTestFile(t, filepath.Join(root, "show.zip"), string(zipFixture(t, map[string]string{
		"show/episode.txt": "episode",
	})))

	got, err := ExtractZIPs(context.Background(), root, []string{"keep.txt", "show.zip"}, "show.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	want := []string{"show/episode.txt", "show/keep.txt"}
	if !equalStrings(got, want) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, want)
	}
	for relative, content := range map[string]string{
		"show/episode.txt": "episode",
		"show/keep.txt":    "keep",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %q: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("%q content = %q, want %q", relative, data, content)
		}
	}
}

func TestExtractZIPsWrapsMultipleArchivesInOneTaskFolder(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.zip"), string(zipFixture(t, map[string]string{"a.txt": "a"})))
	writeTestFile(t, filepath.Join(root, "b.zip"), string(zipFixture(t, map[string]string{"b.txt": "b"})))

	got, err := ExtractZIPs(context.Background(), root, []string{"a.zip", "b.zip"}, "bundle.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	want := []string{"bundle/a.txt", "bundle/b.txt"}
	if !equalStrings(got, want) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, want)
	}
	for relative, content := range map[string]string{
		"bundle/a.txt": "a",
		"bundle/b.txt": "b",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %q: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("%q content = %q, want %q", relative, data, content)
		}
	}
}

func TestExtractZIPsMergesLooseFilesIntoExistingFolder(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "pack.zip"), string(zipFixture(t, map[string]string{
		"pack/movie.txt": "movie",
		"readme.txt":     "readme",
	})))

	got, err := ExtractZIPs(context.Background(), root, []string{"pack.zip"}, "pack.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	want := []string{"pack/movie.txt", "pack/readme.txt"}
	if !equalStrings(got, want) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, want)
	}
	for relative, content := range map[string]string{
		"pack/movie.txt":  "movie",
		"pack/readme.txt": "readme",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %q: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("%q content = %q, want %q", relative, data, content)
		}
	}
}

func TestExtractZIPsRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "bad.zip"), string(zipFixture(t, map[string]string{"../escape.txt": "escape"})))

	_, err := ExtractZIPs(context.Background(), root, []string{"bad.zip"}, "bad.zip")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("ExtractZIPs() error = %v, want path traversal error", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("escape.txt stat error = %v, want file not created", statErr)
	}
}

func TestExtractZIPsRejectsUnsafeTaskFolderName(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "pack.zip"), string(zipFixture(t, map[string]string{"movie.txt": "movie"})))

	_, err := ExtractZIPs(context.Background(), root, []string{"pack.zip"}, "../escape")
	if err == nil || !strings.Contains(err.Error(), "task folder") {
		t.Fatalf("ExtractZIPs() error = %v, want task folder error", err)
	}
}

func TestExtractZIPsRejectsOverwritingDownloadedFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeTestFile(t, filepath.Join(root, "pack.zip"), string(zipFixture(t, map[string]string{"keep.txt": "overwrite"})))

	_, err := ExtractZIPs(context.Background(), root, []string{"keep.txt", "pack.zip"}, "pack.zip")
	if err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("ExtractZIPs() error = %v, want overwrite error", err)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "keep.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "keep" {
		t.Fatalf("keep.txt content = %q, want original", data)
	}
}

func TestExtractZIPsRejectsSymlinkEntry(t *testing.T) {
	root := t.TempDir()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "link", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "links.zip"), buffer.String())

	_, err = ExtractZIPs(context.Background(), root, []string{"links.zip"}, "links.zip")
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("ExtractZIPs() error = %v, want symbolic link error", err)
	}
}

func TestExtractZIPsDecodesGBKEntryNames(t *testing.T) {
	root := t.TempDir()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{
		Name:    string([]byte{0xb2, 0xe2, 0xca, 0xd4}) + ".txt",
		Method:  zip.Store,
		NonUTF8: true,
	}
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "gbk.zip"), buffer.String())

	got, err := ExtractZIPs(context.Background(), root, []string{"gbk.zip"}, "gbk.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	if !equalStrings(got, []string{"gbk/测试.txt"}) {
		t.Fatalf("ExtractZIPs() = %#v, want [gbk/测试.txt]", got)
	}
}

func TestExtractZIPsLeavesListWithoutArchivesUnchanged(t *testing.T) {
	files := []string{"one.txt", "nested/two.txt"}
	got, err := ExtractZIPs(context.Background(), t.TempDir(), files, "unused.zip")
	if err != nil {
		t.Fatalf("ExtractZIPs() error = %v", err)
	}
	if !equalStrings(got, files) {
		t.Fatalf("ExtractZIPs() = %#v, want %#v", got, files)
	}
}

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
