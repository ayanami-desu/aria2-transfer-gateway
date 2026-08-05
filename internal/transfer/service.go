package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aria2-transfer-gateway/internal/aria2"
	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/provider"
	"aria2-transfer-gateway/internal/store"
)

type TaskInput struct {
	Type          string
	URLs          []string
	Content       string
	Options       map[string]any
	DestinationID string
	TargetPath    string
	Cleanup       bool
	Pause         bool
}

type RetryMode string

const (
	RetryModeUpload RetryMode = "upload"
	RetryModeFull   RetryMode = "full"
)

var errFinalFilesNotReady = errors.New("aria2 final files are not ready")

func sanitizeOptions(options map[string]any) map[string]string {
	protected := map[string]struct{}{
		"dir":                     {},
		"input-file":              {},
		"save-session":            {},
		"on-download-complete":    {},
		"on-download-stop":        {},
		"on-download-error":       {},
		"on-bt-download-complete": {},
	}
	result := make(map[string]string, len(options))
	for key, value := range options {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, blocked := protected[strings.ToLower(key)]; blocked {
			continue
		}
		if text, ok := optionValue(value); ok {
			result[key] = text
		}
	}
	return result
}

func optionValue(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case bool:
		return strconv.FormatBool(value), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case json.Number:
		return value.String(), true
	default:
		data, err := json.Marshal(value)
		return string(data), err == nil
	}
}

type Service struct {
	store                *store.Store
	downloader           aria2.Downloader
	providers            map[string]provider.Provider
	destinations         map[string]domain.Destination
	defaultDestinationID string
	stagingRoot          string
	jobs                 chan string
	workerCount          int
}

