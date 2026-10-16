package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/denis/docker-fs-viewer/internal/model"
)

type Store struct {
	Root string
}

func NewStore() (*Store, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "docker-fs-viewer", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{Root: root}, nil
}

func NewID(containerID string) string {
	prefix := containerID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("%s-%s", prefix, time.Now().UTC().Format("20060102T150405"))
}

func (s *Store) Dir(id string) string {
	return filepath.Join(s.Root, id)
}

func (s *Store) Create(session *model.Session) error {
	dir := s.Dir(session.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	return s.Save(session)
}

func (s *Store) Save(session *model.Session) error {
	session.UpdatedAt = time.Now().UTC()
	return writeJSONAtomic(filepath.Join(s.Dir(session.ID), "session.json"), session)
}

func (s *Store) SaveManifest(id string, manifest *model.Manifest) error {
	return writeJSONAtomic(filepath.Join(s.Dir(id), "manifest.json"), manifest)
}

func (s *Store) LoadManifest(id string) (*model.Manifest, error) {
	var manifest model.Manifest
	if err := readJSON(filepath.Join(s.Dir(id), "manifest.json"), &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (s *Store) List() ([]*model.Session, error) {
	children, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	var sessions []*model.Session
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		var item model.Session
		if err := readJSON(filepath.Join(s.Root, child.Name(), "session.json"), &item); err == nil {
			sessions = append(sessions, &item)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

func (s *Store) Resolve(ref string) (*model.Session, error) {
	sessions, err := s.List()
	if err != nil {
		return nil, err
	}
	var matches []*model.Session
	for _, item := range sessions {
		name := strings.TrimPrefix(item.ContainerName, "/")
		if ref == "" || item.ID == ref || strings.HasPrefix(item.ID, ref) ||
			item.ContainerID == ref || strings.HasPrefix(item.ContainerID, ref) || name == ref {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		if ref == "" {
			return nil, errors.New("no sessions found; run mount first")
		}
		return nil, fmt.Errorf("session %q not found", ref)
	}
	if len(matches) > 1 && ref != "" {
		return nil, fmt.Errorf("session reference %q is ambiguous", ref)
	}
	return matches[0], nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
