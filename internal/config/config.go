package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const ExitMissingConfig = 2

type Config struct {
	Listen        string  `yaml:"listen"`
	PublicBaseURL string  `yaml:"public_base_url"`
	LogLevel      string  `yaml:"log_level"`
	HTTP          HTTP    `yaml:"http"`
	ComfyUI       ComfyUI `yaml:"comfyui"`
	Files         Files   `yaml:"files"`
	Jobs          Jobs    `yaml:"jobs"`
	Metrics       Metrics `yaml:"metrics"`
	Auth          Auth    `yaml:"auth"`
	DownloadsDir  string  `yaml:"downloads_dir"`
	ModelsFile    string  `yaml:"models_file"`
	WorkflowsDir  string  `yaml:"workflows_dir"`
	SkipWorkflow  bool    `yaml:"-"`
	Version       string  `yaml:"-"`
	BoundPort     int     `yaml:"-"`
}

type HTTP struct {
	MaxBodyBytes       int64 `yaml:"max_body_bytes"`
	MaxWaitS           int   `yaml:"max_wait_s"`
	AllowLongWait      bool  `yaml:"allow_long_wait"`
	ChatShim           bool  `yaml:"chat_shim"`
	ReadHeaderTimeoutS int   `yaml:"read_header_timeout_s"`
	AllowRemoteImages  bool  `yaml:"allow_remote_images"`
}

type ComfyUI struct {
	BaseURL         string            `yaml:"base_url"`
	AuthHeader      string            `yaml:"auth_header"`
	ExtraData       map[string]any    `yaml:"extra_data"`
	APIKeyEnv       string            `yaml:"api_key_env"`
	RequestTimeoutS int               `yaml:"request_timeout_s"`
	PollIntervalS   int               `yaml:"poll_interval_s"`
	MaxInFlight     int               `yaml:"max_in_flight"`
	MaxWaiting      int               `yaml:"max_waiting"`
	Exclusive       bool              `yaml:"exclusive"`
	AllowInterrupt  bool              `yaml:"allow_interrupt"`
	MinVersion      string            `yaml:"min_version"`
	CustomNodesDir  string            `yaml:"custom_nodes_dir"`
	NodeAllowlist   []string          `yaml:"node_allowlist"`
	ModelsDir       string            `yaml:"models_dir"`
	HFTokenEnv      string            `yaml:"hf_token_env"`
	HFAllowlist     []string          `yaml:"hf_allowlist"`
	ModelMap        map[string]string `yaml:"model_map"`
}

type Files struct {
	Dir            string `yaml:"dir"`
	TTL            string `yaml:"ttl"`
	MaxBytes       int64  `yaml:"max_bytes"`
	HMACSecretFile string `yaml:"hmac_secret_file"`
	HMACSecret     []byte `yaml:"-"`
}

type Jobs struct {
	Dir         string `yaml:"dir"`
	TTL         string `yaml:"ttl"`
	MaxActive   int    `yaml:"max_active"`
	StorePrompt bool   `yaml:"store_prompt"`
}

type Metrics struct {
	Enabled bool   `yaml:"enabled"`
	Auth    string `yaml:"auth"` // auto | true | false
}

type Auth struct {
	Disabled bool   `yaml:"disabled"`
	KeysFile string `yaml:"keys_file"`
}

