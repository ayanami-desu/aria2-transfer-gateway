// Package archive extracts completed downloads before they are uploaded.
package archive

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// maxArchiveDepth bounds recursive extraction of zip archives inside zip
// archives so a crafted or accidental nesting chain cannot run forever.
const maxArchiveDepth = 8

const copyBufferSize = 32 * 1024

type archiveJob struct {
	path  string
	depth int
}

type extractor struct {
	ctx      context.Context
	root     string
	files    map[string]struct{}
	download map[string]struct{}
	queue    []archiveJob
	queued   map[string]struct{}
	buffer   []byte
}

// ExtractZIPs extracts every zip archive in files, which are paths relative to
// root, and returns the upload file list with each archive replaced by the
// files it contains. Nested zip archives are extracted recursively.
//
// When the extraction result is not a single top-level directory, all upload
// files are moved into a directory named after taskName. A trailing .zip is
// removed from the folder name so it cannot collide with the archive file.
//
// Extraction is done in place. Files produced by a previous failed attempt are
// overwritten, but an archive may not overwrite a file that aria2 downloaded.
func ExtractZIPs(ctx context.Context, root string, files []string, taskName string) ([]string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve archive extraction root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("stat archive extraction root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("archive extraction root is not a directory")
	}
	extractor := &extractor{
		ctx:      ctx,
		root:     absoluteRoot,
		files:    make(map[string]struct{}, len(files)),
		download: make(map[string]struct{}, len(files)),
		queued:   make(map[string]struct{}, len(files)),
		buffer:   make([]byte, copyBufferSize),
	}
	firstArchive := ""
	for _, file := range files {
		relative, err := cleanRelativePath(file)
		if err != nil {
			return nil, fmt.Errorf("upload file %q: %w", file, err)
		}
		if isZIP(relative) {
			if firstArchive == "" {
				firstArchive = relative
			}
			extractor.enqueue(relative, 1)
			continue
		}
		extractor.download[relative] = struct{}{}
		extractor.files[relative] = struct{}{}
	}
	if len(extractor.queue) == 0 {
		return append([]string(nil), files...), nil
	}
	if strings.TrimSpace(taskName) == "" && firstArchive != "" {
		base := path.Base(firstArchive)
		taskName = strings.TrimSuffix(base, path.Ext(base))
	}
	for len(extractor.queue) > 0 {
		job := extractor.queue[0]
		extractor.queue = extractor.queue[1:]
		if err := extractor.extract(job); err != nil {
			return nil, err
		}
	}
	if err := extractor.wrap(taskName); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(extractor.files))
	for file := range extractor.files {
		result = append(result, file)
	}
	sort.Strings(result)
	return result, nil
}

func (e *extractor) wrap(taskName string) error {
	if len(e.files) == 0 {
		return nil
	}
	topLevel := e.topLevelEntries()
	if len(topLevel) == 1 {
		info, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(topLevel[0])))
		if err != nil {
			return fmt.Errorf("stat extracted path %q: %w", topLevel[0], err)
		}
		if info.IsDir() {
			return nil
		}
	}
	folder, err := taskFolderName(taskName)
	if err != nil {
		return err
	}
	wrapperPath, err := e.target(folder)
	if err != nil {
		return fmt.Errorf("task folder %q: %w", folder, err)
	}
	info, err := os.Lstat(wrapperPath)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("task folder %q conflicts with an existing file", folder)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(wrapperPath, 0o755); err != nil {
			return fmt.Errorf("create task folder %q: %w", folder, err)
		}
	default:
		return fmt.Errorf("inspect task folder %q: %w", folder, err)
	}
	for _, top := range topLevel {
		if top == folder {
			continue
		}
		if err := e.ctx.Err(); err != nil {
			return err
		}
		source, err := e.target(top)
		if err != nil {
			return fmt.Errorf("move extracted path %q: %w", top, err)
		}
		destination, err := e.target(path.Join(folder, top))
		if err != nil {
			return fmt.Errorf("move extracted path %q: %w", top, err)
		}
		if err := movePath(source, destination); err != nil {
			return fmt.Errorf("move extracted path %q into task folder %q: %w", top, folder, err)
		}
	}
	wrapped := make(map[string]struct{}, len(e.files))
	for file := range e.files {
		if file == folder || strings.HasPrefix(file, folder+"/") {
			wrapped[file] = struct{}{}
			continue
		}
		wrapped[path.Join(folder, file)] = struct{}{}
	}
	e.files = wrapped
	return nil
}

