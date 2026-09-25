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

//go:build !race

package sc1

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/bytedance/sonic/encoder"

	sd1 "github.com/zchee/typesafe-sdk-go/_spikes/s-d1"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Sinks keep measured results reachable, as a caller's would be.
var (
	sinkResult  *Result
	sinkRequest *http.Request
	sinkNaive   map[string]any
)

// delta is a signed difference of two allocation counts.
type delta struct{ mallocs, bytes int64 }

func sub(a, b testsupport.Allocs) delta {
	return delta{int64(a.Mallocs) - int64(b.Mallocs), int64(a.Bytes) - int64(b.Bytes)}
}

func add(a, b testsupport.Allocs) testsupport.Allocs {
	return testsupport.Allocs{Mallocs: a.Mallocs + b.Mallocs, Bytes: a.Bytes + b.Bytes}
}

func (d delta) String() string { return fmt.Sprintf("%d/%d", d.mallocs, d.bytes) }

// series measures section testsupport.AllocRuns times, running setup (not
// measured) before each run, and returns the minimum that at least
// testsupport.AllocAgree runs share in both counters.
func series(tb testing.TB, label string, setup, section func()) testsupport.Allocs {
	tb.Helper()
	runs := make([]testsupport.Allocs, testsupport.AllocRuns)
	for i := range runs {
		if setup != nil {
			setup()
		}
		runs[i] = testsupport.Measure(section)
	}
	return testsupport.StableMin(tb, label, runs)
}

// stageAllocs is one call split into the stages of Client.SystemOne.
type stageAllocs struct {
	encode, request, roundTrip, read, decode, finish testsupport.Allocs
}

func (s stageAllocs) sum() testsupport.Allocs {
	return add(add(add(s.encode, s.request), add(s.roundTrip, s.read)), add(s.decode, s.finish))
}

// measureStages runs the call's stages in SystemOne's order, each measured
// on its own, AllocRuns times.
func measureStages(tb testing.TB, ctx context.Context, c *Client, state any, qs *wire.Prepared) stageAllocs {
	tb.Helper()
	var enc, req, rt, rd, dec, fin [testsupport.AllocRuns]testsupport.Allocs
	for i := range testsupport.AllocRuns {
		var (
			body   codec.Body
			r      *http.Request
			cancel context.CancelFunc
			resp   *http.Response
			s      *callScratch
			raw    []byte
			res    *Result
			err    error
		)
		check := func() {
			if err != nil {
				tb.Fatal(err)
			}
		}
		enc[i] = testsupport.Measure(func() { body, err = c.encode(state, qs) })
		check()
		req[i] = testsupport.Measure(func() { r, cancel, err = c.newRequest(ctx, body, 0) })
		check()
		rt[i] = testsupport.Measure(func() { resp, err = c.rt.RoundTrip(r) })
		check()
		rd[i] = testsupport.Measure(func() {
			s = scratchPool.Get().(*callScratch)
			raw, err = c.read(resp, s)
		})
		check()
		dec[i] = testsupport.Measure(func() { res, err = c.decode(resp, raw, s) })
		check()
		// SystemOne's defers, in the order they run.
		fin[i] = testsupport.Measure(func() {
			scratchPool.Put(s)
			err = resp.Body.Close()
			cancel()
			body.Release()
		})
		check()
		sinkResult = res
	}
	return stageAllocs{
		encode:    testsupport.StableMin(tb, "stage encode", enc[:]),
		request:   testsupport.StableMin(tb, "stage request", req[:]),
		roundTrip: testsupport.StableMin(tb, "stage round trip", rt[:]),
		read:      testsupport.StableMin(tb, "stage read", rd[:]),
		decode:    testsupport.StableMin(tb, "stage decode", dec[:]),
		finish:    testsupport.StableMin(tb, "stage finish", fin[:]),
	}
}

// requestItems is the request stage and the decode stage split into the
// single allocations W2.3 has to account for.
type requestItems struct {
	open, getBody, header, withTimeout, request, cancel, result, decodeInto testsupport.Allocs
	// withTimeoutDetached is context.WithTimeout under a parent that cannot
	// be canceled, and done the first Done() on the attempt's context,
	// which the Recorder never calls and a real transport does.
	withTimeoutDetached, done testsupport.Allocs
}

