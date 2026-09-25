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
	"net/http"
	"net/http/httptrace"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// fanN is the NF4 fan-out width.
const fanN = 64

// guard is the AC-P4 ordering guard.
const guard = 5 * time.Second

// variant is one way of wrapping the transport.
type variant struct {
	name  string
	gated bool
	token bool // the F1 mitigation (iv): header-write token inside the gate
	// firstHold is option (iv-b), the owner's choice at W0.6 (G2): the first
	// request on a new connection keeps the token until its response
	// headers.
	firstHold bool
	// control applies gate+token-first's reworded handler and checks to a
	// variant without firstHold: the negative control showing that the
	// checks can fail. With leadDelay zero nothing is asserted; with it set,
	// every burst must fail check (b).
	control bool
	// leadDelay delays the reworded handler's answer to the first request,
	// so that a waiter written before that answer cannot pass by the timing
	// luck of a loopback round trip.
	leadDelay time.Duration
}

var fanVariants = []variant{
	{name: "gate", gated: true},
	{name: "nogate"},
	{name: "gate+token", gated: true, token: true},
	{name: "gate+token-first", gated: true, token: true, firstHold: true},
	{name: "control:gate+token", gated: true, token: true, control: true},
	{name: "gate+token-first/lead5ms", gated: true, token: true, firstHold: true, leadDelay: 5 * time.Millisecond},
	{name: "control:gate+token/lead5ms", gated: true, token: true, control: true, leadDelay: 5 * time.Millisecond},
}

// wrap builds the round-tripper chain of v over tr.
func wrap(v variant, tr http.RoundTripper, bound time.Duration) *Gate {
	rt := tr
	if v.token {
		wt := NewWriteToken(rt)
		wt.FirstHold = v.firstHold
		rt = wt
	}
	return &Gate{RT: rt, WaitBound: bound, Disabled: !v.gated}
}

