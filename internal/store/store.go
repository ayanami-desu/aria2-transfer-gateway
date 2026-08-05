package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aria2-transfer-gateway/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("task not found")
var ErrAlreadyExists = errors.New("task already exists")

type TaskFilter struct {
	Statuses      []string
	DestinationID string
	Query         string
	Limit         int
	Offset        int
}

type Store struct {
	mu   sync.RWMutex
	path string
	db   *sql.DB
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("task store path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create task store directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite task store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{path: path, db: db}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set task store permissions: %w", err)
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) Create(task domain.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin task create: %w", err)
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id = ?)`, task.ID).Scan(&exists); err != nil {
		return fmt.Errorf("check task existence: %w", err)
	}
	if exists {
		return ErrAlreadyExists
	}
	values, err := taskValues(task)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(insertTaskSQL, values...); err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task create: %w", err)
	}
	return nil
}

func (s *Store) Get(id string) (domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, err := scanTask(s.db.QueryRow(selectTaskSQL+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("read task: %w", err)
	}
	return cloneTask(task), nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check task deletion: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FindByGID(gid string) (domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, err := scanTask(s.db.QueryRow(selectTaskSQL+` WHERE gid = ? ORDER BY created_at DESC LIMIT 1`, gid))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("find task by GID: %w", err)
	}
	return cloneTask(task), nil
}

func (s *Store) List() []domain.Task {
	tasks, _ := s.ListFiltered(TaskFilter{})
	return tasks
}

func (s *Store) ListFiltered(filter TaskFilter) ([]domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query, args := buildListQuery(filter)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		result = append(result, cloneTask(task))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return result, nil
}

func (s *Store) Update(id string, fn func(*domain.Task) error) (domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin task update: %w", err)
	}
	defer tx.Rollback()
	task, err := scanTask(tx.QueryRow(selectTaskSQL+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("read task for update: %w", err)
	}
	if err := fn(&task); err != nil {
		return domain.Task{}, err
	}
	task.UpdatedAt = time.Now().UTC()
	values, err := taskValues(task)
	if err != nil {
		return domain.Task{}, err
	}
	result, err := tx.Exec(updateTaskSQL, append(values[1:], id)...)
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.Task{}, fmt.Errorf("check task update: %w", err)
	} else if affected != 1 {
		return domain.Task{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit task update: %w", err)
	}
	return cloneTask(task), nil
}

func (s *Store) PendingTransfers() []domain.Task {
	tasks, _ := s.ListFiltered(TaskFilter{
		Statuses: []string{domain.StatusTransferPending, domain.StatusTransferring},
	})
	return tasks
}

func (s *Store) initialize() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("configure SQLite task store: %w", err)
	}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	gid TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL DEFAULT '',
	urls TEXT,
	content TEXT NOT NULL DEFAULT '',
	options TEXT,
	destination_id TEXT NOT NULL DEFAULT '',
	target_path TEXT NOT NULL DEFAULT '/',
	staging_path TEXT NOT NULL DEFAULT '',
	final_files TEXT,
	status TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	retry_count INTEGER NOT NULL DEFAULT 0,
	cleanup INTEGER NOT NULL DEFAULT 0,
	pause INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_tasks_gid ON tasks(gid);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_destination ON tasks(destination_id);
CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at DESC);
`); err != nil {
		return fmt.Errorf("initialize SQLite task store: %w", err)
	}
	return nil
}

const selectTaskSQL = `SELECT id, gid, type, urls, content, options, destination_id, target_path, staging_path, final_files, status, error, retry_count, cleanup, pause, created_at, updated_at, completed_at FROM tasks`