func NewService(taskStore *store.Store, downloader aria2.Downloader, providers map[string]provider.Provider, destinations []domain.Destination, defaultDestinationID string, stagingRoot string, workerCount int) (*Service, error) {
	if taskStore == nil || downloader == nil {
		return nil, errors.New("task store and downloader are required")
	}
	if workerCount <= 0 {
		workerCount = 1
	}
	absoluteStagingRoot, err := filepath.Abs(stagingRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve staging directory: %w", err)
	}
	if err := os.MkdirAll(absoluteStagingRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	destinationMap := make(map[string]domain.Destination, len(destinations))
	for _, destination := range destinations {
		destinationMap[destination.ID] = destination
	}
	defaultDestinationID = strings.TrimSpace(defaultDestinationID)
	if defaultDestinationID != "" {
		if _, exists := destinationMap[defaultDestinationID]; !exists {
			return nil, fmt.Errorf("default destination %q not found", defaultDestinationID)
		}
	}
	return &Service{
		store:                taskStore,
		downloader:           downloader,
		providers:            providers,
		destinations:         destinationMap,
		defaultDestinationID: defaultDestinationID,
		stagingRoot:          absoluteStagingRoot,
		jobs:                 make(chan string, workerCount*8),
		workerCount:          workerCount,
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	for _, task := range s.store.PendingTransfers() {
		_ = s.enqueue(task.ID)
	}
	for i := 0; i < s.workerCount; i++ {
		go s.worker(ctx)
	}
}

func (s *Service) Create(ctx context.Context, input TaskInput) (domain.Task, error) {
	taskType := strings.TrimSpace(input.Type)
	if taskType == "" {
		taskType = "urls"
	}
	if taskType != "urls" && taskType != "torrent" && taskType != "metalink" {
		return domain.Task{}, fmt.Errorf("unsupported task type %q", taskType)
	}
	urls := make([]string, 0, len(input.URLs))
	for _, rawURL := range input.URLs {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL != "" {
			urls = append(urls, rawURL)
		}
	}
	if taskType == "urls" && len(urls) == 0 {
		return domain.Task{}, errors.New("at least one URL is required")
	}
	if taskType != "urls" && strings.TrimSpace(input.Content) == "" {
		return domain.Task{}, fmt.Errorf("content is required for %s tasks", taskType)
	}
	options := sanitizeOptions(input.Options)
	destinationID := strings.TrimSpace(input.DestinationID)
	if destinationID == "" {
		destinationID = s.defaultDestinationID
	}
	destination, exists := s.destinations[destinationID]
	if !exists {
		return domain.Task{}, fmt.Errorf("destination %q not found", destinationID)
	}
	targetPath, err := domain.NormalizeTargetPath(input.TargetPath)
	if err != nil {
		return domain.Task{}, err
	}
	id, err := newID()
	if err != nil {
		return domain.Task{}, fmt.Errorf("generate task id: %w", err)
	}
	stagingPath := filepath.Join(s.stagingRoot, id)
	if err := os.MkdirAll(stagingPath, 0o750); err != nil {
		return domain.Task{}, fmt.Errorf("create staging path: %w", err)
	}
	now := time.Now().UTC()
	task := domain.Task{
		ID:            id,
		Type:          taskType,
		URLs:          urls,
		Content:       input.Content,
		Options:       options,
		DestinationID: destination.ID,
		TargetPath:    targetPath,
		StagingPath:   stagingPath,
		Status:        domain.StatusQueued,
		Cleanup:       input.Cleanup,
		Pause:         input.Pause,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.Create(task); err != nil {
		_ = os.RemoveAll(stagingPath)
		return domain.Task{}, fmt.Errorf("create task: %w", err)
	}
	var gid string
	switch taskType {
	case "urls":
		gid, err = s.downloader.AddURI(ctx, urls, stagingPath, input.Pause, options)
	case "torrent":
		gid, err = s.downloader.AddTorrent(ctx, input.Content, stagingPath, input.Pause, options)
	case "metalink":
		gid, err = s.downloader.AddMetalink(ctx, input.Content, stagingPath, input.Pause, options)
	}
	if err != nil {
		_, _ = s.store.Update(task.ID, func(current *domain.Task) error {
			current.Status = domain.StatusFailed
			current.Error = err.Error()
			return nil
		})
		return domain.Task{}, err
	}
	updated, err := s.store.Update(task.ID, func(current *domain.Task) error {
		current.GID = gid
		current.Status = domain.StatusDownloading
		return nil
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("save aria2 gid: %w", err)
	}
	return updated, nil
}

func (s *Service) HandleCompleted(ctx context.Context, gid, filePath string) error {
	gid = strings.TrimSpace(gid)
	if gid == "" {
		return errors.New("gid is required")
	}
	filePath = strings.TrimSpace(filePath)
	task, err := s.store.FindByGID(gid)
	if err != nil && filePath != "" {
		if taskID, pathErr := s.taskIDFromFilePath(filePath); pathErr == nil {
			task, err = s.store.Get(taskID)
		}
	}
	if err != nil {
		return err
	}
	if task.Status == domain.StatusCompleted || task.Status == domain.StatusTransferPending || task.Status == domain.StatusTransferring {
		return nil
	}
	finalFiles, resolvedGID, err := s.resolveFinalFiles(ctx, task, gid)
	if err != nil {
		if resolvedGID != "" && resolvedGID != task.GID {
			if updateErr := s.rememberGID(task.ID, resolvedGID); updateErr != nil {
				return updateErr
			}
		}
		if errors.Is(err, errFinalFilesNotReady) {
			_, updateErr := s.store.Update(task.ID, func(current *domain.Task) error {
				current.Status = domain.StatusDownloading
				current.Error = ""
				return nil
			})
			return updateErr
		}
		s.markFailed(task.ID, err)
		return err
	}
	if resolvedGID == "" {
		resolvedGID = gid
	}
	if _, err := s.store.Update(task.ID, func(current *domain.Task) error {
		current.GID = resolvedGID
		current.FinalFiles = finalFiles
		current.Status = domain.StatusTransferPending
		current.Error = ""
		return nil
	}); err != nil {
		return err
	}
	return s.enqueue(task.ID)
}

func (s *Service) rememberGID(id, gid string) error {
	if strings.TrimSpace(gid) == "" {
		return nil
	}
	_, err := s.store.Update(id, func(current *domain.Task) error {
		current.GID = gid
		return nil
	})
	return err
}

func (s *Service) taskIDFromFilePath(filePath string) (string, error) {
	root, err := filepath.Abs(s.stagingRoot)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolutePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("file path is outside staging root")
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", errors.New("file path does not contain a task directory")
	}
	return parts[0], nil
}

func (s *Service) resolveFinalFiles(ctx context.Context, task domain.Task, gid string) ([]string, string, error) {
	candidates := []string{strings.TrimSpace(gid)}
	seen := make(map[string]struct{}, len(candidates))
	lastGID := strings.TrimSpace(gid)
	for index := 0; index < len(candidates); index++ {
		currentGID := strings.TrimSpace(candidates[index])
		if currentGID == "" {
			continue
		}
		if _, exists := seen[currentGID]; exists {
			continue
		}
		seen[currentGID] = struct{}{}
		lastGID = currentGID
		files, err := s.downloader.GetFiles(ctx, currentGID)
		if err != nil {
			return nil, currentGID, fmt.Errorf("get aria2 files: %w", err)
		}
		finalFiles, finalErr := finalFilePaths(task.StagingPath, files)
		if finalErr == nil && len(finalFiles) > 0 {
			return finalFiles, currentGID, nil
		}
		if finalErr != nil && !errors.Is(finalErr, errFinalFilesNotReady) {
			return nil, currentGID, finalErr
		}
		if hasFinalFileCandidate(files) {
			return nil, currentGID, errFinalFilesNotReady
		}
		followedBy, err := s.downloader.GetFollowedBy(ctx, currentGID)
		if err != nil {
			return nil, currentGID, fmt.Errorf("get aria2 followed tasks: %w", err)
		}
		for _, childGID := range followedBy {
			childGID = strings.TrimSpace(childGID)
			if childGID != "" {
				candidates = append(candidates, childGID)
			}
		}
	}
	return nil, lastGID, errFinalFilesNotReady
}

func hasFinalFileCandidate(files []aria2.DownloadFile) bool {
	for _, file := range files {
		if file.Selected && !isAria2MetadataFile(file.Path) {
			return true
		}
	}
	return false
}

func finalFilePaths(stagingPath string, files []aria2.DownloadFile) ([]string, error) {
	root, err := filepath.Abs(stagingPath)
	if err != nil {
		return nil, fmt.Errorf("resolve staging directory: %w", err)
	}
	result := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !file.Selected {
			continue
		}
		if isAria2MetadataFile(file.Path) {
			continue
		}
		length, err := strconv.ParseInt(file.Length, 10, 64)
		if err != nil || length < 0 {
			return nil, fmt.Errorf("invalid length for aria2 file %q: %q", file.Path, file.Length)
		}
		completedLength, err := strconv.ParseInt(file.CompletedLength, 10, 64)
		if err != nil || completedLength < 0 || completedLength > length {
			return nil, fmt.Errorf("invalid completed length for aria2 file %q: %q", file.Path, file.CompletedLength)
		}
		localPath := file.Path
		if !filepath.IsAbs(localPath) {
			localPath = filepath.Join(root, localPath)
		}
		localPath, err = filepath.Abs(localPath)
		if err != nil {
			return nil, fmt.Errorf("resolve aria2 file %q: %w", file.Path, err)
		}
		relative, err := filepath.Rel(root, localPath)
		if err != nil {
			return nil, fmt.Errorf("resolve aria2 file %q: %w", file.Path, err)
		}
		if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("aria2 file %q is outside staging directory", file.Path)
		}
		info, err := os.Lstat(localPath)
		if err != nil {
			return nil, fmt.Errorf("stat aria2 file %q: %w", file.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("aria2 file %q is not a regular file", file.Path)
		}
		if info.Size() != length {
			return nil, fmt.Errorf("%w: aria2 file %q has size %d, want %d", errFinalFilesNotReady, file.Path, info.Size(), length)
		}
		relative = filepath.ToSlash(relative)
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		result = append(result, relative)
	}
	return result, nil
}

func isAria2MetadataFile(filePath string) bool {
	filePath = filepath.ToSlash(filePath)
	base := filepath.Base(filePath)
	return strings.HasPrefix(base, "[METADATA]") ||
		strings.HasPrefix(base, "[MEMORY][METADATA]") ||
		strings.EqualFold(filepath.Ext(base), ".torrent") ||
		strings.EqualFold(filepath.Ext(base), ".aria2")
}

func cleanupDownloadMetadata(stagingPath string, finalFiles []string) error {
	root, err := filepath.Abs(stagingPath)
	if err != nil {
		return fmt.Errorf("resolve staging directory: %w", err)
	}
	keep := make(map[string]struct{}, len(finalFiles))
	for _, file := range finalFiles {
		keep[filepath.ToSlash(filepath.Clean(file))] = struct{}{}
	}
	return filepath.WalkDir(root, func(localPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".aria2" && extension != ".torrent" {
			return nil
		}
		relative, err := filepath.Rel(root, localPath)
		if err != nil {
			return err
		}
		if _, ok := keep[filepath.ToSlash(relative)]; ok {
			return nil
		}
		if err := os.Remove(localPath); err != nil {
			return fmt.Errorf("remove aria2 metadata %q: %w", localPath, err)
		}
		return nil
	})
}

func (s *Service) HandleStopped(gid, reason string) error {
	task, err := s.store.FindByGID(strings.TrimSpace(gid))
	if err != nil {
		return err
	}
	if task.Status == domain.StatusCompleted {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "aria2 task stopped"
	}
	_, err = s.store.Update(task.ID, func(current *domain.Task) error {
		current.Status = domain.StatusFailed
		current.Error = reason
		return nil
	})
	return err
}

func (s *Service) Retry(ctx context.Context, id string, mode RetryMode) (domain.Task, error) {
	normalizedMode, err := normalizeRetryMode(mode)
	if err != nil {
		return domain.Task{}, err
	}
	task, err := s.store.Get(id)
	if err != nil {
		return domain.Task{}, err
	}
	if normalizedMode == RetryModeFull {
		return s.retryFull(ctx, task)
	}
	return s.retryUpload(task)
}

func normalizeRetryMode(mode RetryMode) (RetryMode, error) {
	mode = RetryMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		return RetryModeUpload, nil
	}
	if mode != RetryModeUpload && mode != RetryModeFull {
		return "", fmt.Errorf("unsupported retry mode %q", mode)
	}
	return mode, nil
}

func (s *Service) retryUpload(task domain.Task) (domain.Task, error) {
	stagingPath, err := s.taskStagingPath(task.StagingPath)
	if err != nil {
		return domain.Task{}, fmt.Errorf("upload retry for task %q: %w", task.ID, err)
	}
	info, err := os.Stat(stagingPath)
	if err != nil {
		return domain.Task{}, fmt.Errorf("upload retry for task %q: staging directory is unavailable: %w", task.ID, err)
	}
	if !info.IsDir() {
		return domain.Task{}, fmt.Errorf("upload retry for task %q: staging path is not a directory", task.ID)
	}
	updated, err := s.store.Update(task.ID, func(current *domain.Task) error {
		current.Status = domain.StatusTransferPending
		current.Error = ""
		current.CompletedAt = time.Time{}
		return nil
	})
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.enqueue(task.ID); err != nil {
		return domain.Task{}, err
	}
	return updated, nil
}

func (s *Service) retryFull(ctx context.Context, task domain.Task) (domain.Task, error) {
	if err := s.deleteTask(ctx, task); err != nil {
		return domain.Task{}, fmt.Errorf("full retry for task %q: %w", task.ID, err)
	}
	options := make(map[string]any, len(task.Options))
	for key, value := range task.Options {
		options[key] = value
	}
	return s.Create(ctx, TaskInput{
		Type:          task.Type,
		URLs:          task.URLs,
		Content:       task.Content,
		Options:       options,
		DestinationID: task.DestinationID,
		TargetPath:    task.TargetPath,
		Cleanup:       task.Cleanup,
		Pause:         task.Pause,
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	task, err := s.store.Get(id)
	if err != nil {
		return err
	}
	return s.deleteTask(ctx, task)
}

func (s *Service) deleteTask(ctx context.Context, task domain.Task) error {
	if task.GID != "" {
		if err := s.downloader.Remove(ctx, task.GID); err != nil && !errors.Is(err, aria2.ErrGIDNotFound) {
			return fmt.Errorf("delete task %q: cancel aria2 task: %w", task.ID, err)
		}
	}
	if err := s.removeStagingIfPresent(task.StagingPath); err != nil {
		return fmt.Errorf("delete task %q: remove staging directory: %w", task.ID, err)
	}
	if err := s.store.Delete(task.ID); err != nil {
		return fmt.Errorf("delete task %q: delete record: %w", task.ID, err)
	}
	return nil
}

func (s *Service) removeStagingIfPresent(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve staging path: %w", err)
	}
	if _, err := os.Lstat(absolute); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stagingPath, err := s.taskStagingPath(absolute)
	if err != nil {
		return err
	}
	return os.RemoveAll(stagingPath)
}

func (s *Service) taskStagingPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("staging path is empty")
	}
	root, err := filepath.Abs(s.stagingRoot)
	if err != nil {
		return "", fmt.Errorf("resolve staging root: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve staging path: %w", err)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("staging path is outside the staging root")
	}
	return absolute, nil
}

