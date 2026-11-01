package statistics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"
)

type recorderKey struct{}
type sourceKey struct{}
type reasoningKey struct{}

func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}
func Source(ctx context.Context) string { v, _ := ctx.Value(sourceKey{}).(string); return v }
func WithReasoning(ctx context.Context, reasoning string) context.Context {
	return context.WithValue(ctx, reasoningKey{}, reasoning)
}

// Recorder belongs to one agent invocation. Children get their own recorder.
type Recorder struct {
	mu       sync.Mutex
	store    *Store
	base     Event
	seq      int
	started  time.Time
	pausedAt time.Time
	paused   time.Duration
	waiters  int
	onError  func(error)
}

func Begin(ctx context.Context, store *Store, base Event, onError func(error)) (context.Context, *Recorder) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		if onError != nil {
			onError(err)
		}
		return ctx, nil
	}
	now := time.Now()
	base.ID = hex.EncodeToString(id[:])
	base.Kind = "turn"
	base.At = now.UTC()
	base.StartedAt = base.At
	r := &Recorder{store: store, base: base, started: now, onError: onError}
	r.save(base)
	return context.WithValue(ctx, recorderKey{}, r), r
}

func (r *Recorder) save(e Event) {
	if err := r.store.Append(e); err != nil && r.onError != nil {
		r.onError(err)
	}
}

func (r *Recorder) Finish() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := time.Since(r.started) - r.paused
	if r.waiters > 0 {
		d -= time.Since(r.pausedAt)
	}
	e := r.base
	e.ActiveMS = Int(max(0, d.Milliseconds()))
	r.save(e)
}

// Pause excludes the union of permission and question waits, even when
// several parallel tools wait for a person at once.
func Pause(ctx context.Context) func() {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.waiters == 0 {
		r.pausedAt = time.Now()
	}
	r.waiters++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.waiters--
			if r.waiters == 0 {
				r.paused += time.Since(r.pausedAt)
			}
		})
	}
}

func RecordCall(ctx context.Context, provider, model, reasoning string, input, output, cached *int64) {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e := r.base
	e.ID += ":call:" + strconv.Itoa(r.seq)
	e.Kind = "call"
	e.At = time.Now().UTC()
	e.Provider = provider
	e.Model = model
	e.Requests = 0
	e.Skills = nil
	if v, ok := ctx.Value(reasoningKey{}).(string); ok {
		reasoning = v
	}
	if reasoning != "" {
		e.Reasoning = reasoning
	}
	e.Input, e.Output, e.Cached = input, output, cached
	r.save(e)
}

func RecordSkill(ctx context.Context, name string) {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.base.Skills = append(r.base.Skills, name)
	r.save(r.base)
}

func Background(ctx context.Context) {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.base.Source = "background"
	r.base.Requests = 0
	r.save(r.base)
}

func (r *Recorder) ResetRequests() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.base.Requests = 0
	r.save(r.base)
}

func (r *Recorder) FollowUp(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.base
	e.ID = "followup:" + e.Session + ":" + id
	e.Kind = "request"
	e.Source = "followup"
	e.Requests = 1
	e.At = time.Now().UTC()
	e.Skills = nil
	r.save(e)
}