const insertTaskSQL = `INSERT INTO tasks (id, gid, type, urls, content, options, destination_id, target_path, staging_path, final_files, status, error, retry_count, cleanup, pause, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const updateTaskSQL = `UPDATE tasks SET gid = ?, type = ?, urls = ?, content = ?, options = ?, destination_id = ?, target_path = ?, staging_path = ?, final_files = ?, status = ?, error = ?, retry_count = ?, cleanup = ?, pause = ?, created_at = ?, updated_at = ?, completed_at = ? WHERE id = ?`

func buildListQuery(filter TaskFilter) (string, []any) {
	query := selectTaskSQL
	conditions := make([]string, 0, 3)
	args := make([]any, 0, len(filter.Statuses)+3)
	statuses := make([]string, 0, len(filter.Statuses))
	for _, status := range filter.Statuses {
		status = strings.TrimSpace(status)
		if status != "" {
			statuses = append(statuses, status)
		}
	}
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, status := range statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		conditions = append(conditions, "status IN ("+strings.Join(placeholders, ", ")+")")
	}
	if destinationID := strings.TrimSpace(filter.DestinationID); destinationID != "" {
		conditions = append(conditions, "destination_id = ?")
		args = append(args, destinationID)
	}
	if search := strings.TrimSpace(filter.Query); search != "" {
		conditions = append(conditions, "LOWER(id || ' ' || gid || ' ' || type || ' ' || destination_id || ' ' || target_path || ' ' || error || ' ' || content || ' ' || COALESCE(urls, '') || ' ' || COALESCE(options, '') || ' ' || COALESCE(final_files, '')) LIKE ?")
		args = append(args, "%"+strings.ToLower(search)+"%")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	} else if filter.Offset > 0 {
		query += " LIMIT -1 OFFSET ?"
		args = append(args, filter.Offset)
	}
	return query, args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var urlsRaw, optionsRaw, finalFilesRaw sql.NullString
	var completedAtRaw sql.NullString
	var retryCount, cleanup, pause int64
	var createdAtRaw, updatedAtRaw string
	if err := row.Scan(
		&task.ID,
		&task.GID,
		&task.Type,
		&urlsRaw,
		&task.Content,
		&optionsRaw,
		&task.DestinationID,
		&task.TargetPath,
		&task.StagingPath,
		&finalFilesRaw,
		&task.Status,
		&task.Error,
		&retryCount,
		&cleanup,
		&pause,
		&createdAtRaw,
		&updatedAtRaw,
		&completedAtRaw,
	); err != nil {
		return domain.Task{}, err
	}
	if err := decodeJSONValue(urlsRaw, &task.URLs); err != nil {
		return domain.Task{}, fmt.Errorf("decode task URLs: %w", err)
	}
	if err := decodeJSONValue(optionsRaw, &task.Options); err != nil {
		return domain.Task{}, fmt.Errorf("decode task options: %w", err)
	}
	if err := decodeJSONValue(finalFilesRaw, &task.FinalFiles); err != nil {
		return domain.Task{}, fmt.Errorf("decode task final files: %w", err)
	}
	var err error
	if task.CreatedAt, err = parseTaskTime(createdAtRaw); err != nil {
		return domain.Task{}, fmt.Errorf("decode task created time: %w", err)
	}
	if task.UpdatedAt, err = parseTaskTime(updatedAtRaw); err != nil {
		return domain.Task{}, fmt.Errorf("decode task updated time: %w", err)
	}
	if completedAtRaw.Valid {
		if task.CompletedAt, err = parseTaskTime(completedAtRaw.String); err != nil {
			return domain.Task{}, fmt.Errorf("decode task completed time: %w", err)
		}
	}
	task.RetryCount = int(retryCount)
	task.Cleanup = cleanup != 0
	task.Pause = pause != 0
	return task, nil
}

func taskValues(task domain.Task) ([]any, error) {
	urls, err := json.Marshal(task.URLs)
	if err != nil {
		return nil, fmt.Errorf("encode task URLs: %w", err)
	}
	options, err := json.Marshal(task.Options)
	if err != nil {
		return nil, fmt.Errorf("encode task options: %w", err)
	}
	var finalFiles any
	if task.FinalFiles != nil {
		finalFiles, err = json.Marshal(task.FinalFiles)
		if err != nil {
			return nil, fmt.Errorf("encode task final files: %w", err)
		}
	}
	var completedAt any
	if !task.CompletedAt.IsZero() {
		completedAt = formatTaskTime(task.CompletedAt)
	}
	return []any{
		task.ID,
		task.GID,
		task.Type,
		string(urls),
		task.Content,
		string(options),
		task.DestinationID,
		task.TargetPath,
		task.StagingPath,
		finalFiles,
		task.Status,
		task.Error,
		task.RetryCount,
		boolValue(task.Cleanup),
		boolValue(task.Pause),
		formatTaskTime(task.CreatedAt),
		formatTaskTime(task.UpdatedAt),
		completedAt,
	}, nil
}

func decodeJSONValue(raw sql.NullString, target any) error {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw.String), target)
}

func formatTaskTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTaskTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func boolValue(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cloneTask(task domain.Task) domain.Task {
	task.URLs = append([]string(nil), task.URLs...)
	if task.FinalFiles != nil {
		cloned := make([]string, len(task.FinalFiles))
		copy(cloned, task.FinalFiles)
		task.FinalFiles = cloned
	}
	if task.Options != nil {
		cloned := make(map[string]string, len(task.Options))
		for key, value := range task.Options {
			cloned[key] = value
		}
		task.Options = cloned
	}
	return task
}