func (s *Service) Get(id string) (domain.Task, error) {
	return s.store.Get(id)
}

func (s *Service) List() []domain.Task {
	return s.store.List()
}

func (s *Service) ListFiltered(filter store.TaskFilter) ([]domain.Task, error) {
	return s.store.ListFiltered(filter)
}

func (s *Service) Destinations() []domain.Destination {
	result := make([]domain.Destination, 0, len(s.destinations))
	for _, destination := range s.destinations {
		result = append(result, destination)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == s.defaultDestinationID {
			return true
		}
		if result[j].ID == s.defaultDestinationID {
			return false
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (s *Service) View(task domain.Task) domain.TaskView {
	destination := s.destinations[task.DestinationID]
	return task.View(destination.Name)
}

func (s *Service) enqueue(id string) error {
	select {
	case s.jobs <- id:
		return nil
	default:
		return errors.New("transfer queue is full")
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.jobs:
			s.runTransfer(ctx, id)
		}
	}
}

func (s *Service) runTransfer(ctx context.Context, id string) {
	task, err := s.store.Get(id)
	if err != nil {
		return
	}
	destination, exists := s.destinations[task.DestinationID]
	if !exists {
		s.markFailed(id, fmt.Errorf("destination %q not found", task.DestinationID))
		return
	}
	backend, exists := s.providers[destination.Provider]
	if !exists {
		s.markFailed(id, fmt.Errorf("provider %q is not configured", destination.Provider))
		return
	}
	if _, err := s.store.Update(id, func(current *domain.Task) error {
		current.Status = domain.StatusTransferring
		current.Error = ""
		return nil
	}); err != nil {
		return
	}
	if task.FinalFiles == nil {
		finalFiles, resolvedGID, err := s.resolveFinalFiles(ctx, task, task.GID)
		if err != nil {
			if resolvedGID != "" && resolvedGID != task.GID {
				if updateErr := s.rememberGID(id, resolvedGID); updateErr != nil {
					return
				}
				task.GID = resolvedGID
			}
			if errors.Is(err, errFinalFilesNotReady) {
				_, _ = s.store.Update(id, func(current *domain.Task) error {
					current.Status = domain.StatusDownloading
					current.Error = ""
					return nil
				})
				return
			}
			s.markFailed(id, err)
			return
		}
		if resolvedGID == "" {
			resolvedGID = task.GID
		}
		updated, err := s.store.Update(id, func(current *domain.Task) error {
			current.GID = resolvedGID
			current.FinalFiles = finalFiles
			return nil
		})
		if err != nil {
			s.markFailed(id, err)
			return
		}
		task = updated
	}
	if err := cleanupDownloadMetadata(task.StagingPath, task.FinalFiles); err != nil {
		s.markFailed(id, err)
		return
	}
	err = backend.Transfer(ctx, provider.TransferRequest{
		SourceDir:   task.StagingPath,
		TargetPath:  task.TargetPath,
		Files:       task.FinalFiles,
		Destination: destination,
	})
	if err != nil {
		s.markFailed(id, err)
		return
	}
	if task.Cleanup {
		if err := os.RemoveAll(task.StagingPath); err != nil {
			s.markFailed(id, fmt.Errorf("transfer succeeded but cleanup failed: %w", err))
			return
		}
	}
	_, _ = s.store.Update(id, func(current *domain.Task) error {
		current.Status = domain.StatusCompleted
		current.Error = ""
		current.CompletedAt = time.Now().UTC()
		return nil
	})
}

func (s *Service) markFailed(id string, transferErr error) {
	_, _ = s.store.Update(id, func(current *domain.Task) error {
		current.Status = domain.StatusFailed
		current.Error = transferErr.Error()
		current.RetryCount++
		return nil
	})
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
