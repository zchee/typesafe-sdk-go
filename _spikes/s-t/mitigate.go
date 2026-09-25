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

package st

import (
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

// Limiter is F1 mitigation (ii): a semaphore of N requests in the transport,
// each held from RoundTrip until the response body is closed (or the error).
type Limiter struct {
	RT  http.RoundTripper
	sem chan struct{}
}

// NewLimiter returns a Limiter admitting n requests at once.
func NewLimiter(rt http.RoundTripper, n int) *Limiter {
	return &Limiter{RT: rt, sem: make(chan struct{}, n)}
}

// RoundTrip implements http.RoundTripper.
func (l *Limiter) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case l.sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	resp, err := l.RT.RoundTrip(req)
	if err != nil {
		<-l.sem
		return nil, err
	}
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: func() { <-l.sem }}
	return resp, nil
}

// releaseBody runs release once, on the first Close.
type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

// Close implements io.Closer.
func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

// WriteToken is F1 mitigation (iv): a token of one held from RoundTrip until
// the request's HEADERS are written (httptrace WroteHeaders,
// internal/http2/transport.go:1409) or RoundTrip returns. At most one
// caller is then between the pool's ReserveNewRequest and its header write,
// so the reservation count that awaitOpenSlotForStreamLocked adds
// (currentRequestCountLocked, internal/http2/transport.go:885-887) holds no
// foreign reservation and the strict wait is a wait for a real stream slot.
// It needs no knowledge of the server's MAX_CONCURRENT_STREAMS.
//
// With FirstHold set, the first request on a new connection
// (httptrace.GotConnInfo.Reused == false) keeps the token until RoundTrip
// returns its response headers, by which time the client has processed the
// server's SETTINGS (the first frame the server sends, RFC 9113 section
// 3.4); until then the client assumes 100 streams
// (initialMaxConcurrentStreams, internal/http2/transport.go:57,624).
//
// One WriteToken serves one transport, and RoundTrip takes the token before
// the wrapped RoundTrip runs: the pool reserves the stream
// (ReserveNewRequest, internal/http2/client_conn_pool.go:54,81) before
// GotConn fires (internal/http2/transport.go:418,424), so a token taken per
// connection at GotConn would leave the reservations uncounted and F1
// would return.
type WriteToken struct {
	RT        http.RoundTripper
	FirstHold bool
	// HoldBound, when positive, bounds a FirstHold hold: the token is
	// released HoldBound after the first request's WroteHeaders even when
	// its response headers have not arrived, so a first response the server
	// never sends blocks the other callers for at most HoldBound (the gate's
	// wait bound) instead of until the first request's context ends. Zero
	// keeps the token until RoundTrip returns.
	HoldBound time.Duration
	ch        chan struct{}
}

// NewWriteToken returns a WriteToken around rt.
func NewWriteToken(rt http.RoundTripper) *WriteToken {
	return &WriteToken{RT: rt, ch: make(chan struct{}, 1)}
}

// RoundTrip implements http.RoundTripper.
func (w *WriteToken) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case w.ch <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	var once sync.Once
	var first atomic.Bool
	var bound atomic.Pointer[time.Timer]
	release := func() { once.Do(func() { <-w.ch }) }
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if w.FirstHold && !info.Reused {
				first.Store(true)
			}
		},
		WroteHeaders: func() {
			switch {
			case !first.Load():
				release()
			case w.HoldBound > 0:
				bound.Store(time.AfterFunc(w.HoldBound, release))
			}
		},
	}
	resp, err := w.RT.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	release()
	if t := bound.Load(); t != nil {
		t.Stop()
	}
	return resp, err
}