// TestST1FanOut measures the cold 64-way fan-out (NF4/AC-P4): connections,
// the ordering assertion (the handler holds every response until 64
// requests arrived, 5 s guard), waiter latency, and a warm second burst.
//
// The gate+token-first variant (option (iv-b)) asserts AC-P4's ordering
// clause as reworded at W0.6 (G2) for its cold burst: the handler answers
// the burst's first request at once and holds every other response until
// the 63 requests after it have arrived (5 s guard); the first request must
// be the leader's, the client must have the leader's response headers
// before any other caller writes its HEADERS, and every request must arrive
// on connection 0. Its warm burst keeps the original clause. The
// control:gate+token variant runs the same handler and checks over the
// plain token (option (iv-a)) without asserting them. The /lead5ms pair
// answers the first request after 5 ms: token-first must still pass every
// burst and the plain token must fail check (b) in every burst. For every
// gated variant the burst's gap from the leader's WroteHeaders to the first
// waiter's WroteHeaders is recorded: the cost of the hold.
func TestST1FanOut(t *testing.T) {
	const reps = 10
	for _, v := range fanVariants {
		reworded := v.firstHold || v.control
		t.Run(v.name, func(t *testing.T) {
			var (
				coldConns, warmNew     []int
				orderingOK             int
				waiterWire, allWire    []time.Duration
				waiterGot, leaderGot   []time.Duration
				spans, warmWire        []time.Duration
				roles                  = map[Role]int{}
				failures, statusNot200 int
				// leadGaps: leader WroteHeaders → first non-leader
				// WroteHeaders; answerGaps (reworded variants): the
				// leader's first response byte → first non-leader
				// WroteHeaders, negative when a waiter wrote first.
				leadGaps, answerGaps        []time.Duration
				leaderFirst, answeredBefore int
			)
			for rep := range reps {
				b := newBarrier(fanN, guard)
				if reworded {
					b.free, b.freeDelay = "cold", v.leadDelay
				}
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: b})
				tr := newTransport(t, Options{})
				g := wrap(v, tr, 20*time.Second)

				var fbMu sync.Mutex
				firstByte := make([]time.Time, fanN)
				cold := fanOut(fanN, func(i int) call {
					ctx := t.Context()
					if reworded {
						ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotFirstResponseByte: func() {
							now := time.Now()
							fbMu.Lock()
							firstByte[i] = now
							fbMu.Unlock()
						}})
					}
					return do(ctx, g, srv.URL()+"/cold/"+strconv.Itoa(i))
				})
				accCold := srv.Accepts()
				warm := fanOut(fanN, func(i int) call {
					return do(t.Context(), g, srv.URL()+"/warm/"+strconv.Itoa(i))
				})
				accWarm := srv.Accepts()
				coldConns = append(coldConns, accCold)
				warmNew = append(warmNew, accWarm-accCold)

				ok := b.guardedCount("cold") == 0 && b.guardedCount("warm") == 0
				for _, r := range srv.Requests() {
					if r.Conn != 0 {
						ok = false
					}
				}
				first, last := cold[0].Start, cold[0].WroteHeaders
				for _, c := range cold {
					roles[c.Role]++
					if c.Err != nil {
						failures++
						ok = false
						t.Errorf("rep %d: cold call: %v", rep, c.Err)
						continue
					}
					if c.Status != http.StatusOK {
						statusNot200++
						ok = false
					}
					if c.Start.Before(first) {
						first = c.Start
					}
					if c.WroteHeaders.After(last) {
						last = c.WroteHeaders
					}
					wire := c.WroteHeaders.Sub(c.Start)
					allWire = append(allWire, wire)
					switch c.Role {
					case RoleWaiter:
						waiterWire = append(waiterWire, wire)
						waiterGot = append(waiterGot, c.GotConn.Sub(c.Start))
					case RoleLeader:
						leaderGot = append(leaderGot, c.GotConn.Sub(c.Start))
					}
				}
				spans = append(spans, last.Sub(first))
				for _, c := range warm {
					if c.Err != nil || c.Status != http.StatusOK {
						failures++
						ok = false
						continue
					}
					warmWire = append(warmWire, c.WroteHeaders.Sub(c.Start))
				}
				leader := -1
				var firstOtherWrite time.Time
				for i, c := range cold {
					switch {
					case c.Err != nil:
					case c.Role == RoleLeader:
						leader = i
					case firstOtherWrite.IsZero() || c.WroteHeaders.Before(firstOtherWrite):
						firstOtherWrite = c.WroteHeaders
					}
				}
				if v.gated && leader >= 0 && !firstOtherWrite.IsZero() {
					leadGaps = append(leadGaps, firstOtherWrite.Sub(cold[leader].WroteHeaders))
				}
				if reworded {
					var answered time.Time
					if leader >= 0 {
						fbMu.Lock()
						answered = firstByte[leader]
						fbMu.Unlock()
					}
					isFirst := leader >= 0 && b.firstFree() == "/cold/"+strconv.Itoa(leader)
					before := !answered.IsZero() && firstOtherWrite.After(answered)
					if isFirst {
						leaderFirst++
					}
					if before {
						answeredBefore++
					}
					if !answered.IsZero() && !firstOtherWrite.IsZero() {
						answerGaps = append(answerGaps, firstOtherWrite.Sub(answered))
					}
					if !isFirst || !before {
						ok = false
						if !v.control {
							t.Errorf("rep %d: first request %q (leader index %d); leader's response headers at %v, first other HEADERS written at %v",
								rep, b.firstFree(), leader, answered, firstOtherWrite)
						}
					}
				}
				if ok {
					orderingOK++
				}
				if accCold != 1 && v.gated {
					t.Errorf("rep %d: cold connections %d, want 1", rep, accCold)
				}
				if accWarm != accCold {
					t.Errorf("rep %d: warm burst opened %d connections, want 0", rep, accWarm-accCold)
				}
			}
			if v.gated && !v.control && orderingOK != reps {
				t.Errorf("ordering %d/%d", orderingOK, reps)
			}
			if v.control && v.leadDelay > 0 && answeredBefore != 0 {
				t.Errorf("plain token: leader answered before any waiter write in %d/%d bursts, want 0", answeredBefore, reps)
			}
			kv := []any{
				"spike", "S-T1", "case", "cold64", "variant", v.name, "reps", reps,
				"cold_conns", fmt.Sprint(coldConns), "warm_new_conns", fmt.Sprint(warmNew),
				"ordering_ok", fmt.Sprintf("%d/%d", orderingOK, reps), "failures", failures, "non200", statusNot200,
				"roles", fmt.Sprint(roles),
				"waiter_wire_p50_ms", ms(pct(waiterWire, 0.5)), "waiter_wire_p99_ms", ms(pct(waiterWire, 0.99)),
				"waiter_gotconn_p50_ms", ms(pct(waiterGot, 0.5)), "waiter_gotconn_p99_ms", ms(pct(waiterGot, 0.99)),
				"all_wire_p50_ms", ms(pct(allWire, 0.5)), "all_wire_p99_ms", ms(pct(allWire, 0.99)),
				"leader_gotconn_p50_ms", ms(pct(leaderGot, 0.5)),
				"span_p50_ms", ms(pct(spans, 0.5)), "span_max_ms", ms(pct(spans, 1)),
				"warm_wire_p50_ms", ms(pct(warmWire, 0.5)), "warm_wire_p99_ms", ms(pct(warmWire, 0.99)),
			}
			if v.gated {
				kv = append(kv, "lead_to_waiter_write_p50_ms", ms(pct(leadGaps, 0.5)), "lead_to_waiter_write_max_ms", ms(pct(leadGaps, 1)))
			}
			if reworded {
				var minAnswer time.Duration
				if len(answerGaps) > 0 {
					minAnswer = slices.Min(answerGaps)
				}
				kv = append(kv, "leader_first", fmt.Sprintf("%d/%d", leaderFirst, reps),
					"leader_answered_before_waiter_writes", fmt.Sprintf("%d/%d", answeredBefore, reps),
					"answer_to_waiter_write_min_ms", ms(minAnswer),
					"answer_to_waiter_write_p50_ms", ms(pct(answerGaps, 0.5)))
			}
			result(kv...)
		})
	}
}