func measureItems(tb testing.TB, ctx context.Context, c *Client, state any, qs *wire.Prepared) requestItems {
	tb.Helper()
	var open, gb, hdr, ctxm, rq, cn, rs, di, det, dn [testsupport.AllocRuns]testsupport.Allocs
	detached := context.WithoutCancel(ctx)
	for i := range testsupport.AllocRuns {
		body, err := c.encode(state, qs)
		if err != nil {
			tb.Fatal(err)
		}
		var (
			rd     *codec.BodyReader
			get    func() (io.ReadCloser, error)
			h      http.Header
			actx   context.Context
			cancel context.CancelFunc
			r      *http.Request
		)
		open[i] = testsupport.Measure(func() { rd, err = body.Open() })
		if err != nil {
			tb.Fatal(err)
		}
		gb[i] = testsupport.Measure(func() { get = getBody(body) })
		hdr[i] = testsupport.Measure(func() { h = c.header(0) })
		ctxm[i] = testsupport.Measure(func() { actx, cancel = context.WithTimeout(ctx, c.timeout) })
		rq[i] = testsupport.Measure(func() { r = c.request(actx, h, rd, get, int64(body.Len())) })
		sinkRequest = r
		resp, err := c.rt.RoundTrip(r)
		if err != nil {
			tb.Fatal(err)
		}
		s := scratchPool.Get().(*callScratch)
		raw, err := c.read(resp, s)
		if err != nil {
			tb.Fatal(err)
		}
		var res *Result
		rs[i] = testsupport.Measure(func() { res = &Result{Status: resp.StatusCode, Header: resp.Header, Body: raw} })
		di[i] = testsupport.Measure(func() { err = s.dec.DecodeInto(sd1.VariantA1, raw, &res.Response) })
		if err != nil {
			tb.Fatal(err)
		}
		sinkResult = res
		scratchPool.Put(s)
		if err := resp.Body.Close(); err != nil {
			tb.Fatal(err)
		}
		cn[i] = testsupport.Measure(func() { cancel() })
		body.Release()
		var dctx context.Context
		var dcancel context.CancelFunc
		det[i] = testsupport.Measure(func() { dctx, dcancel = context.WithTimeout(detached, c.timeout) })
		dn[i] = testsupport.Measure(func() { _ = dctx.Done() })
		dcancel()
	}
	return requestItems{
		open:        testsupport.StableMin(tb, "item body.Open", open[:]),
		getBody:     testsupport.StableMin(tb, "item GetBody closure", gb[:]),
		header:      testsupport.StableMin(tb, "item header map", hdr[:]),
		withTimeout: testsupport.StableMin(tb, "item context.WithTimeout", ctxm[:]),
		request:     testsupport.StableMin(tb, "item Request (WithContext)", rq[:]),
		cancel:      testsupport.StableMin(tb, "item cancel()", cn[:]),
		result:      testsupport.StableMin(tb, "item *Result", rs[:]),
		decodeInto:  testsupport.StableMin(tb, "item DecodeInto", di[:]),

		withTimeoutDetached: testsupport.StableMin(tb, "item WithTimeout, detached parent", det[:]),
		done:                testsupport.StableMin(tb, "item attempt ctx Done()", dn[:]),
	}
}

