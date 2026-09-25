// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package st is the throwaway W0.4 transport spike (plan §6.3, S-T1 to
// S-T5b): a minimal cold-start gate around the stock *http.Transport and the
// tests that measure it. It is not production code; internal/h2gate (W2.2)
// replaces it. The leading underscore of _spikes keeps the package out of
// ./..., CI, lint and coverage; run it with an explicit path.
package st

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

// State is the gate state of plan §6.3.
type State int

// The gate states.
const (
	Cold State = iota
	Dialing
	Warm
)

// String returns the state's name.
func (s State) String() string {
	switch s {
	case Cold:
		return "cold"
	case Dialing:
		return "dialing"
	case Warm:
		return "warm"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// Role is what a call did at the gate.
type Role int

// The roles a call can take.
const (
	// RoleWarm passed a warm gate straight to the transport.
	RoleWarm Role = iota
	// RoleLeader led a cold dial.
	RoleLeader
	// RoleWaiter waited for a leader and was released at its GotConn.
	RoleWaiter
	// RoleFallThrough waited until its bound expired and then called the
	// transport itself.
	RoleFallThrough
	// RoleWaiterFailed received a fresh copy of the leader's dial error.
	RoleWaiterFailed
	// RoleWaiterCtx ended because its own context ended while waiting.
	RoleWaiterCtx
)

// String returns the role's name.
func (r Role) String() string {
	return [...]string{"warm", "leader", "waiter", "fall-through", "waiter-failed", "waiter-ctx"}[r]
}

// ErrNotNegotiated is returned by [ALPNCheck] when the API hop did not
// negotiate h2 (h2gate.ErrNotNegotiated in W2.2).
var ErrNotNegotiated = errors.New("h2gate: HTTP/2 not negotiated")

// DialError is a failure before GotConn: dial, proxy, TLS or ALPN. Each
// caller gets its own value; waiters share Err with the leader.
type DialError struct {
	// Proxy is set when a *net.OpError with Op "proxyconnect" is in the chain.
	Proxy bool
	// Timeout is set when a net.Error with Timeout() in the chain reports
	// true.
	Timeout bool
	// NotNegotiated is set for ErrNotNegotiated or a remote alert 120.
	NotNegotiated bool
	// Err is the transport's error (shared by the leader and its waiters).
	Err error
}

// Error implements error.
func (e *DialError) Error() string {
	return fmt.Sprintf("h2gate: dial failed (proxy=%t timeout=%t not-negotiated=%t): %v", e.Proxy, e.Timeout, e.NotNegotiated, e.Err)
}

// Unwrap returns the shared cause.
func (e *DialError) Unwrap() error { return e.Err }

// Classify builds the DialError flags for err by walking its whole chain
// (errors.As stops at the first match, and a proxyconnect OpError wraps the
// remote-error OpError of alert 120).
func Classify(err error) *DialError {
	d := &DialError{Err: err}
	Walk(err, func(e error) {
		if oe, ok := e.(*net.OpError); ok {
			if oe.Op == "proxyconnect" {
				d.Proxy = true
			}
			if oe.Op == "remote error" && oe.Err != nil && oe.Err.Error() == "tls: no application protocol" {
				d.NotNegotiated = true
			}
		}
		if ne, ok := e.(net.Error); ok && ne.Timeout() {
			d.Timeout = true
		}
		if e == ErrNotNegotiated { //nolint:errorlint // Walk visits every node; identity is the test
			d.NotNegotiated = true
		}
	})
	return d
}

// Walk calls fn for err and every error it wraps, depth first, following
// both Unwrap() error and Unwrap() []error.
func Walk(err error, fn func(error)) {
	if err == nil {
		return
	}
	fn(err)
	switch u := err.(type) { //nolint:errorlint // walking the tree by hand is the point
	case interface{ Unwrap() error }:
		Walk(u.Unwrap(), fn)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			Walk(e, fn)
		}
	}
}

// outcome is how a leader's generation ended.
type outcome int

const (
	pending outcome = iota
	released
	failed
	leaderGone
)

// generation is one leader's dial as the waiters see it.
type generation struct {
	done    chan struct{}
	outcome outcome
	cause   *DialError // the leader's classified error when failed
	waiters int        // waiters currently parked on done
}

// Stats counts gate events.
type Stats struct {
	Leaders, Releases, Failures, Handovers, ColdResets int
	FallThroughs, WaiterFailures, WaiterCtxEnds        int
}

// Gate is the throwaway cold-start gate of plan §6.3: cold → dialing → warm.
// The first call leads; the others wait until the leader's GotConn, their
// own context, or WaitBound, whichever comes first. A bound expiry falls
// through to the transport. A leader failure while its context is alive
// gives every waiter a fresh DialError of the leader's class around the
// shared cause and resets the gate to cold. A leader whose context ended
// hands over to the first waiter to run (the gate stays dialing), or resets
// the gate to cold when nobody waits.
type Gate struct {
	// RT is the wrapped transport.
	RT http.RoundTripper
	// WaitBound bounds a waiter: connectTimeout + TLSHandshakeTimeout (+ the
	// proxy allowance); zero means no bound.
	WaitBound time.Duration
	// Disabled turns the gate into a pass-through, for no-gate baselines.
	Disabled bool

	mu       sync.Mutex
	state    State
	gen      *generation
	handover bool
	stats    Stats

	parked atomic.Int64 // waiters parked right now, for tests
}

// State returns the gate state.
func (g *Gate) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Stats returns a snapshot of the counters.
func (g *Gate) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

// Parked returns the number of waiters parked right now.
func (g *Gate) Parked() int { return int(g.parked.Load()) }

// RoundTrip implements http.RoundTripper.
func (g *Gate) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, _, err := g.Do(req)
	return resp, err
}

