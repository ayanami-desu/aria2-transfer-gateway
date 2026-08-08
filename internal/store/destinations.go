package store

import (
	"database/sql"
	"errors"
	"fmt"

	"aria2-transfer-gateway/internal/domain"
)

var ErrDestinationNotFound = errors.New("destination not found")
var ErrDestinationAlreadyExists = errors.New("destination already exists")

const selectDestinationSQL = `SELECT id, name, provider, endpoint, mount, remote, root, rclone_config, token, proxy FROM destinations`

func (s *Store) InitializeDestinations(destinations []domain.Destination, defaultDestinationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin destination initialization: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM destinations`).Scan(&count); err != nil {
		return fmt.Errorf("count destinations: %w", err)
	}
	if count > 0 {
		return nil
	}
	for _, destination := range destinations {
		if _, err := tx.Exec(`INSERT INTO destinations (id, name, provider, endpoint, mount, remote, root, rclone_config, token, proxy) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, destination.ID, destination.Name, destination.Provider, destination.Endpoint, destination.Mount, destination.Remote, destination.Root, destination.RcloneConfig, destination.Token, destination.Proxy); err != nil {
			return fmt.Errorf("initialize destination %q: %w", destination.ID, err)
		}
	}
	if defaultDestinationID != "" {
		if _, err := tx.Exec(`INSERT INTO gateway_settings (key, value) VALUES ('default_destination_id', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, defaultDestinationID); err != nil {
			return fmt.Errorf("initialize default destination: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit destination initialization: %w", err)
	}
	return nil
}

func (s *Store) DestinationSettings() ([]domain.Destination, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(selectDestinationSQL + ` ORDER BY id`)
	if err != nil {
		return nil, "", fmt.Errorf("list destinations: %w", err)
	}
	result := make([]domain.Destination, 0)
	for rows.Next() {
		destination, err := scanDestination(rows)
		if err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("scan destination: %w", err)
		}
		result = append(result, destination)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", fmt.Errorf("iterate destinations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, "", fmt.Errorf("close destinations: %w", err)
	}

	var defaultDestinationID string
	err = s.db.QueryRow(`SELECT value FROM gateway_settings WHERE key = 'default_destination_id'`).Scan(&defaultDestinationID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read default destination: %w", err)
	}
	return result, defaultDestinationID, nil
}

func (s *Store) GetDestination(id string) (domain.Destination, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getDestination(s.db.QueryRow(selectDestinationSQL+` WHERE id = ?`, id))
}

func (s *Store) CreateDestination(destination domain.Destination) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM destinations WHERE id = ?`, destination.ID).Scan(&exists); err != nil {
		return fmt.Errorf("check destination %q: %w", destination.ID, err)
	}
	if exists != 0 {
		return ErrDestinationAlreadyExists
	}
	if _, err := s.db.Exec(`INSERT INTO destinations (id, name, provider, endpoint, mount, remote, root, rclone_config, token, proxy) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, destination.ID, destination.Name, destination.Provider, destination.Endpoint, destination.Mount, destination.Remote, destination.Root, destination.RcloneConfig, destination.Token, destination.Proxy); err != nil {
		return fmt.Errorf("create destination %q: %w", destination.ID, err)
	}
	return nil
}

func (s *Store) UpdateDestination(destination domain.Destination) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE destinations SET name = ?, provider = ?, endpoint = ?, mount = ?, remote = ?, root = ?, rclone_config = ?, token = ?, proxy = ? WHERE id = ?`, destination.Name, destination.Provider, destination.Endpoint, destination.Mount, destination.Remote, destination.Root, destination.RcloneConfig, destination.Token, destination.Proxy, destination.ID)
	if err != nil {
		return fmt.Errorf("update destination %q: %w", destination.ID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check destination update: %w", err)
	} else if affected != 1 {
		return ErrDestinationNotFound
	}
	return nil
}

func (s *Store) DeleteDestination(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM destinations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete destination %q: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check destination deletion: %w", err)
	} else if affected != 1 {
		return ErrDestinationNotFound
	}
	return nil
}

func (s *Store) SetDefaultDestination(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM destinations WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check default destination %q: %w", id, err)
	}
	if exists == 0 {
		return ErrDestinationNotFound
	}
	if _, err := s.db.Exec(`INSERT INTO gateway_settings (key, value) VALUES ('default_destination_id', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, id); err != nil {
		return fmt.Errorf("set default destination %q: %w", id, err)
	}
	return nil
}

func getDestination(row *sql.Row) (domain.Destination, error) {
	destination, err := scanDestination(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Destination{}, ErrDestinationNotFound
	}
	if err != nil {
		return domain.Destination{}, fmt.Errorf("read destination: %w", err)
	}
	return destination, nil
}

func scanDestination(row rowScanner) (domain.Destination, error) {
	var destination domain.Destination
	err := row.Scan(&destination.ID, &destination.Name, &destination.Provider, &destination.Endpoint, &destination.Mount, &destination.Remote, &destination.Root, &destination.RcloneConfig, &destination.Token, &destination.Proxy)
	return destination, err
}
