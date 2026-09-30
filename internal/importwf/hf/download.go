package hf

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/javded-itres/open-comfy/internal/ids"
)

// progressReader counts bytes read from an upstream reader and reports the
// cumulative total through onChunk on every Read. It is always used by pointer,
// never copied, so its internal mutex is not moved.
type progressReader struct {
	r       io.Reader
	mu      sync.Mutex
	cum     int64
	onChunk func(cum int64)
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.mu.Lock()
	pr.cum += int64(n)
	fn := pr.onChunk
	pr.mu.Unlock()
	if fn != nil {
		fn(pr.cum)
	}
	return n, err
}

type DLStatus string

const (
	DLQueued  DLStatus = "queued"
	DLRunning DLStatus = "in_progress"
	DLDone    DLStatus = "completed"
	DLFailed  DLStatus = "failed"
)

type DLItem struct {
	Value     string   `json:"value"`
	Field     string   `json:"field"`
	Class     string   `json:"class_type"`
	ModelType string   `json:"model_type,omitempty"`
	Repo      string   `json:"repo,omitempty"`
	File      string   `json:"file,omitempty"`
	Dest      string   `json:"dest,omitempty"`
	Local     string   `json:"local,omitempty"`
	Status    DLStatus `json:"status"`
	Error     string   `json:"error,omitempty"`
	Bytes     int64    `json:"bytes"`
	Total     int64    `json:"total,omitempty"`
}

type DLJob struct {
	ID          string
	Status      DLStatus
	Items       []DLItem
	Error       string
	lastPersist time.Time
	mu          sync.Mutex
}

type DLJobView struct {
	ID     string   `json:"id"`
	Status DLStatus `json:"status"`
	Items  []DLItem `json:"items"`
	Error  string   `json:"error,omitempty"`
}

type Downloads struct {
	mu            sync.Mutex
	jobs          map[string]*DLJob
	maxConcurrent int
	limiter       chan struct{}
	storeDir      string
	storeTTL      time.Duration
}

func NewDownloads() *Downloads {
	return &Downloads{jobs: map[string]*DLJob{}, maxConcurrent: 0}
}

// SetMaxConcurrent caps the number of concurrent downloads at the process
// level. Zero (default) means unlimited. Must be called before Start.
func (d *Downloads) SetMaxConcurrent(n int) {
	if d == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxConcurrent = n
	d.limiter = make(chan struct{}, n)
}

// SetStore enables persistence of job snapshots to dir. Each job is written as
// a JSON file "<id>.json" atomically (write .tmp + rename) whenever its
// progress changes. On startup, files still within ttl are loaded back so that
// progress survives a restart.
func (d *Downloads) SetStore(dir string, ttl time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeDir = dir
	d.storeTTL = ttl
	d.loadLocked()
}

// loadLocked restores in-progress jobs from disk. Call with d.mu held.
func (d *Downloads) loadLocked() {
	if d.storeDir == "" {
		return
	}
	if err := os.MkdirAll(d.storeDir, 0o755); err != nil {
		return
	}
	entries, err := os.ReadDir(d.storeDir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(d.storeDir, e.Name())
		if d.storeTTL > 0 {
			if st, err := os.Stat(p); err == nil && now.Sub(st.ModTime()) > d.storeTTL {
				_ = os.Remove(p)
				continue
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var view DLJobView
		if json.Unmarshal(b, &view) != nil {
			continue
		}
		if _, exists := d.jobs[view.ID]; !exists {
			d.jobs[view.ID] = &DLJob{
				ID:     view.ID,
				Status: view.Status,
				Error:  view.Error,
				Items:  view.Items,
			}
		}
	}
}

// maybePersist writes the current snapshot of a job to disk if a store dir is
// configured. Failures are silent — persistence is best effort.
func (d *Downloads) maybePersist(j *DLJob) {
	if d == nil || j == nil {
		return
	}
	d.mu.Lock()
	dir := d.storeDir
	d.mu.Unlock()
	if dir == "" {
		return
	}
	data, err := json.Marshal(j.Snapshot())
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, j.ID+".json.tmp")
	final := filepath.Join(dir, j.ID+".json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
	}
}

func (d *Downloads) Get(id string) *DLJob {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[id]
}

// List returns snapshots of every tracked job in stable id order.
func (d *Downloads) List() []DLJobView {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.jobs))
	for id := range d.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]DLJobView, 0, len(ids))
	for _, id := range ids {
		out = append(out, d.jobs[id].Snapshot())
	}
	return out
}

