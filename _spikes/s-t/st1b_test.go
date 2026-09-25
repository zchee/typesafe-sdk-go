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
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// f1Case is one S-T1b/F1 configuration: a cold burst of calls against a
// server advertising MAX_CONCURRENT_STREAMS limit, through the gate.
type f1Case struct {
	limit     uint32 // 0: the server advertises no limit (client default 1000)
	calls     int
	nonStrict bool
	mitig     string // "", "limiter", "token", "token-first"
	limiterN  int
	service   time.Duration // 0: f1Service
}

func (c f1Case) name() string {
	mode := "strict"
	if c.nonStrict {
		mode = "nonstrict"
	}
	n := fmt.Sprintf("%dvs%d/%s", c.calls, c.limit, mode)
	switch c.mitig {
	case "limiter":
		n += "/limiter" + strconv.Itoa(c.limiterN)
	case "token", "token-first":
		n += "/" + c.mitig
	}
	if c.service != 0 {
		n += "/service" + strconv.Itoa(int(c.service/time.Millisecond)) + "ms"
	}
	return n
}

// f1Service is the handler's service time and f1Deadline the per-call
// deadline (the only thing that ends a strict-mode stall).
const (
	f1Service  = 10 * time.Millisecond
	f1Deadline = 2 * time.Second
)

// TestST1bStreamLimit reproduces F1 on the loopback server and measures the
// mitigations: successes, failures by class, connections, stream high-water
// mark, streams refused by the server, wall time.
func TestST1bStreamLimit(t *testing.T) {
	var cases []f1Case
	for _, lc := range [][2]int{{200, 8}, {64, 4}, {8, 8}, {9, 8}, {4, 4}, {5, 4}} {
		for _, ns := range []bool{false, true} {
			cases = append(cases, f1Case{calls: lc[0], limit: uint32(lc[1]), nonStrict: ns})
		}
	}
	for _, n := range []int{8, 100, 4} {
		cases = append(cases, f1Case{calls: 200, limit: 8, mitig: "limiter", limiterN: n})
	}
	for _, lc := range [][2]int{{200, 8}, {64, 4}, {8, 8}, {9, 8}, {4, 4}, {5, 4}} {
		cases = append(cases, f1Case{calls: lc[0], limit: uint32(lc[1]), mitig: "token"})
		cases = append(cases, f1Case{calls: lc[0], limit: uint32(lc[1]), mitig: "token-first"})
	}
	// The live API advertises 1024 (plan §3.1); 0 advertises nothing.
	for _, lim := range []uint32{1024, 0} {
		for _, ns := range []bool{false, true} {
			cases = append(cases, f1Case{calls: 200, limit: lim, nonStrict: ns})
		}
	}
	for _, lc := range [][2]int{{200, 8}, {64, 4}, {200, 1024}, {200, 0}} {
		cases = append(cases, f1Case{calls: lc[0], limit: uint32(lc[1]), nonStrict: true, mitig: "limiter", limiterN: 100})
	}
	cases = append(cases, f1Case{calls: 200, limit: 8, nonStrict: true, mitig: "limiter", limiterN: 8})
	// Cold-start cost with a slower server: token-first holds every cold
	// waiter behind the leader's whole response.
	for _, m := range []f1Case{{}, {mitig: "token-first"}, {nonStrict: true, mitig: "limiter", limiterN: 100}} {
		m.calls, m.limit, m.service = 64, 0, 50*time.Millisecond
		cases = append(cases, m)
	}
	for _, c := range cases {
		t.Run(c.name(), func(t *testing.T) { runF1(t, c) })
	}
}

// TestST1bFirstHold re-measures option (iv-b), the owner's choice at W0.6
// (G2), for W0.4b: 200 vs limit 8 and 64 vs limit 4 with the token held by
// the first request per new connection until its response headers, and the
// cold-burst cost at 50 ms service time, gate alone against gate+token-first.
// Every case must complete all its calls on one connection.
func TestST1bFirstHold(t *testing.T) {
	const slow = 50 * time.Millisecond
	for _, c := range []f1Case{
		{calls: 200, limit: 8, mitig: "token-first"},
		{calls: 64, limit: 4, mitig: "token-first"},
		{calls: 64, service: slow},
		{calls: 64, mitig: "token-first", service: slow},
	} {
		t.Run(c.name(), func(t *testing.T) {
			ok, accepts := runF1(t, c)
			if ok != c.calls || accepts != 1 {
				t.Errorf("ok %d/%d, accepts %d; want every call on 1 connection", ok, c.calls, accepts)
			}
		})
	}
}

