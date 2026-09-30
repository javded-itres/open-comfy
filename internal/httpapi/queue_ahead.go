package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/jobs"
	"github.com/javded-itres/open-comfy/internal/queue"
)

func (s *Server) kick(id string) {
	select {
	case s.videoCh <- id:
	default:
		go func() { s.videoCh <- id }()
	}
}

func (s *Server) generationBusy(ctx context.Context) bool {
	if s.Admit.InFlight() > 0 || s.Admit.Waiting() > 0 || s.Jobs.CountActive() > 0 {
		return true
	}
	return len(s.comfyIDs(ctx)) > 0
}

func (s *Server) comfyIDs(ctx context.Context) []string {
	s.qmu.Lock()
	if !s.qat.IsZero() && time.Since(s.qat) < 300*time.Millisecond {
		out := append([]string(nil), s.qids...)
		s.qmu.Unlock()
		return out
	}
	s.qmu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	q, err := s.Comfy.Queue(cctx)
	if err != nil {
		return nil
	}
	var ids []string
	for _, it := range q.Running {
		ids = append(ids, comfy.ExtractPromptID(it))
	}
	for _, it := range q.Pending {
		ids = append(ids, comfy.ExtractPromptID(it))
	}
	s.qmu.Lock()
	s.qat = time.Now()
	s.qids = ids
	s.qmu.Unlock()
	return ids
}

// queueAhead is how many generations will run before j.
// ComfyUI's own queue counts, plus local jobs not yet submitted there.
func (s *Server) queueAhead(ctx context.Context, j *jobs.Job) int {
	if j == nil || (j.Status != jobs.Queued && j.Status != jobs.InProgress) {
		return 0
	}
	ids := s.comfyIDs(ctx)
	index := map[string]int{}
	for i, id := range ids {
		if id != "" {
			index[id] = i
		}
	}
	if j.ComfyPromptID != "" {
		if i, ok := index[j.ComfyPromptID]; ok {
			return i
		}
	}
	if j.RequestedPromptID != "" {
		if i, ok := index[j.RequestedPromptID]; ok {
			return i
		}
	}
	before := 0
	base := len(ids)
	if inf := s.Admit.InFlight(); inf > base {
		// A synchronous image holds a slot without a job row.
		base = inf
	}
	for _, other := range s.Jobs.Active() {
		if other.ID == j.ID || !jobBefore(other, j) {
			continue
		}
		if other.ComfyPromptID != "" {
			if _, ok := index[other.ComfyPromptID]; ok {
				continue
			}
		}
		if other.RequestedPromptID != "" {
			if _, ok := index[other.RequestedPromptID]; ok {
				continue
			}
		}
		before++
	}
	return base + before
}

// acquireSlot waits out a model-cap miss. Acquire returns ErrBusy immediately
// when the GPU token is free but this model is already at max_concurrent.
func (s *Server) acquireSlot(ctx context.Context, model string, max int) (func(), error) {
	for {
		rel, err := s.Admit.Acquire(ctx, model, max)
		if err == nil {
			return rel, nil
		}
		if !errors.Is(err, queue.ErrBusy) {
			return nil, err
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func jobBefore(a, b *jobs.Job) bool {
	if a.Seq != 0 && b.Seq != 0 && a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	return a.ID < b.ID
}