type HFOpts struct {
	ModelsDir string
	Token     string
	Allow     []string
	ModelMap  map[string]string
}

// WeightRef is the slice of a missing weight that the downloader needs.
// Analysis keeps the richer ModelNeed; callers map into this.
type WeightRef struct {
	Value string
	Field string
	Class string
}

func (d *Downloads) Start(ctx context.Context, missing []WeightRef, opt HFOpts) *DLJob {
	j := &DLJob{ID: ids.New("hf_"), Status: DLQueued}
	for _, m := range missing {
		j.Items = append(j.Items, DLItem{
			Value: m.Value, Field: m.Field, Class: m.Class, Status: DLQueued,
		})
	}
	d.mu.Lock()
	d.jobs[j.ID] = j
	d.mu.Unlock()
	go d.run(ctx, j, opt)
	return j
}

func (d *Downloads) run(parent context.Context, j *DLJob, opt HFOpts) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	j.setStatus(DLRunning)

	// Process-level rate limiter. Each downloadOne grabs a slot; a zero
	// maxConcurrent means unlimited (limiter is nil).
	acquire := func() {}
	release := func() {}
	if d != nil {
		d.mu.Lock()
		lim := d.limiter
		d.mu.Unlock()
		if lim != nil {
			acquire = func() {
				select {
				case lim <- struct{}{}:
				case <-ctx.Done():
				}
			}
			release = func() { <-lim }
		}
	}

	var failed int
	for i := range j.Items {
		j.mu.Lock()
		done := j.Items[i].Status == DLDone
		if !done {
			j.Items[i].Status = DLRunning
		}
		j.mu.Unlock()
		if done {
			continue
		}
		d.maybePersist(j)
		acquire()
		var err error
		var last int64
		for attempt := 0; attempt < 8; attempt++ {
			err = downloadOne(ctx, d, j, i, &j.Items[i], opt)
			if err == nil || ctx.Err() != nil {
				break
			}
			j.mu.Lock()
			got := j.Items[i].Bytes
			j.Items[i].Error = err.Error()
			j.mu.Unlock()
			d.maybePersist(j)
			if attempt > 0 && got <= last {
				break
			}
			last = got
			timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
			case <-timer.C:
			}
		}
		release()
		if err != nil {
			j.Items[i].Status = DLFailed
			j.Items[i].Error = err.Error()
			failed++
		} else {
			j.Items[i].Status = DLDone
		}
		d.maybePersist(j)
	}
	if failed > 0 {
		j.setStatus(DLFailed)
		j.Error = fmt.Sprintf("%d download(s) failed", failed)
		d.maybePersist(j)
		return
	}
	j.setStatus(DLDone)
	d.maybePersist(j)
}

func (j *DLJob) setStatus(s DLStatus) {
	j.mu.Lock()
	j.Status = s
	j.mu.Unlock()
}

func (j *DLJob) Snapshot() DLJobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return DLJobView{
		ID:     j.ID,
		Status: j.Status,
		Error:  j.Error,
		Items:  append([]DLItem{}, j.Items...),
	}
}

func (d *Downloads) ResumeIncomplete(opt HFOpts) {
	if d == nil {
		return
	}
	d.mu.Lock()
	var pending []*DLJob
	for _, j := range d.jobs {
		if j.Status == DLQueued || j.Status == DLRunning {
			pending = append(pending, j)
		}
	}
	d.mu.Unlock()
	for _, j := range pending {
		go d.run(context.Background(), j, opt)
	}
}

