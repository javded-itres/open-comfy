package httpapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/javded-itres/open-comfy/internal/auth"
	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/config"
	"github.com/javded-itres/open-comfy/internal/files"
	"github.com/javded-itres/open-comfy/internal/importwf"
	"github.com/javded-itres/open-comfy/internal/jobs"
	"github.com/javded-itres/open-comfy/internal/queue"
)

type Server struct {
	Cfg   *config.Config
	Auth  *auth.Service
	Cat   *catalog.Catalog
	Comfy *comfy.Client
	Jobs  *jobs.Store
	Files *files.Store
	Admit *queue.Admission
	Log   *log.Logger
	DL    *importwf.Downloads

	videoCh chan string
	once    sync.Once
}

func New(cfg *config.Config, a *auth.Service, cat *catalog.Catalog, c *comfy.Client, j *jobs.Store, f *files.Store, admit *queue.Admission, dl *importwf.Downloads) *Server {
	return &Server{
		Cfg:     cfg,
		Auth:    a,
		Cat:     cat,
		Comfy:   c,
		Jobs:    j,
		Files:   f,
		Admit:   admit,
		Log:     log.New(os.Stderr, "", log.LstdFlags),
		DL:      dl,
		videoCh: make(chan string, 64),
	}
}

func (s *Server) StartWorkers(ctx context.Context) {
	s.once.Do(func() {
		go s.videoLoop(ctx)
		go s.cleanerLoop(ctx)
		for _, id := range s.Jobs.ActiveIDs() {
			select {
			case s.videoCh <- id:
			default:
			}
		}
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /docs", s.swaggerUI)
	mux.HandleFunc("GET /docs/", s.swaggerUI)
	mux.HandleFunc("GET /swagger", s.swaggerUI)
	mux.HandleFunc("GET /swagger/index.html", s.swaggerUI)
	mux.HandleFunc("GET /openapi.yaml", s.openapiYAMLHandler)
	mux.HandleFunc("GET /openapi.json", s.openapiJSON)
	mux.HandleFunc("GET /import", s.importPage)

	s.protect(mux, "GET /v1/models", s.listModels)
	s.protect(mux, "GET /models", s.listModels)
	s.protect(mux, "GET /v1/models/{id}", s.getModel)
	s.protect(mux, "GET /v1/images/models", s.listImageModels)
	s.protect(mux, "GET /v1/videos/models", s.listVideoModels)

	s.protect(mux, "POST /v1/images/generations", s.images)
	s.protect(mux, "POST /images/generations", s.images)
	s.protect(mux, "POST /v1/images", s.images)
	s.protect(mux, "POST /images", s.images)

	s.protect(mux, "POST /v1/videos", s.createVideo)
	s.protect(mux, "POST /videos", s.createVideo)
	s.protect(mux, "GET /v1/videos", s.listVideos)
	s.protect(mux, "GET /videos", s.listVideos)
	s.protect(mux, "GET /v1/videos/{id}", s.getVideo)
	s.protect(mux, "GET /videos/{id}", s.getVideo)
	s.protect(mux, "GET /v1/videos/{id}/content", s.videoContent)
	s.protect(mux, "DELETE /v1/videos/{id}", s.deleteVideo)
	s.protect(mux, "DELETE /videos/{id}", s.deleteVideo)

	s.protect(mux, "GET /v1/comfy/workflows", s.listComfyWorkflows)
	s.protect(mux, "POST /v1/comfy/analyze", s.analyzeComfyWorkflow)
	s.protect(mux, "POST /v1/comfy/provision", s.provisionComfyWorkflow)
	s.protect(mux, "GET /v1/comfy/downloads/{id}", s.comfyDownloadStatus)
	s.protect(mux, "GET /v1/comfy/queue", s.comfyQueue)
	s.protect(mux, "GET /v1/comfy/downloads", s.comfyDownloadsList)
	s.protect(mux, "POST /v1/comfy/import", s.importComfyWorkflows)
	s.protect(mux, "POST /v1/comfy/unload", s.unloadComfyModels)
	s.protect(mux, "DELETE /v1/models/{id}", s.deleteModel)
	s.protect(mux, "POST /v1/chat/completions", s.chat)
	s.protect(mux, "POST /chat/completions", s.chat)

	s.protect(mux, "POST /mcp", s.mcpPOST)
	s.protect(mux, "GET /mcp", s.mcpGET)
	s.protect(mux, "DELETE /mcp", s.mcpDELETE)
	mux.HandleFunc("OPTIONS /mcp", s.mcpOPTIONS)

	mux.HandleFunc("GET /v1/files/{id}", s.getFile)
	return mux
}

type ctxKey int

const keyPrincipal ctxKey = 1

type principal struct {
	Key  auth.Key
	Rate auth.Rate
}

func (s *Server) protect(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		ip := auth.ClientHost(r.RemoteAddr)
		if s.Auth.FailLocked(ip) {
			writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many failed auth attempts", "")
			return
		}
		if s.Cfg.Auth.Disabled {
			k, _ := s.Auth.Lookup("")
			r = r.WithContext(context.WithValue(r.Context(), keyPrincipal, principal{Key: k}))
			h(w, r)
			return
		}
		plain := auth.BearerToken(r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))
		k, ok := s.Auth.Lookup(plain)
		if !ok {
			if s.Auth.NoteFail(ip) {
				s.Log.Printf("auth: blocked ip=%s pattern=%s (failed auth attempts)", ip, pattern)
				writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many failed auth attempts", "")
				return
			}
			s.Log.Printf("auth: invalid key ip=%s pattern=%s", ip, pattern)
			writeError(w, 401, "invalid_request_error", "invalid_api_key", "invalid api key", "")
			return
		}
		rate, allowed := s.Auth.AllowRPM(k.Hash, k.RPM)
		w.Header().Set("X-RateLimit-Limit-Requests", strconv.Itoa(rate.Limit))
		w.Header().Set("X-RateLimit-Remaining-Requests", strconv.Itoa(rate.Remaining))
		w.Header().Set("X-RateLimit-Reset-Requests", strconv.FormatInt(rate.Reset, 10))
		if !allowed {
			w.Header().Set("Retry-After", "60")
			writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "rate limit exceeded", "")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), keyPrincipal, principal{Key: k, Rate: rate}))
		h(w, r)
	})
}

