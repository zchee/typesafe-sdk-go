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

package engine

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"runtime/debug"
	"sync"

	"github.com/zchee/typesafe-sdk-go/internal/h2gate"
)

// Transport is a client's HTTP transport, built once by resolve and shared
// by every request.
type Transport struct {
	// RT is what every request goes through: gate, or the caller's
	// WithRoundTripper.
	RT http.RoundTripper
	// Gate is the SDK's transport (the default one or WithHTTPTransport's
	// clone), nil under WithRoundTripper.
	Gate *h2gate.Transport
	// Idler is WithRoundTripper's rt when it has CloseIdleConnections, as
	// an *http.Transport has.
	Idler interface{ CloseIdleConnections() }
	// Closer is WithRoundTripper's rt when it is an io.Closer.
	Closer io.Closer
	// Trace is WithClientTrace's hooks, shielded per request.
	Trace  *httptrace.ClientTrace
	Logger *slog.Logger

	closeOnce sync.Once
	closeErr  error
}

// defaultHTTPVersion is the policy for a base URL scheme with no

// RoundTrip sends req through the transport. It returns the transport's
// error as it is; the root package maps a failure of the SDK's transport
// before a connection was had, and an API host that did not speak HTTP/2
// under HTTP2Only, to its error types (section 6.3, R67 Q3). A caller's
// trace hooks run under a shield: a panic in one is raised again on the
// caller once the round trip returns, never on a transport goroutine.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var sh *shield
	if trace := t.callerTrace(req.Context()); trace != nil {
		// net/http composes every trace on the context into the one it calls,
		// so the context net/http sees carries the shielded trace alone
		// (K28c).
		sh = NewShield(req.Context(), trace, t.Logger)
		defer sh.markReturned()
		req = req.WithContext(httptrace.WithClientTrace(UntracedContext{req.Context()}, &sh.trace))
	}
	resp, err := t.RT.RoundTrip(req)
	if sh != nil {
		sh.done(resp)
	}
	return resp, err
}

// callerTrace returns the caller's hooks that reach a request with context
// ctx: WithClientTrace's, then those of a trace on ctx, composed as
// httptrace composes them, or nil when there are none. It never changes
// t.trace or ctx's trace.
// callerTrace returns the caller's hooks that reach a request with context
// ctx: WithClientTrace's, then those of a trace on ctx, composed as
// httptrace composes them, or nil when there are none. It never changes
// t.trace or ctx's trace.
func (t *Transport) callerTrace(ctx context.Context) *httptrace.ClientTrace {
	onCtx := httptrace.ContextClientTrace(ctx)
	switch {
	case onCtx == nil:
		return t.Trace
	case t.Trace == nil:
		return onCtx
	}
	// WithClientTrace composes the trace it is given with the one already on
	// the context, in place; composing into a copy of the option's trace
	// leaves both originals as they were.
	merged := *t.Trace
	httptrace.WithClientTrace(httptrace.WithClientTrace(context.Background(), onCtx), &merged)
	return &merged
}

// UntracedContext is a request context without its httptrace values: net/http
// finds neither the caller's trace nor the net-level hooks httptrace derived
// from it, and composes only the shielded trace roundTrip installs above it.
// Deadline, cancellation, cause and every other value are the wrapped
// context's; context.WithCancel on it still finds the wrapped context's
// cancellation through Value, so it starts no goroutine.
type UntracedContext struct{ context.Context }

// traceKeys answers, with a non-nil value, for exactly the context keys
// httptrace.WithClientTrace sets: its client-event key and, for a trace
// with a net-level hook, the key of the net package's hooks.
var traceKeys = httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{ConnectStart: func(string, string) {}})

// Value returns nil for httptrace's keys and the wrapped context's value for
// every other key.
func (c UntracedContext) Value(key any) any {
	if traceKeys.Value(key) != nil {
		return nil
	}
	return c.Context.Value(key)
}

// Close releases the transport, once: the SDK's transport and a caller's
// WithHTTPTransport clone close their idle connections; a WithRoundTripper
// closes its idle connections when it has CloseIdleConnections, as an
// *http.Transport has, and is then closed when it is an io.Closer (AC-F9,
// R79). Later calls return the first call's result.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		if t.Gate != nil {
			t.Gate.CloseIdleConnections()
			return
		}
		if t.Idler != nil {
			t.Idler.CloseIdleConnections()
		}
		if t.Closer != nil {
			t.closeErr = t.Closer.Close()
		}
	})
	return t.closeErr
}

// Stats returns the SDK transport's counters, or zero values under
// WithRoundTripper.
func (t *Transport) Stats() h2gate.Stats {
	if t.Gate == nil {
		return h2gate.Stats{}
	}
	return t.Gate.Stats()
}

// shield wraps a caller's httptrace hooks for one request (K28, K28b, K28c):
// a hook that panics inside net/http, which may hold its connection pool's
// lock or a reserved stream there, is recovered in place, and done raises the
// panic again on the goroutine that called RoundTrip. A panic it does not
// raise is logged at WARN with its hook and stack, never its value, which
// may hold data.
type shield struct {
	trace  httptrace.ClientTrace
	ctx    context.Context // the request's, for the log records
	logger *slog.Logger

	mu       sync.Mutex
	returned bool
	panicked bool
	hook     string // the hook of the kept panic
	value    any
	stack    []byte
}