// TestST1FirstHoldBound bounds the FirstHold hold. With no per-call deadline
// (the SDK's WithNoTimeout), the server holds the leader's response for
// twice the bound. With HoldBound set to the gate's wait bound (500 ms
// here), the waiters get the token back HoldBound after the leader's
// HEADERS, are answered on the same connection while the leader is still
// held, and every call succeeds. With no bound they wait for the leader's
// response, and a response that never came would block them until their
// own contexts end (K19).
func TestST1FirstHoldBound(t *testing.T) {
	const bound = 500 * time.Millisecond
	for _, hb := range []time.Duration{bound, 0} {
		name := "unbounded"
		if hb > 0 {
			name = "bound" + strconv.Itoa(int(hb/time.Millisecond)) + "ms"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			held := ""
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				first := held == ""
				if first {
					held = r.URL.Path
				}
				mu.Unlock()
				if first {
					tm := time.NewTimer(2 * bound)
					defer tm.Stop()
					select {
					case <-tm.C:
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(http.StatusOK)
			})})
			tr := newTransport(t, Options{})
			wt := NewWriteToken(tr)
			wt.FirstHold, wt.HoldBound = true, hb
			g := &Gate{RT: wt, WaitBound: bound}
			calls := fanOut(fanN, func(i int) call { return do(t.Context(), g, srv.URL()+"/hold/"+strconv.Itoa(i)) })
			leader, okN, roles := -1, 0, map[Role]int{}
			var start, firstOther, lastOtherDone time.Time
			for i, c := range calls {
				roles[c.Role]++
				if start.IsZero() || c.Start.Before(start) {
					start = c.Start
				}
				if c.Err != nil || c.Status != http.StatusOK {
					continue
				}
				okN++
				if c.Role == RoleLeader {
					leader = i
					continue
				}
				if firstOther.IsZero() || c.WroteHeaders.Before(firstOther) {
					firstOther = c.WroteHeaders
				}
				if c.Done.After(lastOtherDone) {
					lastOtherDone = c.Done
				}
			}
			if leader < 0 || firstOther.IsZero() {
				t.Fatalf("leader %d, roles %v, ok %d", leader, roles, okN)
			}
			l := calls[leader]
			gap := firstOther.Sub(l.WroteHeaders)
			mu.Lock()
			heldIsLeader := held == "/hold/"+strconv.Itoa(leader)
			mu.Unlock()
			result("spike", "S-T1", "case", "firsthold-bound", "variant", name, "hold_bound_ms", ms(hb), "gate_wait_bound_ms", ms(bound),
				"leader_response_held_ms", ms(2*bound), "ok", fmt.Sprintf("%d/%d", okN, fanN), "roles", fmt.Sprint(roles),
				"accepts", srv.Accepts(), "held_is_leader", heldIsLeader,
				"leader_write_to_first_waiter_write_ms", ms(gap), "last_waiter_done_ms", ms(lastOtherDone.Sub(start)),
				"leader_done_ms", ms(l.Done.Sub(start)))
			if okN != fanN || srv.Accepts() != 1 || !heldIsLeader {
				t.Errorf("ok %d/%d, accepts %d, held request is the leader's: %t", okN, fanN, srv.Accepts(), heldIsLeader)
			}
			switch {
			case hb > 0 && (gap < hb-time.Millisecond || !lastOtherDone.Before(l.Done)):
				t.Errorf("bound %v: first waiter HEADERS %v after the leader's, last waiter done %v, leader done %v",
					hb, gap, lastOtherDone.Sub(start), l.Done.Sub(start))
			case hb == 0 && gap < 2*bound:
				t.Errorf("unbounded: first waiter HEADERS %v after the leader's, want at least the %v hold", gap, 2*bound)
			}
		})
	}
}

