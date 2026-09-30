package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
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

var png1 = mustDec("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDec(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func testdata() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata")
}

func mockComfy(t *testing.T) *httptest.Server {
	return mockComfyOpt(t, `{"queue_running":[],"queue_pending":[]}`, nil)
}

func mockComfyOpt(t *testing.T, queue string, holdPrompt <-chan struct{}) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	files := map[string][]byte{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/object_info":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"SaveImage":{"name":"SaveImage","output_node":true,"input":{"required":{}}},"CLIPLoader":{"name":"CLIPLoader","input":{"required":{"clip_name":[["qwen/qwen_3_4b.safetensors"]]}}}}`))
		case r.URL.Path == "/userdata" && r.Method == http.MethodGet:
			w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/userdata/"):
			key := strings.TrimPrefix(r.URL.Path, "/userdata/")
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodPost {
				b, _ := io.ReadAll(r.Body)
				files[key] = b
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`"` + key + `"`))
				return
			}
			if b, ok := files[key]; ok {
				w.Header().Set("Content-Type", "application/json")
				w.Write(b)
				return
			}
			http.NotFound(w, r)
		case r.URL.Path == "/system_stats":
			w.Write([]byte(`{}`))
		case r.URL.Path == "/prompt" && r.Method == http.MethodPost:
			if holdPrompt != nil {
				<-holdPrompt
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"prompt_id":"11111111-1111-1111-1111-111111111111"}`))
		case r.URL.Path == "/queue":
			w.Write([]byte(queue))
		case strings.HasPrefix(r.URL.Path, "/history/"):
			id := strings.TrimPrefix(r.URL.Path, "/history/")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"`+id+`":{"outputs":{`+
				`"9":{"images":[{"filename":"out.png","subfolder":"","type":"output"}]},`+
				`"save_video":{"gifs":[{"filename":"out.mp4","subfolder":"","type":"output","format":"video/h264-mp4"}]},`+
				`"5218":{"gifs":[{"filename":"out.mp4","subfolder":"","type":"output","format":"video/h264-mp4"}]}`+
				`}}}`)
		case r.URL.Path == "/view":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png1)
		case r.URL.Path == "/upload/image":
			w.Write([]byte(`{"name":"up.png","subfolder":"","type":"input"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func testServer(t *testing.T, comfyURL string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
	})
	secret := bytes.Repeat([]byte{7}, 32)
	cfg := config.Defaults()
	cfg.Listen = "127.0.0.1:0"
	cfg.PublicBaseURL = ""
	cfg.Auth.Disabled = false
	cfg.Auth.KeysFile = filepath.Join(dir, "keys.yaml")
	cfg.ModelsFile = filepath.Join(testdata(), "models.yaml")
	cfg.WorkflowsDir = filepath.Join(testdata(), "workflows")
	cfg.Files.Dir = filepath.Join(dir, "files")
	cfg.Jobs.Dir = filepath.Join(dir, "jobs")
	cfg.Files.HMACSecret = secret
	cfg.BoundPort = 8788
	cfg.HTTP.ChatShim = true
	cfg.ComfyUI.PollIntervalS = 0
	_ = os.MkdirAll(cfg.Files.Dir, 0o700)
	_ = os.MkdirAll(cfg.Jobs.Dir, 0o700)
	plain := "sk-test-aaaaaaaaaaaaaaaa"
	os.WriteFile(cfg.Auth.KeysFile, []byte("keys:\n  - name: t\n    key: "+plain+"\n    models: [\"*\"]\n    rpm: 100\n    max_concurrent: 8\n"), 0o600)
	a, err := auth.Load(cfg.Auth.KeysFile, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(cfg.ModelsFile, cfg.WorkflowsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	cl := comfy.New(comfyURL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	js := jobs.New(cfg.Jobs.Dir, time.Hour)
	fs := files.New(cfg.Files.Dir, secret, time.Hour, cfg.Origin)
	dl := importwf.NewDownloads()
	dl.SetStore(filepath.Join(dir, "downloads"), time.Hour)
	s := New(&cfg, a, cat, cl, js, fs, queue.New(cfg.ComfyUI.MaxInFlight, cfg.ComfyUI.MaxWaiting), dl)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.StartWorkers(ctx)
	return s, plain
}

func TestSwagger(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, _ := testServer(t, cu.URL)
	h := s.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/docs", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "swagger-ui") {
		t.Fatalf("docs %d %s", rr.Code, rr.Body.String()[:min(200, rr.Body.Len())])
	}
	if !strings.Contains(rr.Body.String(), "color-scheme: light") {
		t.Fatal("expected light swagger theme")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/openapi.json", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] == nil {
		t.Fatal("missing openapi")
	}
}

func TestHealthAndAuth(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/health", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/models", nil))
	if rr.Code != 401 {
		t.Fatalf("want 401 got %d %s", rr.Code, rr.Body.Bytes())
	}

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
}

func TestImportLoginGate(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/import", nil))
	if rr.Code != 401 {
		t.Fatalf("want 401 without key, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "login-form") {
		t.Fatal("expected login page body")
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/import", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("want 200 with key, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Import ComfyUI workflows") {
		t.Fatal("expected import page body")
	}

	// A browser navigation cannot send Authorization. The login page stores
	// the key in a cookie, and the next GET /import must open the panel.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/import/session", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("session status %d %s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != "opencomfy_key" || !cookie[0].HttpOnly {
		t.Fatalf("cookie %#v", cookie)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/import", nil)
	req.AddCookie(cookie[0])
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Import ComfyUI workflows") {
		t.Fatalf("cookie gate %d %s", rr.Code, rr.Body.String()[:80])
	}
}

func TestImageQueuedWhenComfyBusy(t *testing.T) {
	hold := make(chan struct{})
	cu := mockComfyOpt(t, `{"queue_running":[["1","aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"]],"queue_pending":[]}`, hold)
	defer cu.Close()
	defer close(hold)
	s, key := testServer(t, cu.URL)
	h := s.Handler()

	post := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"toy-image","prompt":"a cube"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := post()
	if first["status"] != "queued" {
		t.Fatalf("first %+v", first)
	}
	if int(first["queue_ahead"].(float64)) != 1 {
		t.Fatalf("ahead %+v", first["queue_ahead"])
	}
	second := post()
	if second["status"] != "queued" {
		t.Fatalf("second %+v", second)
	}
	if int(second["queue_ahead"].(float64)) < 2 {
		t.Fatalf("second ahead %+v", second["queue_ahead"])
	}
	id, _ := first["id"].(string)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"queue_ahead"`) {
		t.Fatalf("poll %d %s", rec.Code, rec.Body.String())
	}
}

