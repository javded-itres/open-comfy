package queue

import (
	"context"
	"errors"
	"sync"
)

var ErrBusy = errors.New("busy")

type Admission struct {
	maxInFlight int
	maxWaiting  int

	gpu chan struct{}

	mu      sync.Mutex
	waiting int
	models  map[string]int
}

func New(maxInFlight, maxWaiting int) *Admission {
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	if maxWaiting <= 0 {
		maxWaiting = 32
	}
	return &Admission{
		maxInFlight: maxInFlight,
		maxWaiting:  maxWaiting,
		gpu:         make(chan struct{}, maxInFlight),
		models:      map[string]int{},
	}
}

func (a *Admission) Acquire(ctx context.Context, model string, modelMax int) (func(), error) {
	select {
	case a.gpu <- struct{}{}:
		if !a.takeModel(model, modelMax) {
			<-a.gpu
			return nil, ErrBusy
		}
		return a.release(model), nil
	default:
	}

	a.mu.Lock()
	if a.waiting >= a.maxWaiting {
		a.mu.Unlock()
		return nil, ErrBusy
	}
	a.waiting++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.waiting--
		a.mu.Unlock()
	}()

	select {
	case a.gpu <- struct{}{}:
		if !a.takeModel(model, modelMax) {
			<-a.gpu
			return nil, ErrBusy
		}
		return a.release(model), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *Admission) takeModel(model string, max int) bool {
	if max <= 0 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.models[model] >= max {
		return false
	}
	a.models[model]++
	return true
}

func (a *Admission) release(model string) func() {
	return func() {
		a.mu.Lock()
		if a.models[model] > 0 {
			a.models[model]--
		}
		a.mu.Unlock()
		select {
		case <-a.gpu:
		default:
		}
	}
}

func (a *Admission) Waiting() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waiting
}

func (a *Admission) InFlight() int {
	return len(a.gpu)
}
