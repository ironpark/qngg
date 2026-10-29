package qngg

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Pool runs JavaScript from several goroutines by handing each one a
// runtime of its own. Runtimes are created on demand and reused; up to size
// idle runtimes are kept.
type Pool struct {
	config Config
	setup  func(*Runtime) error

	mu     sync.Mutex
	idle   []*Runtime
	size   int
	closed bool
}

// NewPool returns a pool keeping up to size idle runtimes created with
// config. setup, if not nil, prepares each new runtime, for example by
// defining globals.
func NewPool(size int, config Config, setup func(*Runtime) error) (*Pool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("pool size must be positive, got %d", size)
	}

	return &Pool{config: config, setup: setup, size: size}, nil
}

// Do runs fn with ctx and a runtime that no other goroutine uses until fn
// returns. Values created in fn must not be used after it returns. The
// runtime is returned to the pool unless fn panics or the runtime was
// interrupted or closed, in which case it is discarded. Globals fn defines
// stay in the runtime for later calls of Do: only what setup defines is
// guaranteed to be there.
func (p *Pool) Do(ctx context.Context, fn func(ctx context.Context, rt *Runtime) error) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context done before the call: %w", context.Cause(ctx))
	}

	rt, err := p.get()
	if err != nil {
		return err
	}

	reuse := false
	defer func() { p.put(rt, reuse) }()

	err = fn(ctx, rt)
	reuse = true

	return err
}

// Close closes the idle runtimes. Runtimes in use are closed when their Do
// returns.
func (p *Pool) Close() error {
	p.mu.Lock()
	idle := p.idle
	p.idle, p.closed = nil, true
	p.mu.Unlock()

	errs := make([]error, 0, len(idle))
	for _, rt := range idle {
		errs = append(errs, rt.Close())
	}

	return errors.Join(errs...)
}

func (p *Pool) get() (*Runtime, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return nil, errors.New("pool is closed")
	}

	if n := len(p.idle); n > 0 {
		rt := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()

		return rt, nil
	}
	p.mu.Unlock()

	rt, err := New(p.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime: %w", err)
	}

	if p.setup != nil {
		if err := p.setup(rt); err != nil {
			_ = rt.Close()

			return nil, fmt.Errorf("pool setup failed: %w", err)
		}
	}

	return rt, nil
}

func (p *Pool) put(rt *Runtime, reuse bool) {
	if reuse && rt.interruptErr() == nil {
		p.mu.Lock()
		if !p.closed && len(p.idle) < p.size {
			p.idle = append(p.idle, rt)
			p.mu.Unlock()

			return
		}
		p.mu.Unlock()
	}

	_ = rt.Close()
}