func TestImagesB64(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	body := `{"model":"toy-image","prompt":"a cube"}`
	req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Data) == 0 || out.Data[0].B64 == "" {
		t.Fatal(rr.Body.String())
	}
}

func TestImageURLNoAuthHeader(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	s.Cfg.PublicBaseURL = "http://127.0.0.1:8788"
	body := `{"model":"toy-image","prompt":"a cube","response_format":"url"}`
	req := httptest.NewRequest("POST", "/images/generations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var out struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Data[0].URL == "" {
		t.Fatal(rr.Body.String())
	}
	if strings.Contains(out.Data[0].URL, "http://:8788") {
		t.Fatal("bad origin", out.Data[0].URL)
	}
	u := out.Data[0].URL
	path := strings.TrimPrefix(u, "http://127.0.0.1:8788")
	gr := httptest.NewRequest("GET", path, nil)
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, gr)
	if rr.Code != 200 {
		t.Fatalf("GET url without auth: %d %s", rr.Code, rr.Body.String())
	}
}

func TestVideosCompletedURL(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	s.Cfg.PublicBaseURL = "http://127.0.0.1:8788"
	body := `{"model":"minimax-hailuo-02","prompt":"waves","seconds":"6"}`
	req := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Status != "queued" && created.Status != "in_progress" && created.Status != "completed" {
		t.Fatal(rr.Body.String())
	}
	var job struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	for i := 0; i < 50; i++ {
		gr := httptest.NewRequest("GET", "/v1/videos/"+created.ID, nil)
		gr.Header.Set("Authorization", "Bearer "+key)
		rr = httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, gr)
		json.Unmarshal(rr.Body.Bytes(), &job)
		if job.Status == "completed" {
			break
		}
		if job.Status == "failed" {
			t.Fatal(rr.Body.String())
		}
		time.Sleep(30 * time.Millisecond)
	}
	if job.Status != "completed" || job.URL == "" {
		t.Fatalf("%+v body=%s", job, rr.Body.String())
	}
	path := strings.TrimPrefix(job.URL, "http://127.0.0.1:8788")
	gr := httptest.NewRequest("GET", path, nil)
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, gr)
	if rr.Code != 200 {
		t.Fatalf("holix-media GET url: %d", rr.Code)
	}
}

func TestChatShim(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	body := `{"model":"toy-image","messages":[{"role":"user","content":"red cube"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
}

func TestAnalyzeAndProvision(t *testing.T) {
	importwf.UseEmptyNodeMap()
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()
	body := `{"name":"demo.json","workflow":{"nodes":[{"id":1,"type":"TotallyFakeNode"},{"id":9,"type":"SaveImage"}],"links":[]}}`
	req := httptest.NewRequest("POST", "/v1/comfy/analyze", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TotallyFakeNode") {
		t.Fatal(rr.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/comfy/provision", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"saved":true`) {
		t.Fatal(rr.Body.String())
	}
}

func TestWriteTimeoutZero(t *testing.T) {
	cfg := config.Defaults()
	srv := HTTPServer(&cfg, http.NotFoundHandler())
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Fatalf("timeouts write=%v read=%v", srv.WriteTimeout, srv.ReadTimeout)
	}
}

func TestOriginWildcardListen(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listen = ":8788"
	cfg.PublicBaseURL = ""
	cfg.BoundPort = 8788
	if cfg.Origin() != "http://127.0.0.1:8788" {
		t.Fatal(cfg.Origin())
	}
}