// TestAllocCall measures one whole call per shape (3 and 20 questions, a
// 1 KiB boxed-string state) with a warm pool, the collector off and
// GOMAXPROCS 1: the floor (the Recorder called with a request built
// beforehand, its response drained into io.Discard, plus E_sonic), the
// prototype's total, SDK-own = total − floor, SDK-own by stage and by item,
// and the naive comparator. Every count is a runtime.ReadMemStats delta,
// the minimum that three of five runs share. The CALL, STAGE and ITEM lines
// are the ledger's rows.
func TestAllocCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	for _, sc := range scenarios(t) {
		rec := newRecorder(sc.body)
		c := newClient(t, rec, Config{})
		naive := NewNaiveClient(rec, sc.questions.Questions)
		state := newState()
		for range 2 { // warm the pools, the encoder and the decoder's scratch
			if _, err := c.SystemOne(ctx, state, sc.questions); err != nil {
				t.Fatal(err)
			}
			if _, err := naive.SystemOne(ctx, state); err != nil {
				t.Fatal(err)
			}
		}
		fr := newFloorRequest(t, c, state, sc.questions)
		if err := floorCall(rec, fr.req); err != nil {
			t.Fatal(err)
		}
		var err error
		check := func(label string) {
			if err != nil {
				t.Fatalf("%s %s: %v", sc.name, label, err)
			}
		}
		empty := series(t, sc.name+" empty section", nil, func() {})
		buf := make([]byte, 0, 4<<10)
		esonic := series(t, sc.name+" E_sonic", nil, func() {
			buf = buf[:0]
			err = encoder.EncodeInto(&buf, state, 0)
		})
		check("E_sonic")
		floorRT := series(t, sc.name+" floor round trip", fr.rewind, func() { err = floorCall(rec, fr.req) })
		check("floor")
		total := series(t, sc.name+" call/sdk", nil, func() { sinkResult, err = c.SystemOne(ctx, state, sc.questions) })
		check("call/sdk")
		naiveTotal := series(t, sc.name+" call/naive", nil, func() { sinkNaive, err = naive.SystemOne(ctx, state) })
		check("call/naive")
		st := measureStages(t, ctx, c, state, sc.questions)
		it := measureItems(t, ctx, c, state, sc.questions)

		floor := add(floorRT, esonic)
		own := sub(total, floor)
		naiveOwn := sub(naiveTotal, floor)
		t.Logf("CALL  %-3s empty=%s E_sonic=%s floorRT=%s floor=%s total=%s own=%s naive=%s naiveOwn=%s own/naiveOwn=%.3f total/naive=%.3f",
			sc.name, empty, esonic, floorRT, floor, total, own, naiveTotal, naiveOwn,
			float64(own.mallocs)/float64(naiveOwn.mallocs), float64(total.Mallocs)/float64(naiveTotal.Mallocs))
		t.Logf("STAGE %-3s encode=%s (own %s) request=%s roundTrip=%s (own %s) read=%s decode=%s finish=%s sum=%s",
			sc.name, st.encode, sub(st.encode, esonic), st.request, st.roundTrip, sub(st.roundTrip, floorRT), st.read, st.decode, st.finish, st.sum())
		t.Logf("ITEM  %-3s body.Open=%s GetBody=%s header=%s WithTimeout=%s Request=%s cancel=%s Result=%s DecodeInto=%s WithTimeoutDetached=%s Done=%s",
			sc.name, it.open, it.getBody, it.header, it.withTimeout, it.request, it.cancel, it.result, it.decodeInto, it.withTimeoutDetached, it.done)

		if empty != (testsupport.Allocs{}) {
			t.Errorf("%s: an empty section allocates %s", sc.name, empty)
		}
		if got := st.sum().Mallocs; got != total.Mallocs {
			t.Errorf("%s: the stages sum to %d mallocs, the whole call makes %d", sc.name, got, total.Mallocs)
		}
		reqItems := it.open.Mallocs + it.getBody.Mallocs + it.header.Mallocs + it.withTimeout.Mallocs + it.request.Mallocs
		if reqItems != st.request.Mallocs {
			t.Errorf("%s: the request items sum to %d mallocs, the request stage makes %d", sc.name, reqItems, st.request.Mallocs)
		}
		if 2*own.mallocs >= naiveOwn.mallocs {
			t.Errorf("%s: SDK-own %d mallocs is not below half the naive comparator's own %d", sc.name, own.mallocs, naiveOwn.mallocs)
		}
	}
}

// memCase is one AC-P5 reply.
type memCase struct {
	name    string
	reply   testsupport.Reply
	outcome string // "ok", "eof" (io.ErrUnexpectedEOF) or "large" (*TooLargeError)
}

// outcomeOf classifies a call's error.
func outcomeOf(err error) string {
	var tl *TooLargeError
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.As(err, &tl):
		return "large"
	default:
		return err.Error()
	}
}

