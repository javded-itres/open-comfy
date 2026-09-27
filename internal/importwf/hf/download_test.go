package hf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadsSetStorePersistsAndRestores(t *testing.T) {
	dir := t.TempDir()

	d := NewDownloads()
	d.SetStore(dir, time.Hour)

	// Start a job that fails immediately (no models dir) so it reaches a
	// persisted failed state quickly.
	job := d.Start(context.Background(), []WeightRef{
		{Value: "org/repo", Field: "model", Class: "SomeClass"},
	}, HFOpts{})

	if got := d.Get(job.ID); got == nil {
		t.Fatalf("job not tracked after Start")
	}

	// Wait for the store file to appear.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, job.ID+".json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("store file for %s not written", job.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A fresh instance should restore the job from disk.
	d2 := NewDownloads()
	d2.SetStore(dir, time.Hour)

	got := d2.Get(job.ID)
	if got == nil {
		t.Fatalf("job not restored from store")
	}
	if got.Status != job.Status {
		t.Fatalf("restored status = %q, want %q", got.Status, job.Status)
	}
	if len(got.Items) == 0 {
		t.Fatalf("restored job has no items")
	}
}

func TestDownloadsSetStoreTTLExpiry(t *testing.T) {
	dir := t.TempDir()

	d := NewDownloads()
	d.SetStore(dir, time.Hour)

	job := d.Start(context.Background(), []WeightRef{
		{Value: "org/repo", Field: "model", Class: "SomeClass"},
	}, HFOpts{})

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, job.ID+".json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("store file for %s not written", job.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Age the file beyond the TTL, then load — it must be dropped.
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dir, job.ID+".json"), old, old)

	d2 := NewDownloads()
	d2.SetStore(dir, time.Minute)

	if got := d2.Get(job.ID); got != nil {
		t.Fatalf("expired store file not removed, got %s", got.ID)
	}
}
