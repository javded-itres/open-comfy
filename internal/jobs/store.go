package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	Queued     Status = "queued"
	InProgress Status = "in_progress"
	Completed  Status = "completed"
	Failed     Status = "failed"
	Cancelled  Status = "cancelled"
	Expired    Status = "expired"
)

type Job struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	KeyHash           string         `json:"key_hash"`
	KeyPrefix         string         `json:"key_prefix"`
	Model             string         `json:"model"`
	Status            Status         `json:"status"`
	Progress          int            `json:"progress"`
	Prompt            string         `json:"prompt"`
	Params            map[string]any `json:"params"`
	RequestedPromptID string         `json:"requested_prompt_id"`
	ComfyPromptID     string         `json:"comfy_prompt_id"`
	QueuePosition     int            `json:"queue_position"`
	FileID            string         `json:"file_id"`
	MIME              string         `json:"mime"`
	Error             *JobError      `json:"error"`
	CreatedAt         int64          `json:"created_at"`
	CompletedAt       *int64         `json:"completed_at"`
	ExpiresAt         int64          `json:"expires_at"`
	InputPath         string         `json:"input_path,omitempty"`
	InputPaths        []string       `json:"input_paths,omitempty"`
}

type JobError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type Store struct {
	dir string
	ttl time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(dir string, ttl time.Duration) *Store {
	return &Store{dir: dir, ttl: ttl, locks: map[string]*sync.Mutex{}}
}

func (s *Store) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.locks[id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	return m
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) Put(j *Job) error {
	lk := s.lock(j.ID)
	lk.Lock()
	defer lk.Unlock()
	return s.writeLocked(j)
}

func (s *Store) writeLocked(j *Job) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	final := s.path(j.ID)
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Chmod(final, 0o600)
}

func (s *Store) Get(id string) (*Job, error) {
	lk := s.lock(id)
	lk.Lock()
	defer lk.Unlock()
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) Update(id string, fn func(*Job) error) (*Job, error) {
	lk := s.lock(id)
	lk.Lock()
	defer lk.Unlock()
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	if err := fn(&j); err != nil {
		return nil, err
	}
	if err := s.writeLocked(&j); err != nil {
		return nil, err
	}
	cp := j
	return &cp, nil
}

func (s *Store) ListByHash(hash string) ([]*Job, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Job
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.KeyHash == hash {
			cp := j
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *Store) CountActive() int {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.Status == Queued || j.Status == InProgress {
			n++
		}
	}
	return n
}

func (s *Store) ActiveIDs() []string {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.Status == Queued || j.Status == InProgress {
			ids = append(ids, j.ID)
		}
	}
	return ids
}

func (s *Store) CleanExpired(now time.Time, unlink func(fileID string)) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.ExpiresAt > 0 && now.Unix() > j.ExpiresAt {
			if unlink != nil && j.FileID != "" {
				unlink(j.FileID)
			}
			_ = os.Remove(p)
			if j.InputPath != "" {
				_ = os.Remove(j.InputPath)
			}
		}
	}
}
