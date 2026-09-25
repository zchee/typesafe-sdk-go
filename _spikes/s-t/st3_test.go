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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// holdServer serves paths under /hold/ only once release is closed (or the
// stream ends) and everything else at once.
func holdServer(t *testing.T, onStream func(*testsupport.Stream) testsupport.Action) (*testsupport.LoopbackServer, chan struct{}) {
	release := make(chan struct{})
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
		OnStream: onStream,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.URL.Path) > 6 && r.URL.Path[:6] == "/hold/" {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, "ok "+r.URL.Path)
		}),
	})
	return srv, release
}

// seenOn maps each path to the connections it was seen on, in order.
func seenOn(srv *testsupport.LoopbackServer) map[string][]string {
	m := map[string][]string{}
	for _, r := range srv.Requests() {
		tag := fmt.Sprintf("c%d/s%d/%s", r.Conn, r.StreamID, r.Action)
		if r.Dropped {
			tag += "/dropped"
		}
		m[r.Path] = append(m[r.Path], tag)
	}
	return m
}

// TestST3GoAway sends GOAWAY with LastStreamID below two of four in-flight
// streams on a warm connection.
func TestST3GoAway(t *testing.T) {
	srv, release := holdServer(t, nil)
	tr := newTransport(t, Options{})
	g := &Gate{RT: tr, WaitBound: 20 * time.Second}
	warm := do(t.Context(), g, srv.URL()+"/warm")
	if warm.Err != nil {
		t.Fatal(warm.Err)
	}
	paths := []string{"/hold/a", "/hold/b", "/hold/c", "/hold/d"}
	var wg sync.WaitGroup
	calls := make([]call, len(paths))
	for i, p := range paths {
		wg.Go(func() { calls[i] = do(t.Context(), g, srv.URL()+p) })
	}
	waitUntil(t, "four held streams", func() bool {
		cs := srv.LiveH2Conns()
		return len(cs) == 1 && len(cs[0].ActiveStreams()) == 4
	})
	conn := srv.LiveH2Conns()[0]
	active := conn.ActiveStreams()
	last := active[1] // streams active[2:] are above LastStreamID
	if err := conn.GoAway(last, testsupport.CodeNoError); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the replays on a new connection", func() bool { return len(srv.Requests()) >= 1+4+2 })
	close(release)
	wg.Wait()
	after := do(t.Context(), g, srv.URL()+"/after")
	var outcome []string
	for i, c := range calls {
		outcome = append(outcome, fmt.Sprintf("%s:%d:%v", paths[i], c.Status, c.Err))
	}
	result("spike", "S-T3", "case", "goaway-below-inflight", "active_streams", fmt.Sprint(active), "last_stream_id", last,
		"outcomes", fmt.Sprint(outcome), "seen_on", fmt.Sprint(seenOn(srv)), "after_status", after.Status,
		"accepts", srv.Accepts(), "dials", tr.Dials(), "live_h2_conns_after", len(srv.LiveH2Conns()))
	for _, c := range calls {
		if c.Err != nil || c.Status != http.StatusOK {
			t.Errorf("call %v %d", c.Err, c.Status)
		}
	}
}