func (e *extractor) topLevelEntries() []string {
	seen := make(map[string]struct{}, len(e.files))
	for file := range e.files {
		top := file
		if index := strings.IndexByte(file, '/'); index >= 0 {
			top = file[:index]
		}
		if top != "" {
			seen[top] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for top := range seen {
		result = append(result, top)
	}
	sort.Strings(result)
	return result
}

func taskFolderName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", errors.New("task name is required to name the extracted folder")
	}
	if extension := path.Ext(name); strings.EqualFold(extension, ".zip") {
		name = strings.TrimSpace(strings.TrimSuffix(name, extension))
	}
	if name == "" {
		return "", errors.New("task name has no extracted folder name")
	}
	cleaned, err := cleanRelativePath(name)
	if err != nil {
		return "", fmt.Errorf("task folder name %q: %w", name, err)
	}
	if strings.Contains(cleaned, "/") {
		return "", fmt.Errorf("task folder name %q must be a single path element", name)
	}
	return cleaned, nil
}

func movePath(source, destination string) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("destination %q already exists", destination)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(source, destination)
	}
	destinationInfo, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return os.Rename(source, destination)
	}
	if err != nil {
		return err
	}
	if !destinationInfo.IsDir() {
		return fmt.Errorf("destination %q is not a directory", destination)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := movePath(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(source)
}

func (e *extractor) enqueue(relative string, depth int) {
	if _, exists := e.queued[relative]; exists {
		return
	}
	e.queued[relative] = struct{}{}
	e.queue = append(e.queue, archiveJob{path: relative, depth: depth})
}

func (e *extractor) extract(job archiveJob) error {
	if job.depth > maxArchiveDepth {
		return fmt.Errorf("zip archive %q: nesting exceeds %d levels", job.path, maxArchiveDepth)
	}
	archivePath, err := e.target(job.path)
	if err != nil {
		return fmt.Errorf("zip archive %q: %w", job.path, err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive %q: %w", job.path, err)
	}
	defer reader.Close()
	base := path.Dir(job.path)
	if base == "." {
		base = ""
	}
	for _, entry := range reader.File {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		name, err := entryName(entry)
		if err != nil {
			return fmt.Errorf("zip archive %q: %w", job.path, err)
		}
		entryRelative, err := cleanRelativePath(name)
		if err != nil {
			return fmt.Errorf("zip archive %q entry %q: %w", job.path, name, err)
		}
		relative := entryRelative
		if base != "" {
			relative = path.Join(base, entryRelative)
		}
		target, err := e.target(relative)
		if err != nil {
			return fmt.Errorf("zip archive %q entry %q: %w", job.path, name, err)
		}
		mode := entry.Mode()
		if entry.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create directory for zip entry %q: %w", relative, err)
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("zip archive %q entry %q is a symbolic link", job.path, name)
		}
		if mode.Type() != 0 {
			return fmt.Errorf("zip archive %q entry %q has unsupported file type", job.path, name)
		}
		if err := e.writeEntry(entry, relative, target, job.depth); err != nil {
			return fmt.Errorf("zip archive %q: %w", job.path, err)
		}
	}
	return nil
}

func (e *extractor) writeEntry(entry *zip.File, relative, target string, depth int) error {
	if _, exists := e.download[relative]; exists {
		return fmt.Errorf("entry %q would overwrite a downloaded file", relative)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("entry %q conflicts with a directory", relative)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("replace existing path %q: %w", relative, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect existing path %q: %w", relative, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", relative, err)
	}
	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open entry %q: %w", relative, err)
	}
	defer source.Close()
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create extracted file %q: %w", relative, err)
	}
	copyErr := e.copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("extract entry %q: %w", relative, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close extracted file %q: %w", relative, closeErr)
	}
	if isZIP(relative) {
		e.enqueue(relative, depth+1)
		return nil
	}
	e.files[relative] = struct{}{}
	return nil
}

func (e *extractor) copy(destination io.Writer, source io.Reader) error {
	for {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(e.buffer)
		if read > 0 {
			if _, err := destination.Write(e.buffer[:read]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (e *extractor) target(relative string) (string, error) {
	target := filepath.Join(e.root, filepath.FromSlash(relative))
	resolved, err := filepath.Rel(e.root, target)
	if err != nil || resolved == "." || resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes the task directory")
	}
	return target, nil
}

func entryName(entry *zip.File) (string, error) {
	name := entry.Name
	if !utf8.ValidString(name) {
		decoded, err := simplifiedchinese.GBK.NewDecoder().String(name)
		if err == nil && utf8.ValidString(decoded) {
			name = decoded
		}
	}
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("entry name %q is not valid UTF-8 or GBK", entry.Name)
	}
	return name, nil
}

func cleanRelativePath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", errors.New("path contains a NUL byte")
	}
	if strings.HasPrefix(value, "/") {
		return "", errors.New("path is absolute")
	}
	if len(value) >= 2 && value[1] == ':' &&
		((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return "", errors.New("path contains a drive letter")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path escapes the task directory")
	}
	return cleaned, nil
}

func isZIP(relative string) bool {
	return strings.EqualFold(path.Ext(relative), ".zip")
}