func Defaults() Config {
	data := env("OPENCOMFY_DATA", "/var/lib/opencomfy")
	etc := "/etc/opencomfy"
	return Config{
		Listen:        env("OPENCOMFY_LISTEN", ":8788"),
		PublicBaseURL: os.Getenv("OPENCOMFY_PUBLIC_BASE_URL"),
		LogLevel:      "info",
		HTTP: HTTP{
			MaxBodyBytes:       32 << 20,
			MaxWaitS:           120,
			ChatShim:           true,
			ReadHeaderTimeoutS: 10,
		},
		ComfyUI: ComfyUI{
			BaseURL:         env("OPENCOMFY_COMFYUI_URL", "http://127.0.0.1:8188"),
			ExtraData:       map[string]any{},
			RequestTimeoutS: 60,
			PollIntervalS:   2,
			MaxInFlight:     2,
			MaxWaiting:      32,
			Exclusive:       true,
			MinVersion:      "0.3.7",
		},
		Files: Files{
			Dir:            filepath.Join(data, "files"),
			TTL:            "24h",
			MaxBytes:       20 << 30,
			HMACSecretFile: filepath.Join(data, "hmac.secret"),
		},
		Jobs: Jobs{
			Dir:       filepath.Join(data, "jobs"),
			TTL:       "48h",
			MaxActive: 256,
		},
		Metrics: Metrics{
			Enabled: true,
			Auth:    "auto",
		},
		Auth: Auth{
			KeysFile: filepath.Join(etc, "keys.yaml"),
		},
		ModelsFile:   filepath.Join(etc, "models.yaml"),
		WorkflowsDir: filepath.Join(etc, "workflows"),
		Version:      "0.1.1",
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path == "" {
		path = os.Getenv("OPENCOMFY_CONFIG")
	}
	if path == "" {
		return nil, fmt.Errorf("missing config: run opencomfy -init or copy configs/ (exit %d)", ExitMissingConfig)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("missing config %s: run opencomfy -init or copy configs/ (exit %d)", path, ExitMissingConfig)
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	applyEnv(&cfg)
	if cfg.HTTP.ReadHeaderTimeoutS <= 0 {
		cfg.HTTP.ReadHeaderTimeoutS = 10
	}
	if cfg.ComfyUI.MaxInFlight <= 0 {
		cfg.ComfyUI.MaxInFlight = 2
	}
	if cfg.ComfyUI.MaxWaiting <= 0 {
		cfg.ComfyUI.MaxWaiting = 32
	}
	if cfg.Jobs.MaxActive <= 0 {
		cfg.Jobs.MaxActive = 256
	}
	if cfg.Auth.Disabled && !IsLoopbackListen(cfg.Listen) {
		return nil, fmt.Errorf("auth.disabled is only allowed on loopback listen, got %q", cfg.Listen)
	}
	return &cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("OPENCOMFY_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("OPENCOMFY_COMFYUI_URL"); v != "" {
		c.ComfyUI.BaseURL = v
	}
	if v := os.Getenv("OPENCOMFY_PUBLIC_BASE_URL"); v != "" {
		c.PublicBaseURL = v
	}
	if v := os.Getenv("OPENCOMFY_DATA"); v != "" {
		c.Files.Dir = filepath.Join(v, "files")
		c.Jobs.Dir = filepath.Join(v, "jobs")
		if c.Files.HMACSecretFile == "" || strings.HasPrefix(c.Files.HMACSecretFile, "/var/lib/opencomfy") {
			c.Files.HMACSecretFile = filepath.Join(v, "hmac.secret")
		}
	}
}

func (c *Config) FileTTL() time.Duration {
	d, err := time.ParseDuration(c.Files.TTL)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

func (c *Config) JobTTL() time.Duration {
	d, err := time.ParseDuration(c.Jobs.TTL)
	if err != nil {
		return 48 * time.Hour
	}
	return d
}

func (c *Config) Origin() string {
	if u := strings.TrimRight(c.PublicBaseURL, "/"); u != "" {
		return u
	}
	port := c.BoundPort
	if port <= 0 {
		if _, p, err := net.SplitHostPort(c.Listen); err == nil {
			if n, conv := atoi(p); conv == nil && n > 0 {
				port = n
			}
		}
	}
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func IsLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func MetricsNeedAuth(c *Config) bool {
	switch strings.ToLower(c.Metrics.Auth) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	default:
		return !IsLoopbackListen(c.Listen)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not int")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func EnsureDirs(c *Config) error {
	for _, d := range []string{c.Files.Dir, c.Jobs.Dir, c.WorkflowsDir} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
