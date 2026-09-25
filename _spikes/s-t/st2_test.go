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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// configureFake applies the §6.3 transport settings that make sense on the
// fake network to the httptest client transport (its DialContext targets
// the in-memory listener, httptest/server.go:439-444, so NewTransport is not
// used here; W2.2 re-runs this through WithHTTPTransport).
func configureFake(tb testing.TB, rt http.RoundTripper, strict bool) *http.Transport {
	tb.Helper()
	tr, ok := rt.(*http.Transport)
	if !ok {
		tb.Fatalf("client transport is %T", rt)
	}
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	tr.Protocols = &p
	tr.MaxConnsPerHost = 1
	tr.IdleConnTimeout = 90 * time.Second
	tr.HTTP2 = &http.HTTP2Config{StrictMaxConcurrentRequests: strict, SendPingTimeout: 30 * time.Second, PingTimeout: 15 * time.Second}
	return tr
}

// TestST2Synctest runs the gate over the fake-network h2c server inside a
// synctest bubble: cold fan-out, warm burst, the ping timer and the idle
// timer on fake time.
func TestST2Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := testsupport.NewFakeH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		tr := configureFake(t, srv.Client().Transport, true)
		g := &Gate{RT: tr, WaitBound: 20 * time.Second}
		start := time.Now()
		cold := fanOut(fanN, func(i int) call { return do(t.Context(), g, "http://example.com/cold/"+strconv.Itoa(i)) })
		okCold, roles := 0, map[Role]int{}
		for _, c := range cold {
			roles[c.Role]++
			if c.Err == nil && c.Status == http.StatusOK && c.ProtoMajor == 2 {
				okCold++
			}
		}
		accCold := srv.Accepts()
		warm := fanOut(fanN, func(i int) call { return do(t.Context(), g, "http://example.com/warm/"+strconv.Itoa(i)) })
		okWarm := 0
		for _, c := range warm {
			if c.Err == nil && c.Status == http.StatusOK {
				okWarm++
			}
		}
		accWarm := srv.Accepts()
		coldFake := time.Since(start)

		time.Sleep(31 * time.Second) // past SendPingTimeout: the client pings, the server answers
		synctest.Wait()
		accPing := srv.Accepts()
		afterPing := do(t.Context(), g, "http://example.com/after-ping")
		accAfterPing := srv.Accepts()

		time.Sleep(91 * time.Second) // past IdleConnTimeout: the client closes the idle connection
		synctest.Wait()
		afterIdle := do(t.Context(), g, "http://example.com/after-idle")
		accAfterIdle := srv.Accepts()

		result("spike", "S-T2", "case", "fanout+timers", "cold_ok", fmt.Sprintf("%d/%d", okCold, fanN), "roles", fmt.Sprint(roles),
			"accepts_cold", accCold, "warm_ok", fmt.Sprintf("%d/%d", okWarm, fanN), "accepts_warm", accWarm,
			"fake_elapsed_cold_warm", coldFake, "accepts_after_31s", accPing, "after_ping_status", afterPing.Status,
			"accepts_after_ping_call", accAfterPing, "after_idle_status", afterIdle.Status, "after_idle_err", afterIdle.Err,
			"accepts_after_91s_idle", accAfterIdle, "gate_state", g.State(), "gate_stats", fmt.Sprintf("%+v", g.Stats()))
		if accCold != 1 || accWarm != 1 || okCold != fanN || okWarm != fanN || afterIdle.Err != nil || accAfterIdle > 2 {
			t.Errorf("accepts cold %d warm %d idle %d, ok %d/%d, idle err %v", accCold, accWarm, accAfterIdle, okCold, okWarm, afterIdle.Err)
		}
	})
}

// newLimitedFake is a fake-network h2c server advertising limit concurrent
// streams, with a handler that takes service on fake time. The server's
// HTTP2 config must be set before Client() starts the in-memory network.
func newLimitedFake(t *testing.T, limit int, service time.Duration) (*httptest.Server, *atomic.Int64) {
	var accepts atomic.Int64
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(service):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: limit}
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			accepts.Add(1)
		}
	}
	return srv, &accepts
}

// TestST2F1 reproduces F1 on fake time: 16 callers against a limit of 4,
// each with a 2 s deadline. Under synctest the strict stall is a durable
// block (cc.cond.Wait, internal/http2/transport.go:1555), so fake time jumps
// straight to the deadline.
func TestST2F1(t *testing.T) {
	for _, mode := range []string{"strict", "nonstrict", "strict+token", "strict+token-first"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv, accepts := newLimitedFake(t, 4, 10*time.Millisecond)
				tr := configureFake(t, srv.Client().Transport, mode != "nonstrict")
				var rt http.RoundTripper = tr
				if mode == "strict+token" || mode == "strict+token-first" {
					wt := NewWriteToken(rt)
					wt.FirstHold = mode == "strict+token-first"
					rt = wt
				}
				g := &Gate{RT: rt, WaitBound: 20 * time.Second}
				start := time.Now()
				calls := fanOut(16, func(i int) call {
					ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
					defer cancel()
					return do(ctx, g, "http://example.com/"+strconv.Itoa(i))
				})
				elapsed := time.Since(start)
				result("spike", "S-T2", "case", "f1-fake-16vs4", "mode", mode, "classes", fmt.Sprint(countClasses(calls)),
					"accepts", accepts.Load(), "fake_elapsed", elapsed, "first_other", firstOther(calls))
			})
		})
	}
}

// TestST2Refusals records what synctest refuses. Each case fails its test
// by design, so it runs only when ST2_REFUSALS=1; the output is kept under
// results/.
func TestST2Refusals(t *testing.T) {
	if os.Getenv("ST2_REFUSALS") != "1" {
		t.Skip("set ST2_REFUSALS=1 to run the cases synctest refuses")
	}
	for _, pings := range []bool{true, false} {
		t.Run("strict stall without a deadline/pings="+strconv.FormatBool(pings), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv, _ := newLimitedFake(t, 4, 10*time.Millisecond)
				tr := configureFake(t, srv.Client().Transport, true)
				if !pings {
					tr.HTTP2.SendPingTimeout = 0
				}
				g := &Gate{RT: tr, WaitBound: 20 * time.Second}
				calls := fanOut(16, func(i int) call { return do(t.Context(), g, "http://example.com/"+strconv.Itoa(i)) })
				result("spike", "S-T2", "case", "refusal-no-deadline", "pings", pings, "classes", fmt.Sprint(countClasses(calls)))
			})
		})
	}
	t.Run("loopback socket inside a bubble", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{})
			tr := newTransport(t, Options{})
			g := &Gate{RT: tr, WaitBound: 20 * time.Second}
			c := do(t.Context(), g, srv.URL()+"/")
			result("spike", "S-T2", "case", "refusal-real-socket", "status", c.Status, "err", c.Err)
		})
	})
}
