package files

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/javded-itres/open-comfy/internal/ids"
)

type Store struct {
	dir    string
	secret []byte
	ttl    time.Duration
	origin func() string
	mu     sync.Mutex
	meta   map[string]*Meta
}

type Meta struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	MIME    string `json:"mime"`
	KeyHash string `json:"key_hash"`
	Bytes   int    `json:"bytes"`
}

func LoadSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(b))
	if raw, err := hex.DecodeString(s); err == nil && len(raw) >= 32 {
		return raw, nil
	}
	if len(b) >= 32 {
		return b, nil
	}
	return nil, fmt.Errorf("hmac secret too short")
}

func WriteSecret(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(b)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func New(dir string, secret []byte, ttl time.Duration, origin func() string) *Store {
	return &Store{dir: dir, secret: secret, ttl: ttl, origin: origin, meta: map[string]*Meta{}}
}

func (s *Store) Put(keyHash, mime string, data []byte) (*Meta, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	id := ids.New("file_")
	ext := extFor(mime)
	path := filepath.Join(s.dir, id+ext)
	tmp, err := os.CreateTemp(s.dir, ".tmp-")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	_ = tmp.Sync()
	tmp.Close()
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	m := &Meta{ID: id, Path: path, MIME: mime, KeyHash: keyHash, Bytes: len(data)}
	s.mu.Lock()
	s.meta[id] = m
	s.mu.Unlock()
	return m, nil
}

func (s *Store) Get(id string) (*Meta, []byte, error) {
	s.mu.Lock()
	m := s.meta[id]
	s.mu.Unlock()
	if m == nil {
		matches, _ := filepath.Glob(filepath.Join(s.dir, id+".*"))
		if len(matches) == 0 {
			p := filepath.Join(s.dir, id)
			if _, err := os.Stat(p); err == nil {
				matches = []string{p}
			}
		}
		if len(matches) == 0 {
			return nil, nil, os.ErrNotExist
		}
		b, err := os.ReadFile(matches[0])
		if err != nil {
			return nil, nil, err
		}
		return &Meta{ID: id, Path: matches[0]}, b, nil
	}
	b, err := os.ReadFile(m.Path)
	return m, b, err
}

func (s *Store) Meta(id string) *Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta[id]
}

func (s *Store) Unlink(id string) {
	s.mu.Lock()
	m := s.meta[id]
	delete(s.meta, id)
	s.mu.Unlock()
	if m != nil {
		_ = os.Remove(m.Path)
	}
}

func (s *Store) SignURL(fileID string) (string, error) {
	origin := ""
	if s.origin != nil {
		origin = s.origin()
	}
	if origin == "" {
		return "", fmt.Errorf("empty origin")
	}
	if len(s.secret) == 0 {
		return "", fmt.Errorf("no hmac secret")
	}
	exp := time.Now().Add(s.ttl).Unix()
	sig := s.sign(fileID, exp)
	return fmt.Sprintf("%s/v1/files/%s?exp=%d&sig=%s", origin, fileID, exp, sig), nil
}

func (s *Store) sign(fileID string, exp int64) string {
	mac := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(mac, "%s.%d", fileID, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) Verify(fileID, expStr, sig string) bool {
	if len(s.secret) == 0 || sig == "" || expStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	if exp < now-60 || exp > now+s.ttl.Nanoseconds()/int64(time.Second)+60 {
		if exp+60 < now {
			return false
		}
	}
	want := s.sign(fileID, exp)
	return hmac.Equal([]byte(want), []byte(strings.ToLower(sig))) || hmac.Equal([]byte(want), []byte(sig))
}

func extFor(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}