// NewShield returns a shield for a request with context ctx whose trace
// calls every hook of c, recovered.
func NewShield(ctx context.Context, c *httptrace.ClientTrace, logger *slog.Logger) *shield {
	s := &shield{ctx: ctx, logger: logger}
	if h := c.GetConn; h != nil {
		s.trace.GetConn = func(hostPort string) { defer s.recover("GetConn"); h(hostPort) }
	}
	if h := c.GotConn; h != nil {
		s.trace.GotConn = func(info httptrace.GotConnInfo) { defer s.recover("GotConn"); h(info) }
	}
	if h := c.PutIdleConn; h != nil {
		s.trace.PutIdleConn = func(err error) { defer s.recover("PutIdleConn"); h(err) }
	}
	if h := c.GotFirstResponseByte; h != nil {
		s.trace.GotFirstResponseByte = func() { defer s.recover("GotFirstResponseByte"); h() }
	}
	if h := c.Got100Continue; h != nil {
		s.trace.Got100Continue = func() { defer s.recover("Got100Continue"); h() }
	}
	if h := c.Got1xxResponse; h != nil {
		s.trace.Got1xxResponse = func(code int, header textproto.MIMEHeader) (err error) {
			defer s.recover("Got1xxResponse")
			return h(code, header)
		}
	}
	if h := c.DNSStart; h != nil {
		s.trace.DNSStart = func(info httptrace.DNSStartInfo) { defer s.recover("DNSStart"); h(info) }
	}
	if h := c.DNSDone; h != nil {
		s.trace.DNSDone = func(info httptrace.DNSDoneInfo) { defer s.recover("DNSDone"); h(info) }
	}
	if h := c.ConnectStart; h != nil {
		s.trace.ConnectStart = func(network, addr string) { defer s.recover("ConnectStart"); h(network, addr) }
	}
	if h := c.ConnectDone; h != nil {
		s.trace.ConnectDone = func(network, addr string, err error) { defer s.recover("ConnectDone"); h(network, addr, err) }
	}
	if h := c.TLSHandshakeStart; h != nil {
		s.trace.TLSHandshakeStart = func() { defer s.recover("TLSHandshakeStart"); h() }
	}
	if h := c.TLSHandshakeDone; h != nil {
		s.trace.TLSHandshakeDone = func(cs tls.ConnectionState, err error) { defer s.recover("TLSHandshakeDone"); h(cs, err) }
	}
	if h := c.WroteHeaderField; h != nil {
		s.trace.WroteHeaderField = func(key string, value []string) { defer s.recover("WroteHeaderField"); h(key, value) }
	}
	if h := c.WroteHeaders; h != nil {
		s.trace.WroteHeaders = func() { defer s.recover("WroteHeaders"); h() }
	}
	if h := c.Wait100Continue; h != nil {
		s.trace.Wait100Continue = func() { defer s.recover("Wait100Continue"); h() }
	}
	if h := c.WroteRequest; h != nil {
		s.trace.WroteRequest = func(info httptrace.WroteRequestInfo) { defer s.recover("WroteRequest"); h(info) }
	}
	return s
}

// recover is deferred by every wrapped hook: it stops a panic of the hook,
// keeps the first one before RoundTrip returned for done, and logs any
// other.
func (s *shield) recover(hook string) {
	v := recover()
	if v == nil {
		return
	}
	stack := debug.Stack()
	s.mu.Lock()
	late, first := s.returned, !s.panicked
	if !late && first {
		s.panicked, s.hook, s.value, s.stack = true, hook, v, stack
	}
	s.mu.Unlock()
	switch {
	case late:
		s.log("transport: trace hook panic after the request returned, recovered", hook, stack)
	case !first:
		s.log("transport: trace hook panic after an earlier one, recovered", hook, stack)
	}
}

// done ends the request's shield on the goroutine that called RoundTrip,
// once RoundTrip has returned: when a hook panicked, it closes resp's body,
// if any, logs the hook and its stack, and panics again with the hook's
// value.
func (s *shield) done(resp *http.Response) {
	s.mu.Lock()
	s.returned = true
	panicked, hook, v, stack := s.panicked, s.hook, s.value, s.stack
	s.mu.Unlock()
	if !panicked {
		return
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	s.log("transport: trace hook panic, raised again on the caller", hook, stack)
	panic(v)
}

// markReturned ends the request's shield when RoundTrip itself panicked, so
// done never ran; roundTrip defers it. A hook panic after it is logged, and
// one kept before it, which RoundTrip's panic replaces on the caller, is
// logged rather than lost. After done it does nothing.
func (s *shield) markReturned() {
	s.mu.Lock()
	if s.returned {
		s.mu.Unlock()
		return
	}
	s.returned = true
	panicked, hook, stack := s.panicked, s.hook, s.stack
	s.mu.Unlock()
	if panicked {
		s.log("transport: trace hook panic replaced by a RoundTrip panic, recovered", hook, stack)
	}
}

// log writes a WARN record about a hook's panic: the hook and the stack of
// the goroutine it panicked on.
func (s *shield) log(msg, hook string, stack []byte) {
	s.logger.LogAttrs(s.ctx, slog.LevelWarn, msg, slog.String("hook", hook), slog.String("stack", string(stack)))
}