// TestMemStatsCap is the AC-P5 probe: the TotalAlloc delta of one whole call
// and of its body read alone, per reply and per initial-buffer setting.
// Before every measured run the heap is collected (runtime.GC, outside the
// section: the collector is otherwise off and a 16 MiB case allocates about
// 32 MiB) and one small call re-establishes the pools' per-P slots that the
// collection dropped, so the section pays only for its own call.
func TestMemStatsCap(t *testing.T) {
	testsupport.QuietRuntime(t)
	ctx := t.Context()
	const cap16 = DefaultMaxResponseBytes
	over := bytes.Repeat([]byte{' '}, cap16+1)
	exact := padded(t, cap16)
	small := testsupport.Fixture(t, "result.json")
	sc := scenario3(t)
	state := newState()
	cases := []memCase{
		{"i-declared-16MiB-sent-10B", testsupport.Reply{Body: []byte(`{"model":"`), ContentLength: cap16}, "eof"},
		{"ii-declared-16MiB+1", testsupport.Reply{Body: over}, "large"},
		{"iii-undeclared-16MiB+1", testsupport.Reply{Body: over, ContentLength: -1}, "large"},
		{"iv-declared-16MiB", testsupport.Reply{Body: exact}, "ok"},
		{"v-undeclared-16MiB", testsupport.Reply{Body: exact, ContentLength: -1}, "ok"},
		{"vi-declared-result.json", testsupport.Reply{Body: small}, "ok"},
		{"vii-undeclared-result.json", testsupport.Reply{Body: small, ContentLength: -1}, "ok"},
	}
	settings := []struct {
		name string
		cfg  Config
	}{
		{"plan-256K", Config{InitialDeclared: 256 << 10}},
		{"tight-64K", Config{InitialDeclared: 64 << 10}},
		{"split-256K-4K", Config{InitialDeclared: 256 << 10, InitialUndeclared: 4 << 10}},
	}
	warm := newClient(t, newRecorder(small), Config{})
	rewarm := func() {
		runtime.GC()
		if _, err := warm.SystemOne(ctx, state, sc.questions); err != nil {
			t.Fatal(err)
		}
	}
	for _, set := range settings {
		for _, mc := range cases {
			label := set.name + " " + mc.name
			rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{mc.reply}}
			c := newClient(t, rec, set.cfg)
			var err error
			ran := false
			// checkOutcome checks the previous run's outcome, from the next
			// run's setup and after the last run, outside every section.
			checkOutcome := func() {
				if ran {
					if got := outcomeOf(err); got != mc.outcome {
						t.Fatalf("%s: outcome %q, want %q", label, got, mc.outcome)
					}
				}
				ran = true
			}
			call := series(t, label+" call", func() { checkOutcome(); rewarm() }, func() { sinkResult, err = c.SystemOne(ctx, state, sc.questions) })
			checkOutcome()
			ran = false
			var resp *http.Response
			var cleanup func()
			s := scratchPool.Get().(*callScratch)
			read := series(t, label+" read", func() {
				checkOutcome()
				if cleanup != nil {
					cleanup()
				}
				rewarm()
				body, err := c.encode(state, sc.questions)
				if err != nil {
					t.Fatal(err)
				}
				r, cancel, err := c.newRequest(ctx, body, 0)
				if err != nil {
					t.Fatal(err)
				}
				if resp, err = c.rt.RoundTrip(r); err != nil {
					t.Fatal(err)
				}
				cleanup = func() { _ = resp.Body.Close(); cancel(); body.Release() }
			}, func() {
				var raw []byte
				raw, err = c.read(resp, s)
				_ = raw
			})
			checkOutcome()
			cleanup()
			scratchPool.Put(s)
			t.Logf("MEM %-13s %-27s outcome=%-5s call=%-18s read=%-18s callMiB=%.3f", set.name, mc.name, outcomeOf(err), call, read, float64(call.Bytes)/(1<<20))
			switch id, _, _ := strings.Cut(mc.name, "-"); id {
			case "i", "ii":
				if call.Bytes >= 1<<20 {
					t.Errorf("%s: TotalAlloc delta %d is not below 1 MiB (AC-P5)", label, call.Bytes)
				}
			case "iii":
				if call.Bytes > 2*cap16+1<<20 {
					t.Errorf("%s: TotalAlloc delta %d exceeds 2 × cap + 1 MiB (AC-P5)", label, call.Bytes)
				}
			}
		}
	}
}