// holdFirst returns a DialHold that parks dial 0 until the returned
// channel is closed.
func holdFirst() (func(int) <-chan struct{}, chan struct{}) {
	hold := make(chan struct{})
	return func(n int) <-chan struct{} {
		if n == 0 {
			return hold
		}
		return nil
	}, hold
}

// TestST1LeaderCancelled cancels the leader mid-dial with 63 waiters parked:
// one waiter takes over, the detached dial completes, 1 connection.
func TestST1LeaderCancelled(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
	dialHold, hold := holdFirst()
	tr := newTransport(t, Options{DialHold: dialHold})
	g := &Gate{RT: tr, WaitBound: 20 * time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	leaderDone := make(chan call, 1)
	go func() { leaderDone <- do(ctx, g, srv.URL()+"/leader") }()
	waitUntil(t, "the leader to dial", func() bool { return g.State() == Dialing && tr.Dials() == 1 })
	waitersDone := make(chan []call, 1)
	go func() {
		waitersDone <- fanOut(fanN-1, func(i int) call { return do(t.Context(), g, srv.URL()+"/w/"+strconv.Itoa(i)) })
	}()
	waitUntil(t, "63 parked waiters", func() bool { return g.Parked() == fanN-1 })
	cancel()
	leader := <-leaderDone
	// The dial is still parked: the handover must happen before it can
	// complete, so the new leader is a waiter by construction.
	waitUntil(t, "a waiter to take over", func() bool { st := g.Stats(); return st.Leaders == 2 && st.Handovers == 1 })
	dialsBeforeRelease := tr.Dials()
	releaseAt := time.Now()
	close(hold)
	waiters := <-waitersDone
	roles := map[Role]int{}
	okN := 0
	var lastDone time.Time
	for _, c := range waiters {
		roles[c.Role]++
		if c.Err == nil && c.Status == http.StatusOK {
			okN++
		}
		if c.Done.After(lastDone) {
			lastDone = c.Done
		}
	}
	st := g.Stats()
	result("spike", "S-T1", "case", "leader-cancelled", "leader_role", leader.Role, "leader_err", leader.Err,
		"leader_is_canceled", errors.Is(leader.Err, context.Canceled), "waiters_ok", fmt.Sprintf("%d/%d", okN, fanN-1),
		"waiter_roles", fmt.Sprint(roles), "leaders", st.Leaders, "handovers", st.Handovers, "releases", st.Releases,
		"dials_before_release", dialsBeforeRelease, "dials", tr.Dials(), "accepts", srv.Accepts(), "state", g.State(),
		"release_to_last_done_ms", ms(lastDone.Sub(releaseAt)))
	if !errors.Is(leader.Err, context.Canceled) || okN != fanN-1 || srv.Accepts() != 1 || st.Leaders != 2 || st.Handovers != 1 {
		t.Errorf("leader %v, ok %d, accepts %d, stats %+v", leader.Err, okN, srv.Accepts(), st)
	}
}

// TestST1LeaderVanish cancels a lone leader mid-dial: the gate resets to
// cold and the next caller leads.
func TestST1LeaderVanish(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
	dialHold, hold := holdFirst()
	tr := newTransport(t, Options{DialHold: dialHold})
	g := &Gate{RT: tr, WaitBound: 20 * time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan call, 1)
	go func() { done <- do(ctx, g, srv.URL()+"/vanish") }()
	waitUntil(t, "the leader to dial", func() bool { return g.State() == Dialing && tr.Dials() == 1 })
	cancel()
	first := <-done
	stateAfter := g.State()
	nextDone := make(chan call, 1)
	go func() { nextDone <- do(t.Context(), g, srv.URL()+"/next") }()
	waitUntil(t, "the next caller to lead", func() bool { return g.Stats().Leaders == 2 })
	close(hold) // the detached first dial completes only now
	next := <-nextDone
	st := g.Stats()
	result("spike", "S-T1", "case", "leader-vanish", "first_role", first.Role, "first_err", first.Err,
		"state_after_cancel", stateAfter, "next_role", next.Role, "next_status", next.Status, "next_err", next.Err,
		"leaders", st.Leaders, "cold_resets", st.ColdResets, "dials", tr.Dials(), "accepts", srv.Accepts(), "state", g.State())
	if stateAfter != Cold || next.Role != RoleLeader || next.Err != nil || st.Leaders != 2 {
		t.Errorf("state %v, next %v %v, stats %+v", stateAfter, next.Role, next.Err, st)
	}
}

// TestST1TLSSilent points the gate at a listener that never answers TLS:
// the leader's dial fails after connectTimeout (TLSHandshakeTimeout), every
// waiter gets a fresh DialError around the same cause, the gate is cold.
// The no-gate baseline shows the serial re-dials the gate avoids.
func TestST1TLSSilent(t *testing.T) {
	const connectTimeout = 500 * time.Millisecond
	t.Run("gate", func(t *testing.T) {
		l := testsupport.NewSilentListener(t)
		tr := newTransport(t, Options{ConnectTimeout: connectTimeout})
		g := &Gate{RT: tr, WaitBound: 2 * connectTimeout}
		start := time.Now()
		calls := fanOut(fanN, func(i int) call { return do(t.Context(), g, l.URL()+"/"+strconv.Itoa(i)) })
		elapsed := time.Since(start)
		var leader *DialError
		var waiterErrs []*DialError
		roles := map[Role]int{}
		for _, c := range calls {
			roles[c.Role]++
			de, ok := errors.AsType[*DialError](c.Err)
			if !ok {
				t.Errorf("role %v: error %v is not a *DialError", c.Role, c.Err)
				continue
			}
			if c.Role == RoleLeader {
				leader = de
			} else {
				waiterErrs = append(waiterErrs, de)
			}
		}
		if leader == nil {
			t.Fatalf("no leader: roles %v", roles)
		}
		distinct, sameCause := map[*DialError]bool{leader: true}, 0
		for _, w := range waiterErrs {
			distinct[w] = true
			if w.Err == leader.Err && errors.Is(w, leader.Err) { //nolint:errorlint // identity of the shared cause is the assertion
				sameCause++
			}
		}
		result("spike", "S-T1", "case", "tls-silent", "variant", "gate", "connect_timeout_ms", ms(connectTimeout),
			"roles", fmt.Sprint(roles), "accepts", l.Accepts(), "dials", tr.Dials(), "state", g.State(),
			"elapsed_ms", ms(elapsed), "leader_timeout_flag", leader.Timeout, "leader_chain", chain(leader),
			"waiter_errors", len(waiterErrs), "distinct_values", len(distinct), "waiters_same_cause", sameCause,
			"waiter_chain", chain(waiterErrs[0]))
		if l.Accepts() != 1 || g.State() != Cold || len(distinct) != fanN || sameCause != fanN-1 || !leader.Timeout {
			t.Errorf("accepts %d, state %v, distinct %d, same cause %d, timeout %t", l.Accepts(), g.State(), len(distinct), sameCause, leader.Timeout)
		}
	})
	t.Run("nogate", func(t *testing.T) {
		const n = 8
		l := testsupport.NewSilentListener(t)
		tr := newTransport(t, Options{ConnectTimeout: connectTimeout})
		g := &Gate{RT: tr, Disabled: true}
		start := time.Now()
		calls := fanOut(n, func(i int) call {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			return do(ctx, g, l.URL()+"/"+strconv.Itoa(i))
		})
		var doneMs []string
		for _, c := range calls {
			doneMs = append(doneMs, fmt.Sprintf("%.0f", float64(c.Done.Sub(start))/float64(time.Millisecond)))
		}
		result("spike", "S-T1", "case", "tls-silent", "variant", "nogate", "callers", n, "call_deadline_ms", 3000,
			"connect_timeout_ms", ms(connectTimeout), "classes", fmt.Sprint(countClasses(calls)),
			"accepts", l.Accepts(), "dials", tr.Dials(), "done_ms", fmt.Sprint(doneMs), "first_other", firstOther(calls))
	})
}

// TestST1WaiterBound makes the leader's dial slower than the waiter bound:
// every waiter falls through to RoundTrip, and the stock queue still yields
// one connection.
func TestST1WaiterBound(t *testing.T) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
	dialHold, hold := holdFirst()
	tr := newTransport(t, Options{DialHold: dialHold})
	g := &Gate{RT: tr, WaitBound: 200 * time.Millisecond}
	callsDone := make(chan []call, 1)
	go func() {
		callsDone <- fanOut(fanN, func(i int) call { return do(t.Context(), g, srv.URL()+"/"+strconv.Itoa(i)) })
	}()
	waitUntil(t, "63 fall-throughs", func() bool { return g.Stats().FallThroughs == fanN-1 })
	close(hold) // the leader's dial completes only after every waiter fell through
	calls := <-callsDone
	roles, okN := map[Role]int{}, 0
	for _, c := range calls {
		roles[c.Role]++
		if c.Err == nil && c.Status == http.StatusOK {
			okN++
		}
	}
	st := g.Stats()
	result("spike", "S-T1", "case", "waiter-bound", "bound_ms", 200, "dial", "held until 63 fall-throughs", "roles", fmt.Sprint(roles),
		"ok", fmt.Sprintf("%d/%d", okN, fanN), "fall_throughs", st.FallThroughs, "dials", tr.Dials(), "accepts", srv.Accepts(), "state", g.State())
	if okN != fanN || srv.Accepts() != 1 || st.FallThroughs != fanN-1 {
		t.Errorf("ok %d, accepts %d, fall-throughs %d", okN, srv.Accepts(), st.FallThroughs)
	}
}

