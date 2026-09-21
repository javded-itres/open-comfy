package importwf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/javded-itres/open-comfy/internal/ids"
)

type DLStatus string

const (
	DLQueued  DLStatus = "queued"
	DLRunning DLStatus = "in_progress"
	DLDone    DLStatus = "completed"
	DLFailed  DLStatus = "failed"
)

type DLItem struct {
	Value  string   `json:"value"`
	Field  string   `json:"field"`
	Class  string   `json:"class_type"`
	Repo   string   `json:"repo,omitempty"`
	File   string   `json:"file,omitempty"`
	Dest   string   `json:"dest,omitempty"`
	Local  string   `json:"local,omitempty"`
	Status DLStatus `json:"status"`
	Error  string   `json:"error,omitempty"`
	Bytes  int64    `json:"bytes"`
	Total  int64    `json:"total,omitempty"`
}

type DLJob struct {
	ID     string
	Status DLStatus
	Items  []DLItem
	Error  string
	mu     sync.Mutex
}

type DLJobView struct {
	ID     string   `json:"id"`
	Status DLStatus `json:"status"`
	Items  []DLItem `json:"items"`
	Error  string   `json:"error,omitempty"`
}

type Downloads struct {
	mu   sync.Mutex
	jobs map[string]*DLJob
}

func NewDownloads() *Downloads {
	return &Downloads{jobs: map[string]*DLJob{}}
}

func (d *Downloads) Get(id string) *DLJob {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[id]
}

type HFOpts struct {
	ModelsDir string
	Token     string
	Allow     []string
	ModelMap  map[string]string
}

func (d *Downloads) Start(ctx context.Context, missing []ModelNeed, opt HFOpts) *DLJob {
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
	var failed int
	for i := range j.Items {
		if err := downloadOne(ctx, &j.Items[i], opt); err != nil {
			j.Items[i].Status = DLFailed
			j.Items[i].Error = err.Error()
			failed++
		} else {
			j.Items[i].Status = DLDone
		}
	}
	if failed > 0 {
		j.setStatus(DLFailed)
		j.Error = fmt.Sprintf("%d download(s) failed", failed)
		return
	}
	j.setStatus(DLDone)
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

func downloadOne(ctx context.Context, it *DLItem, opt HFOpts) error {
	if opt.ModelsDir == "" {
		return fmt.Errorf("comfyui.models_dir is empty")
	}
	dest := expectedDest(opt.ModelsDir, it.Class, it.Field, it.Value)
	if !destInRoot(dest, opt.ModelsDir) {
		return fmt.Errorf("dest escapes models_dir")
	}
	it.Dest = dest
	if found, linked, ok := reuseLocalModel(opt.ModelsDir, it.Class, it.Field, it.Value); ok {
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
	cl := &http.Client{Timeout: 0}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("download %s: %s", resp.Status, b)
	}
	it.Total = resp.ContentLength
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	cerr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	it.Bytes = n
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
