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
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// result prints one machine-readable result line: RESULT key=value ...
// Values are printed with %v; strings containing spaces with %q.
func result(kv ...any) {
	var b strings.Builder
	b.WriteString("RESULT")
	for i := 0; i+1 < len(kv); i += 2 {
		v := fmt.Sprint(kv[i+1])
		if strings.ContainsAny(v, " \t\"=") || v == "" {
			v = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(&b, " %v=%s", kv[i], v)
	}
	fmt.Fprintln(os.Stdout, b.String())
}

// pct returns the nearest-rank percentile p (0 < p <= 1) of ds.
func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := max(int(math.Ceil(p*float64(len(s))))-1, 0)
	return s[i]
}

// ms formats d in milliseconds with three decimals.
func ms(d time.Duration) string { return fmt.Sprintf("%.3f", float64(d)/float64(time.Millisecond)) }

// chain describes every error in err's tree: %T and the message, depth first.
func chain(err error) string {
	var parts []string
	Walk(err, func(e error) { parts = append(parts, fmt.Sprintf("%T(%s)", e, e.Error())) })
	return strings.Join(parts, " -> ")
}

// call is one completed request.
type call struct {
	Start, GotConn, WroteHeaders, Done time.Time
	Role                               Role
	Err                                error
	Status, ProtoMajor                 int
	Body                               string
}

// do sends a GET for url through g with ctx and records the timeline.
func do(ctx context.Context, g *Gate, url string) call {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return call{Err: err}
	}
	req, tr := Traced(req)
	c := call{Start: time.Now()}
	resp, role, err := g.Do(req)
	c.Role, c.Err = role, err
	if err == nil {
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		c.Status, c.ProtoMajor, c.Body = resp.StatusCode, resp.ProtoMajor, string(b)
		if rerr != nil {
			c.Err = rerr
		}
	}
	c.Done = time.Now()
	c.GotConn, c.WroteHeaders = tr.Get()
	return c
}

// fanOut starts n calls at once (after a common start signal) and returns
// them in index order.
func fanOut(n int, fn func(i int) call) []call {
	out := make([]call, n)
	var ready, wg sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	for i := range n {
		wg.Go(func() {
			ready.Done()
			<-start
			out[i] = fn(i)
		})
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return out
}

// barrier holds every handler until n requests with the same key arrived,
// or until guard expires (the ordering assertion then fails).
type barrier struct {
	n     int
	guard time.Duration

	mu      sync.Mutex
	arrived map[string]int
	all     map[string]chan struct{}
	guarded map[string]int // handlers released by the guard, per key
}

func newBarrier(n int, guard time.Duration) *barrier {
	return &barrier{n: n, guard: guard, arrived: map[string]int{}, all: map[string]chan struct{}{}, guarded: map[string]int{}}
}

// key is the first path segment: /cold/3 → cold.
func barrierKey(path string) string {
	k, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return k
}

func (b *barrier) ch(key string) chan struct{} {
	c, ok := b.all[key]
	if !ok {
		c = make(chan struct{})
		b.all[key] = c
	}
	return c
}

// ServeHTTP implements http.Handler.
func (b *barrier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := barrierKey(r.URL.Path)
	b.mu.Lock()
	b.arrived[key]++
	c := b.ch(key)
	if b.arrived[key] == b.n {
		close(c)
	}
	b.mu.Unlock()
	tm := time.NewTimer(b.guard)
	defer tm.Stop()
	select {
	case <-c:
		w.WriteHeader(http.StatusOK)
	case <-tm.C:
		b.mu.Lock()
		b.guarded[key]++
		b.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	case <-r.Context().Done():
	}
}

// guardedCount returns how many handlers of key the guard released.
func (b *barrier) guardedCount(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.guarded[key]
}

// newTransport builds the §6.3 default transport for a test and closes its
// idle connections at the end.
func newTransport(tb testing.TB, o Options) *Transport {
	tb.Helper()
	if o.RootCAs == nil {
		o.RootCAs = testsupport.RootCAs(tb)
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 10 * time.Second
	}
	tr, err := NewTransport(o)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(tr.CloseIdleConnections)
	return tr
}

// waitUntil polls cond for up to 5 s.
func waitUntil(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// errClass names the class of a call error for tables.
func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "other"
	}
}

// countClasses counts calls per errClass.
func countClasses(cs []call) map[string]int {
	m := map[string]int{}
	for _, c := range cs {
		m[errClass(c.Err)]++
	}
	return m
}

// firstOther returns the first error of class "other", for the record.
func firstOther(cs []call) string {
	for _, c := range cs {
		if errClass(c.Err) == "other" {
			return c.Err.Error()
		}
	}
	return ""
}
