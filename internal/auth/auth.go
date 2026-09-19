package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/javded-itres/open-comfy/internal/ids"
	"gopkg.in/yaml.v3"
)

type Key struct {
	Name          string
	Hash          string
	Prefix        string
	Models        []string
	RPM           int
	MaxConcurrent int
	Enabled       bool
}

type fileKey struct {
	Name          string   `yaml:"name"`
	Key           string   `yaml:"key"`
	KeySHA256     string   `yaml:"key_sha256"`
	Models        []string `yaml:"models"`
	RPM           int      `yaml:"rpm"`
	MaxConcurrent int      `yaml:"max_concurrent"`
	Enabled       *bool    `yaml:"enabled"`
}

type fileKeys struct {
	Keys []fileKey `yaml:"keys"`
}

type Service struct {
	disabled bool
	keys     []Key

	mu      sync.Mutex
	rpm     map[string]*rpmBucket
	fails   map[string]*failBucket
	inflight map[string]int
}

type rpmBucket struct {
	window time.Time
	count  int
}

type failBucket struct {
	until time.Time
	n     int
}

const (
	failWindow = 10 * time.Minute
	failMax    = 20
)

func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func Bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if k := r.Header.Get("X-Api-Key"); k != "" {
		return k
	}
	return ""
}

func Load(path string, extraPlain []string, disabled bool) (*Service, error) {
	s := &Service{
		disabled: disabled,
		rpm:      map[string]*rpmBucket{},
		fails:    map[string]*failBucket{},
		inflight: map[string]int{},
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			var fk fileKeys
			if err := yaml.Unmarshal(b, &fk); err != nil {
				return nil, err
			}
			for _, k := range fk.Keys {
				key, err := parseKey(k)
				if err != nil {
					return nil, err
				}
				s.keys = append(s.keys, key)
			}
		}
	}
	for i, p := range extraPlain {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		s.keys = append(s.keys, Key{
			Name:          fmt.Sprintf("env-%d", i),
			Hash:          HashKey(p),
			Prefix:        ids.KeyPrefix(p),
			Models:        []string{"*"},
			RPM:           60,
			MaxConcurrent: 4,
			Enabled:       true,
		})
	}
	return s, nil
}

func parseKey(k fileKey) (Key, error) {
	en := true
	if k.Enabled != nil {
		en = *k.Enabled
	}
	out := Key{
		Name:          k.Name,
		Models:        k.Models,
		RPM:           k.RPM,
		MaxConcurrent: k.MaxConcurrent,
		Enabled:       en,
	}
	if len(out.Models) == 0 {
		out.Models = []string{"*"}
	}
	if out.RPM <= 0 {
		out.RPM = 60
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = 2
	}
	switch {
	case k.Key != "":
		out.Hash = HashKey(k.Key)
		out.Prefix = ids.KeyPrefix(k.Key)
	case k.KeySHA256 != "":
		out.Hash = strings.ToLower(strings.TrimSpace(k.KeySHA256))
		out.Prefix = "sk-hashed"
	default:
		return Key{}, fmt.Errorf("key %q: need key or key_sha256", k.Name)
	}
	return out, nil
}

func (s *Service) Empty() bool {
	n := 0
	for _, k := range s.keys {
		if k.Enabled {
			n++
		}
	}
	return n == 0
}

func (s *Service) Lookup(plain string) (Key, bool) {
	if s.disabled {
		return Key{Name: "disabled", Hash: "disabled", Prefix: "sk-disabled", Models: []string{"*"}, RPM: 1000, MaxConcurrent: 32, Enabled: true}, true
	}
	if plain == "" {
		return Key{}, false
	}
	h := HashKey(plain)
	hb := []byte(h)
	var found Key
	ok := false
	for _, k := range s.keys {
		if !k.Enabled {
			continue
		}
		if subtle.ConstantTimeCompare(hb, []byte(k.Hash)) == 1 {
			found = k
			ok = true
			// keep scanning for constant time over the list
		}
	}
	return found, ok
}

func (s *Service) AllowModel(k Key, model string) bool {
	for _, m := range k.Models {
		if m == "*" || m == model {
			return true
		}
	}
	return false
}

func ClientIP(r *http.Request) string {
	if r == nil || r.RemoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Service) NoteFail(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	b := s.fails[ip]
	if b == nil || now.After(b.until) {
		b = &failBucket{until: now.Add(failWindow)}
		s.fails[ip] = b
	}
	b.n++
	return b.n > failMax
}

func (s *Service) FailLocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.fails[ip]
	if b == nil {
		return false
	}
	if time.Now().After(b.until) {
		delete(s.fails, ip)
		return false
	}
	return b.n > failMax
}

type Rate struct {
	Limit     int
	Remaining int
	Reset     int64
}

func (s *Service) AllowRPM(hash string, limit int) (Rate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	b := s.rpm[hash]
	if b == nil || now.Sub(b.window) >= time.Minute {
		b = &rpmBucket{window: now}
		s.rpm[hash] = b
	}
	reset := b.window.Add(time.Minute).Unix()
	if b.count >= limit {
		return Rate{Limit: limit, Remaining: 0, Reset: reset}, false
	}
	b.count++
	return Rate{Limit: limit, Remaining: limit - b.count, Reset: reset}, true
}

func (s *Service) AcquireKeySlot(hash string, max int) bool {
	if max <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[hash] >= max {
		return false
	}
	s.inflight[hash]++
	return true
}

func (s *Service) ReleaseKeySlot(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[hash] > 0 {
		s.inflight[hash]--
	}
}

func GeneratePlain() string {
	return ids.New("sk-")
}
