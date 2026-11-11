package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"highlight-assembly/internal/domain"
	"highlight-assembly/internal/media"
)

// Store persists projects and export jobs on disk under a data directory.
type Store struct {
	root string
	mu   sync.RWMutex
	jobs map[string]*media.Job
}

func New(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "videos"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root, jobs: map[string]*media.Job{}}, nil
}

func (s *Store) ProjectPath(id string) string {
	return filepath.Join(s.root, "projects", id+".json")
}

func (s *Store) VideoDir() string  { return filepath.Join(s.root, "videos") }
func (s *Store) ExportDir() string { return filepath.Join(s.root, "exports") }

func (s *Store) SaveProject(p *domain.Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Version++
	tmp := s.ProjectPath(p.ID) + ".tmp"
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.ProjectPath(p.ID))
}

func (s *Store) LoadProject(id string) (*domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.ProjectPath(id))
	if err != nil {
		return nil, err
	}
	var p domain.Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListProjects() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "projects"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			ids = append(ids, e.Name()[:len(e.Name())-5])
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ---- export jobs ----

var ErrJobNotFound = errors.New("export job not found")

func (s *Store) RegisterJob(id string, j *media.Job) {
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
}

func (s *Store) GetJob(id string) (*media.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	return j, nil
}