// runF1 runs case c once: a cold burst of c.calls through the gate, each
// call with f1Deadline, and prints its RESULT line. It returns the number
// of calls that succeeded and the connections the server accepted.
func runF1(t *testing.T, c f1Case) (ok, accepts int) {
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
		MaxConcurrentStreams: c.limit,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tm := time.NewTimer(cmp.Or(c.service, f1Service))
			defer tm.Stop()
			select {
			case <-tm.C:
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
			}
		}),
	})
	tr := newTransport(t, Options{NonStrict: c.nonStrict})
	var rt http.RoundTripper = tr
	switch c.mitig {
	case "limiter":
		rt = NewLimiter(rt, c.limiterN)
	case "token":
		rt = NewWriteToken(rt)
	case "token-first":
		wt := NewWriteToken(rt)
		wt.FirstHold = true
		rt = wt
	}
	g := &Gate{RT: rt, WaitBound: 20 * time.Second}
	start := time.Now()
	calls := fanOut(c.calls, func(i int) call {
		ctx, cancel := context.WithTimeout(t.Context(), f1Deadline)
		defer cancel()
		return do(ctx, g, srv.URL()+"/"+strconv.Itoa(i))
	})
	var last time.Time
	var okLat []time.Duration
	for _, cl := range calls {
		if cl.Done.After(last) {
			last = cl.Done
		}
		if cl.Err == nil {
			okLat = append(okLat, cl.Done.Sub(cl.Start))
		}
	}
	perConn := map[int]int{}
	for _, r := range srv.Requests() {
		perConn[r.Conn]++
	}
	var dist []int
	for i := range len(perConn) {
		dist = append(dist, perConn[i])
	}
	slices.Sort(dist)
	classes := countClasses(calls)
	result("spike", "S-T1b", "case", c.name(), "calls", c.calls, "limit", c.limit,
		"service_ms", ms(cmp.Or(c.service, f1Service)), "deadline_ms", ms(f1Deadline),
		"ok", classes["ok"], "deadline", classes["deadline"], "canceled", classes["canceled"], "other", classes["other"],
		"accepts", srv.Accepts(), "dials", tr.Dials(), "requests_seen", len(srv.Requests()), "requests_per_conn", fmt.Sprint(dist),
		"max_active_streams", srv.MaxActiveStreams(), "over_limit", srv.OverLimit(),
		"wall_ms", ms(last.Sub(start)), "ok_p50_ms", ms(pct(okLat, 0.5)), "ok_p99_ms", ms(pct(okLat, 0.99)),
		"first_other", firstOther(calls))
	return classes["ok"], srv.Accepts()
}

// TestST1bStallRate repeats the token mitigations 20 times per case with a
// 500 ms deadline and counts the repetitions in which any call missed it
// (the pre-SETTINGS residual of mitigation (iv)).
func TestST1bStallRate(t *testing.T) {
	const reps = 20
	for _, c := range []f1Case{
		{calls: 64, limit: 4, mitig: "token"},
		{calls: 200, limit: 8, mitig: "token"},
		{calls: 64, limit: 4, mitig: "token-first"},
		{calls: 200, limit: 8, mitig: "token-first"},
		{calls: 64, limit: 4, nonStrict: true, mitig: "limiter", limiterN: 100},
		{calls: 200, limit: 8, nonStrict: true, mitig: "limiter", limiterN: 100},
	} {
		t.Run(c.name(), func(t *testing.T) {
			stalled, maxAccepts, overLimit := 0, 0, 0
			var walls []time.Duration
			for range reps {
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
					MaxConcurrentStreams: c.limit,
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						tm := time.NewTimer(f1Service)
						defer tm.Stop()
						select {
						case <-tm.C:
							w.WriteHeader(http.StatusOK)
						case <-r.Context().Done():
						}
					}),
				})
				tr := newTransport(t, Options{NonStrict: c.nonStrict})
				var rt http.RoundTripper = tr
				switch c.mitig {
				case "limiter":
					rt = NewLimiter(rt, c.limiterN)
				case "token", "token-first":
					wt := NewWriteToken(rt)
					wt.FirstHold = c.mitig == "token-first"
					rt = wt
				}
				g := &Gate{RT: rt, WaitBound: 20 * time.Second}
				start := time.Now()
				calls := fanOut(c.calls, func(i int) call {
					ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
					defer cancel()
					return do(ctx, g, srv.URL()+"/"+strconv.Itoa(i))
				})
				walls = append(walls, time.Since(start))
				if countClasses(calls)["ok"] != c.calls {
					stalled++
				}
				maxAccepts = max(maxAccepts, srv.Accepts())
				overLimit += srv.OverLimit()
				srv.Close()
				tr.CloseIdleConnections()
			}
			result("spike", "S-T1b", "case", "stall-rate/"+c.name(), "reps", reps, "deadline_ms", 500,
				"reps_with_a_missed_deadline", stalled, "max_accepts", maxAccepts, "refused_streams_total", overLimit,
				"wall_p50_ms", ms(pct(walls, 0.5)), "wall_max_ms", ms(pct(walls, 1)))
		})
	}
}
