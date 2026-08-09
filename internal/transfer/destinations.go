package transfer

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/store"
)

var ErrDestinationInUse = errors.New("destination is used by existing tasks")
var ErrDefaultDestination = errors.New("default destination cannot be deleted")

var destinationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (s *Service) CreateDestination(destination domain.Destination) (domain.Destination, error) {
	destination, err := normalizeManagedDestination(destination)
	if err != nil {
		return domain.Destination{}, err
	}

	s.destinationsMu.Lock()
	defer s.destinationsMu.Unlock()
	if _, exists := s.destinations[destination.ID]; exists {
		return domain.Destination{}, store.ErrDestinationAlreadyExists
	}
	if err := s.store.CreateDestination(destination); err != nil {
		return domain.Destination{}, err
	}
	s.destinations[destination.ID] = destination
	if s.defaultDestinationID == "" {
		if err := s.store.SetDefaultDestination(destination.ID); err != nil {
			delete(s.destinations, destination.ID)
			_ = s.store.DeleteDestination(destination.ID)
			return domain.Destination{}, err
		}
		s.defaultDestinationID = destination.ID
	}
	return destination, nil
}

func (s *Service) UpdateDestination(id string, destination domain.Destination, clearProxy bool) (domain.Destination, error) {
	id = strings.TrimSpace(id)
	s.destinationsMu.Lock()
	defer s.destinationsMu.Unlock()
	existing, exists := s.destinations[id]
	if !exists {
		return domain.Destination{}, store.ErrDestinationNotFound
	}
	destination.ID = id
	if strings.TrimSpace(destination.Token) == "" {
		destination.Token = existing.Token
	}
	if clearProxy {
		destination.Proxy = ""
	} else if strings.TrimSpace(destination.Proxy) == "" {
		destination.Proxy = existing.Proxy
	}
	destination, err := normalizeManagedDestination(destination)
	if err != nil {
		return domain.Destination{}, err
	}
	if err := s.store.UpdateDestination(destination); err != nil {
		return domain.Destination{}, err
	}
	s.destinations[id] = destination
	return destination, nil
}

func (s *Service) DeleteDestination(id string) error {
	id = strings.TrimSpace(id)
	s.destinationsMu.Lock()
	defer s.destinationsMu.Unlock()
	if _, exists := s.destinations[id]; !exists {
		return store.ErrDestinationNotFound
	}
	if id == s.defaultDestinationID {
		return ErrDefaultDestination
	}
	for _, task := range s.store.List() {
		if task.DestinationID == id {
			return ErrDestinationInUse
		}
	}
	if err := s.store.DeleteDestination(id); err != nil {
		return err
	}
	delete(s.destinations, id)
	return nil
}

func (s *Service) SetDefaultDestination(id string) error {
	id = strings.TrimSpace(id)
	s.destinationsMu.Lock()
	defer s.destinationsMu.Unlock()
	if _, exists := s.destinations[id]; !exists {
		return store.ErrDestinationNotFound
	}
	if err := s.store.SetDefaultDestination(id); err != nil {
		return err
	}
	s.defaultDestinationID = id
	return nil
}

func (s *Service) DefaultDestinationID() string {
	s.destinationsMu.RLock()
	defer s.destinationsMu.RUnlock()
	return s.defaultDestinationID
}

func (s *Service) destination(id string) (domain.Destination, bool) {
	s.destinationsMu.RLock()
	defer s.destinationsMu.RUnlock()
	destination, exists := s.destinations[id]
	return destination, exists
}

func (s *Service) destinationOrDefault(id string) (domain.Destination, bool) {
	s.destinationsMu.RLock()
	defer s.destinationsMu.RUnlock()
	id = strings.TrimSpace(id)
	if id == "" {
		id = s.defaultDestinationID
	}
	destination, exists := s.destinations[id]
	return destination, exists
}

func normalizeManagedDestination(destination domain.Destination) (domain.Destination, error) {
	destination.ID = strings.TrimSpace(destination.ID)
	destination.Name = strings.TrimSpace(destination.Name)
	destination.Provider = strings.ToLower(strings.TrimSpace(destination.Provider))
	destination.Endpoint = strings.TrimSpace(destination.Endpoint)
	destination.Mount = strings.TrimSpace(destination.Mount)
	destination.Remote = strings.TrimSpace(destination.Remote)
	destination.Root = strings.TrimSpace(destination.Root)
	destination.RcloneConfig = strings.TrimSpace(destination.RcloneConfig)
	destination.Token = strings.TrimSpace(destination.Token)
	proxy, err := domain.NormalizeProxyURL(destination.Proxy)
	if err != nil {
		return domain.Destination{}, err
	}
	destination.Proxy = proxy

	if !destinationIDPattern.MatchString(destination.ID) {
		return domain.Destination{}, errors.New("destination id must be 1-64 letters, numbers, dots, underscores, or hyphens")
	}
	if destination.Name == "" {
		return domain.Destination{}, errors.New("destination name is required")
	}
	switch destination.Provider {
	case "openlist":
		endpoint, err := url.Parse(destination.Endpoint)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return domain.Destination{}, errors.New("openlist endpoint must be an absolute HTTP or HTTPS URL")
		}
		if destination.Token == "" {
			return domain.Destination{}, errors.New("openlist token is required")
		}
		mount, err := domain.NormalizeTargetPath(destination.Mount)
		if err != nil {
			return domain.Destination{}, fmt.Errorf("invalid openlist mount: %w", err)
		}
		destination.Endpoint = strings.TrimRight(destination.Endpoint, "/")
		destination.Mount = mount
		destination.Remote = ""
		destination.Root = ""
		destination.RcloneConfig = ""
	case "rclone":
		if destination.Remote == "" {
			return domain.Destination{}, errors.New("rclone remote is required")
		}
		if destination.RcloneConfig == "" {
			return domain.Destination{}, errors.New("rclone config path is required")
		}
		root, err := domain.NormalizeTargetPath(destination.Root)
		if err != nil {
			return domain.Destination{}, fmt.Errorf("invalid rclone root: %w", err)
		}
		destination.Root = root
		destination.Endpoint = ""
		destination.Mount = ""
		destination.Token = ""
	default:
		return domain.Destination{}, fmt.Errorf("unsupported destination provider %q", destination.Provider)
	}
	return destination, nil
}