// TestST1Auto counts the redundant dials of HTTPAuto (Protocols{HTTP1,
// HTTP2}, MaxConnsPerHost 0) with and without the gate (K20).
func TestST1Auto(t *testing.T) {
	const reps = 10
	for _, v := range []variant{{name: "gate", gated: true}, {name: "nogate"}} {
		t.Run(v.name, func(t *testing.T) {
			var accepts, used, dials []int
			failures := 0
			for range reps {
				b := newBarrier(fanN, guard)
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: b})
				tr := newTransport(t, Options{Auto: true})
				g := wrap(v, tr, 20*time.Second)
				calls := fanOut(fanN, func(i int) call { return do(t.Context(), g, srv.URL()+"/cold/"+strconv.Itoa(i)) })
				for _, c := range calls {
					if c.Err != nil || c.Status != http.StatusOK || c.ProtoMajor != 2 {
						failures++
					}
				}
				seen := map[int]bool{}
				for _, r := range srv.Requests() {
					seen[r.Conn] = true
				}
				accepts = append(accepts, srv.Accepts())
				used = append(used, len(seen))
				dials = append(dials, tr.Dials())
			}
			result("spike", "S-T1", "case", "auto-redundant-dials", "variant", v.name, "reps", reps,
				"accepts", fmt.Sprint(accepts), "conns_carrying_requests", fmt.Sprint(used), "dials", fmt.Sprint(dials), "failures", failures)
		})
	}
}