// TestST3GoAwayBody repeats the GOAWAY case with POST bodies with and
// without GetBody: a dropped stream whose body cannot be rewound fails.
func TestST3GoAwayBody(t *testing.T) {
	srv, release := holdServer(t, nil)
	tr := newTransport(t, Options{})
	if _, err := tr.RoundTrip(mustReq(t, http.MethodGet, srv.URL()+"/warm", nil)); err != nil {
		t.Fatal(err)
	}
	type res struct {
		status int
		err    error
	}
	kinds := []string{"getbody", "nogetbody"}
	out := make([]res, 2)
	var wg sync.WaitGroup
	for i, kind := range kinds {
		wg.Go(func() {
			req := mustReq(t, http.MethodPost, srv.URL()+"/hold/"+kind, []byte(`{"state":"x"}`))
			if kind == "nogetbody" {
				req.GetBody = nil
			}
			resp, err := tr.RoundTrip(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				out[i] = res{status: resp.StatusCode}
				return
			}
			out[i] = res{err: err}
		})
	}
	waitUntil(t, "two held streams", func() bool {
		cs := srv.LiveH2Conns()
		return len(cs) == 1 && len(cs[0].ActiveStreams()) == 2
	})
	conn := srv.LiveH2Conns()[0]
	if err := conn.GoAway(1, testsupport.CodeNoError); err != nil { // both held streams are above 1
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	for i, kind := range kinds {
		result("spike", "S-T3", "case", "goaway-post-"+kind, "status", out[i].status, "err", out[i].err,
			"chain", chain(out[i].err), "classify", fmt.Sprintf("%+v", flags(out[i].err)))
	}
	result("spike", "S-T3", "case", "goaway-post-seen", "seen_on", fmt.Sprint(seenOn(srv)), "accepts", srv.Accepts())
}

// flags returns Classify's flags without the error itself.
func flags(err error) map[string]bool {
	if err == nil {
		return nil
	}
	d := Classify(err)
	return map[string]bool{"proxy": d.Proxy, "timeout": d.Timeout, "not_negotiated": d.NotNegotiated}
}

// mustReq builds a request with a bytes body (GetBody set) or none.
func mustReq(t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// TestST3RefusedStream refuses one stream on a warm connection: net/http
// retries it on the same connection.
func TestST3RefusedStream(t *testing.T) {
	srv, _ := holdServer(t, func(s *testsupport.Stream) testsupport.Action {
		if s.Seq == 1 {
			return testsupport.ActionRefuse
		}
		return testsupport.ActionServe
	})
	tr := newTransport(t, Options{})
	g := &Gate{RT: tr, WaitBound: 20 * time.Second}
	first := do(t.Context(), g, srv.URL()+"/first")
	second := do(t.Context(), g, srv.URL()+"/refused-once")
	result("spike", "S-T3", "case", "refused-stream", "first_status", first.Status, "second_status", second.Status,
		"second_err", second.Err, "seen_on", fmt.Sprint(seenOn(srv)), "accepts", srv.Accepts(),
		"second_latency_ms", ms(second.Done.Sub(second.Start)))
	if second.Err != nil || srv.Accepts() != 1 {
		t.Errorf("second %v, accepts %d", second.Err, srv.Accepts())
	}
}

// TestST3TCPClose ends the connection under an in-flight request, with a
// clean close (close_notify and FIN) or a TCP reset (SO_LINGER 0).
func TestST3TCPClose(t *testing.T) {
	for _, mode := range []string{"close", "reset"} {
		t.Run(mode, func(t *testing.T) {
			srv, _ := holdServer(t, nil)
			tr := newTransport(t, Options{})
			g := &Gate{RT: tr, WaitBound: 20 * time.Second}
			if c := do(t.Context(), g, srv.URL()+"/warm"); c.Err != nil {
				t.Fatal(c.Err)
			}
			done := make(chan call, 1)
			go func() { done <- do(t.Context(), g, srv.URL()+"/hold/x") }()
			waitUntil(t, "a held stream", func() bool {
				cs := srv.LiveH2Conns()
				return len(cs) == 1 && len(cs[0].ActiveStreams()) == 1
			})
			if mode == "reset" {
				srv.LiveH2Conns()[0].Reset()
			} else {
				srv.CloseConns()
			}
			inflight := <-done
			next := do(t.Context(), g, srv.URL()+"/next")
			result("spike", "S-T3", "case", "conn-"+mode+"-inflight", "inflight_err", inflight.Err, "inflight_chain", chain(inflight.Err),
				"classify", fmt.Sprintf("%+v", flags(inflight.Err)), "is_net_error", isNetError(inflight.Err),
				"is_econnreset", errors.Is(inflight.Err, syscall.ECONNRESET), "is_unexpected_eof", errors.Is(inflight.Err, io.ErrUnexpectedEOF),
				"next_status", next.Status, "next_err", next.Err, "accepts", srv.Accepts(), "seen_on", fmt.Sprint(seenOn(srv)))
		})
	}
}

// isNetError reports whether a net.Error is in err's chain.
func isNetError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne)
}

// portRE matches a loopback address with its port, so outcomes aggregate.
var portRE = regexp.MustCompile(`127\.0\.0\.1:[0-9]+`)

// TestST3IdleCloseRace races a server-side close of an idle connection (as
// at the server's idle timeout) against a new request, 100 times per mode.
func TestST3IdleCloseRace(t *testing.T) {
	const trials = 100
	for _, mode := range []string{"tcp-close", "tcp-reset", "goaway-then-close"} {
		t.Run(mode, func(t *testing.T) {
			outcomes := map[string]int{}
			var reused, fresh int
			for i := range trials {
				srv, _ := holdServer(t, nil)
				tr := newTransport(t, Options{})
				if c := do(t.Context(), &Gate{RT: tr, Disabled: true}, srv.URL()+"/warm"); c.Err != nil {
					t.Fatal(c.Err)
				}
				conn := srv.LiveH2Conns()[0]
				jitter := time.Duration(rand.IntN(200)) * time.Microsecond
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Go(func() {
					<-start
					if i%2 == 0 {
						time.Sleep(jitter)
					}
					switch mode {
					case "goaway-then-close":
						_ = conn.GoAway(1, testsupport.CodeNoError)
						conn.Close()
					case "tcp-reset":
						conn.Reset()
					default:
						conn.Close()
					}
				})
				var c call
				wg.Go(func() {
					<-start
					if i%2 == 1 {
						time.Sleep(jitter)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					c = do(ctx, &Gate{RT: tr, Disabled: true}, srv.URL()+"/race")
				})
				close(start)
				wg.Wait()
				key := "ok"
				if c.Err != nil {
					key = "err: " + portRE.ReplaceAllString(c.Err.Error(), "127.0.0.1:P")
				}
				outcomes[key]++
				for _, r := range srv.Requests() {
					if r.Path == "/race" {
						if r.Conn == 0 {
							reused++
						} else {
							fresh++
						}
					}
				}
				srv.Close()
				tr.CloseIdleConnections()
			}
			keys := make([]string, 0, len(outcomes))
			for k := range outcomes {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			var parts []string
			for _, k := range keys {
				parts = append(parts, strconv.Itoa(outcomes[k])+"×"+k)
			}
			result("spike", "S-T3", "case", "idle-close-race/"+mode, "trials", trials, "outcomes", fmt.Sprint(parts),
				"race_request_seen_on_old_conn", reused, "seen_on_new_conn", fresh)
		})
	}
}