func (s *Server) principal(r *http.Request) principal {
	p, _ := r.Context().Value(keyPrincipal).(principal)
	return p
}

func (s *Server) origin() string { return s.Cfg.Origin() }

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "version": s.Cfg.Version})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Comfy.SystemStats(ctx); err != nil {
		writeJSON(w, 503, map[string]any{"status": "not_ready", "comfyui": "unreachable"})
		return
	}
	q, _ := s.Comfy.Queue(ctx)
	writeJSON(w, 200, map[string]any{
		"status": "ok", "comfyui": "ok",
		"queue_running": len(q.Running), "queue_pending": len(q.Pending),
	})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.Metrics.Enabled {
		http.NotFound(w, r)
		return
	}
	if config.MetricsNeedAuth(s.Cfg) {
		if _, ok := s.Auth.Lookup(auth.BearerToken(r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))); !ok && !s.Cfg.Auth.Disabled {
			writeError(w, 401, "invalid_request_error", "invalid_api_key", "invalid api key", "")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "opencomfy_jobs_active %d\n", s.Jobs.CountActive())
	fmt.Fprintf(w, "opencomfy_queue_waiting %d\n", s.Admit.Waiting())
	fmt.Fprintf(w, "opencomfy_gpu_inflight %d\n", s.Admit.InFlight())
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	sig, exp := q.Get("sig"), q.Get("exp")
	okHMAC := s.Files.Verify(id, exp, sig)
	var keyHash string
	if !okHMAC {
		plain := auth.BearerToken(r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))
		k, ok := s.Auth.Lookup(plain)
		if !ok && !s.Cfg.Auth.Disabled {
			writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
			return
		}
		keyHash = k.Hash
	}
	meta, b, err := s.Files.Get(id)
	if err != nil {
		writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
		return
	}
	if !okHMAC && meta != nil && meta.KeyHash != "" && keyHash != "" {
		if subtle.ConstantTimeCompare([]byte(meta.KeyHash), []byte(keyHash)) != 1 {
			writeError(w, 404, "invalid_request_error", "not_found", "not found", "")
			return
		}
	}
	mime := "application/octet-stream"
	if meta != nil && meta.MIME != "" {
		mime = meta.MIME
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

func (s *Server) maxBody(r *http.Request) io.ReadCloser {
	n := s.Cfg.HTTP.MaxBodyBytes
	if n <= 0 {
		n = 32 << 20
	}
	return http.MaxBytesReader(nil, r.Body, n)
}

func (s *Server) allowModel(w http.ResponseWriter, p principal, m *catalog.Model) bool {
	if !s.Auth.AllowModel(p.Key, m.ID) {
		writeError(w, 403, "invalid_request_error", "model_not_allowed", "model not allowed", "model")
		return false
	}
	return true
}

func sameHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func listenPort(ln net.Listener) int {
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		return ta.Port
	}
	return 0
}

func HTTPServer(cfg *config.Config, h http.Handler) *http.Server {
	to := time.Duration(cfg.HTTP.ReadHeaderTimeoutS) * time.Second
	if to <= 0 {
		to = 10 * time.Second
	}
	return &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		ReadHeaderTimeout: to,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

func ListenerPort(ln net.Listener) int {
	return listenPort(ln)
}