func downloadOne(ctx context.Context, d *Downloads, j *DLJob, i int, it *DLItem, opt HFOpts) error {
	if opt.ModelsDir == "" {
		return fmt.Errorf("comfyui.models_dir is empty")
	}
	dest := expectedDest(opt.ModelsDir, it.Class, it.Field, it.Value)
	if !destInRoot(dest, opt.ModelsDir) {
		return fmt.Errorf("dest escapes models_dir")
	}
	it.Dest = dest
	it.ModelType = modelFolder(it.Class, it.Field)
	if found, linked, ok := ReuseLocalModel(opt.ModelsDir, it.Class, it.Field, it.Value); ok {
		it.Local = found
		it.Dest = linked
		if st, err := os.Stat(linked); err == nil {
			it.Bytes = st.Size()
			it.Total = st.Size()
		}
		return nil
	}
	hit, err := ResolveHF(ctx, it.Value, opt.Token, opt.Allow, opt.ModelMap, modelFolder(it.Class, it.Field))
	if err != nil {
		return err
	}
	it.Repo, it.File = hit.Repo, hit.File
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	u := hfFileURL(hit.Repo, hit.File)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "OpenComfy")
	if opt.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opt.Token)
	}
	tmp := dest + ".part"
	var resume int64
	if st, err := os.Stat(tmp); err == nil && st.Size() > 0 {
		resume = st.Size()
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resume))
	}
	// Hugging Face drops long HTTP/2 bodies with CANCEL. HTTP/1.1 keeps the .part usable.
	cl := &http.Client{Timeout: 0, Transport: hfTransport}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("download %s: %s", resp.Status, b)
	}
	// A 200 means the server ignored Range and sent the file from byte 0.
	if resume > 0 && resp.StatusCode != http.StatusPartialContent {
		resume = 0
	}
	clen := resolveContentLength(u, resp, opt.Token)
	if resume > 0 && clen > 0 {
		it.Total = resume + clen
	} else if clen > 0 {
		it.Total = clen
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if resume == 0 {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return err
		}
	} else if _, err := f.Seek(resume, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	if j != nil {
		j.mu.Lock()
		j.Items[i].Bytes = resume
		j.mu.Unlock()
	}
	// Live per-chunk progress: progress wraps resp.Body and reports cumulative
	// bytes as io.Copy reads them, so the UI shows progress while the download
	// runs instead of only at completion. Cumulative bytes include the .part
	// prefix when the server honoured Range.
	progress := &progressReader{r: resp.Body}
	progress.onChunk = func(cum int64) {
		if j == nil {
			return
		}
		persist := false
		j.mu.Lock()
		j.Items[i].Bytes = resume + cum
		if time.Since(j.lastPersist) > time.Second {
			j.lastPersist = time.Now()
			persist = true
		}
		j.mu.Unlock()
		if persist && d != nil {
			d.maybePersist(j)
		}
	}
	n, err := io.Copy(f, progress)
	cerr := f.Close()
	// Keep .part. A dropped connection is resumed with Range on the next attempt.
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cerr != nil {
		return cerr
	}
	if j != nil {
		j.mu.Lock()
		j.Items[i].Bytes = resume + n
		j.mu.Unlock()
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// hfTransport disables HTTP/2. Large weight downloads otherwise die with
// "stream error: CANCEL; received from peer" and lose the partial file.
var hfTransport = &http.Transport{
	Proxy:             http.ProxyFromEnvironment,
	ForceAttemptHTTP2: false,
	TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
}

// resolveContentLength returns the download size for a HEAD/GET response,
// falling back to a HEAD request when the GET lacks a Content-Length
// (chunked transfer encoding). A zero return means the size is unknown.
func resolveContentLength(u string, resp *http.Response, token string) int64 {
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	req, err := http.NewRequestWithContext(resp.Request.Context(), http.MethodHead, u, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "OpenComfy")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	hr, err := cl.Do(req)
	if err != nil {
		// A HEAD failure is not fatal — report bytes as they arrive.
		return 0
	}
	defer hr.Body.Close()
	if hr.StatusCode >= 300 {
		return 0
	}
	return hr.ContentLength
}