// Do is RoundTrip that also reports the call's role.
func (g *Gate) Do(req *http.Request) (*http.Response, Role, error) {
	if g.Disabled {
		resp, err := g.RT.RoundTrip(req)
		return resp, RoleWarm, err
	}
	var bound <-chan time.Time
	if g.WaitBound > 0 {
		t := time.NewTimer(g.WaitBound)
		defer t.Stop()
		bound = t.C
	}
	for {
		g.mu.Lock()
		switch {
		case g.state == Warm:
			g.mu.Unlock()
			resp, err := g.RT.RoundTrip(req)
			return resp, RoleWarm, err
		case g.state == Cold || g.handover:
			if g.state == Cold {
				g.gen = &generation{done: make(chan struct{})}
			}
			g.state = Dialing
			g.handover = false
			g.stats.Leaders++
			gen := g.gen
			g.mu.Unlock()
			resp, err := g.lead(req, gen)
			return resp, RoleLeader, err
		}
		gen := g.gen
		gen.waiters++
		g.mu.Unlock()
		g.parked.Add(1)

		select {
		case <-gen.done:
			g.parked.Add(-1)
			switch gen.outcome {
			case released:
				resp, err := g.RT.RoundTrip(req)
				return resp, RoleWaiter, err
			case failed:
				g.mu.Lock()
				g.stats.WaiterFailures++
				g.mu.Unlock()
				c := gen.cause
				return nil, RoleWaiterFailed, &DialError{Proxy: c.Proxy, Timeout: c.Timeout, NotNegotiated: c.NotNegotiated, Err: c.Err}
			default: // leaderGone: loop; the first to lock takes over
				continue
			}
		case <-req.Context().Done():
			g.leave(gen)
			g.mu.Lock()
			g.stats.WaiterCtxEnds++
			g.mu.Unlock()
			return nil, RoleWaiterCtx, req.Context().Err()
		case <-bound:
			g.leave(gen)
			g.mu.Lock()
			g.stats.FallThroughs++
			g.mu.Unlock()
			resp, err := g.RT.RoundTrip(req)
			return resp, RoleFallThrough, err
		}
	}
}

// leave unparks a waiter that stops waiting on gen for its own reason.
func (g *Gate) leave(gen *generation) {
	g.parked.Add(-1)
	g.mu.Lock()
	gen.waiters--
	g.mu.Unlock()
}

// lead runs the leader's request with a GotConn hook that releases the
// waiters, and resolves the generation when the request ends before
// GotConn.
func (g *Gate) lead(req *http.Request, gen *generation) (*http.Response, error) {
	var once sync.Once
	release := func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if gen.outcome != pending {
				return
			}
			g.state = Warm
			g.stats.Releases++
			gen.outcome = released
			close(gen.done)
		})
	}
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { release() }}
	resp, err := g.RT.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err == nil {
		release() // a response always follows GotConn; this is a no-op then
		return resp, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen.outcome != pending {
		return nil, err // failed after GotConn: per request, gate stays warm
	}
	if req.Context().Err() != nil {
		// The leader's own context ended before GotConn.
		gen.outcome = leaderGone
		if gen.waiters == 0 {
			g.state = Cold
			g.stats.ColdResets++
		} else {
			g.handover = true
			g.stats.Handovers++
			g.gen = &generation{done: make(chan struct{})}
		}
		close(gen.done)
		return nil, err
	}
	d := Classify(err)
	gen.outcome = failed
	gen.cause = d
	g.state = Cold
	g.stats.Failures++
	g.stats.ColdResets++
	close(gen.done)
	return nil, &DialError{Proxy: d.Proxy, Timeout: d.Timeout, NotNegotiated: d.NotNegotiated, Err: err}
}
