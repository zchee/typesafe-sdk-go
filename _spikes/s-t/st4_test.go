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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestST4ReplayBeforeWrite: GOAWAY with LastStreamID 0 on the first
// connection, so the request was never processed; stdlib replays it on a
// second connection inside one RoundTrip.
func TestST4ReplayBeforeWrite(t *testing.T) {
	for _, kind := range []string{"get", "post-getbody", "post-nogetbody"} {
		t.Run(kind, func(t *testing.T) {
			var bodies []string
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
				OnStream: func(s *testsupport.Stream) testsupport.Action {
					if s.Conn.Index() == 0 {
						return testsupport.ActionGoAway
					}
					return testsupport.ActionServe
				},
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					bodies = append(bodies, string(b))
					w.WriteHeader(http.StatusOK)
				}),
			})
			tr := newTransport(t, Options{})
			g := &Gate{RT: tr, WaitBound: 20 * time.Second}
			var req *http.Request
			switch kind {
			case "get":
				req = mustReq(t, http.MethodGet, srv.URL()+"/r", nil)
			default:
				req = mustReq(t, http.MethodPost, srv.URL()+"/r", []byte(`{"state":"s"}`))
				if kind == "post-nogetbody" {
					req.GetBody = nil
				}
			}
			resp, role, err := g.Do(req)
			status := 0
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				status = resp.StatusCode
			}
			result("spike", "S-T4", "case", "goaway-before-write/"+kind, "roundtrips", 1, "role", role, "status", status,
				"err", err, "chain", chain(err), "classify", fmt.Sprintf("%+v", flags(err)), "accepts", srv.Accepts(),
				"seen_on", fmt.Sprint(seenOn(srv)), "bodies_served", fmt.Sprintf("%q", bodies))
		})
	}
}

// TestST4FailureAfterWrite: the server reads the whole body, then either
// closes the TCP connection or resets the stream.
func TestST4FailureAfterWrite(t *testing.T) {
	for _, kind := range []string{"tcp-close", "rst-internal"} {
		t.Run(kind, func(t *testing.T) {
			var srv *testsupport.LoopbackServer
			srv = testsupport.NewLoopbackServer(t, testsupport.ServerConfig{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.ReadAll(r.Body)
					if r.URL.Path != "/fail" {
						return
					}
					if kind == "tcp-close" {
						srv.CloseConns()
						<-r.Context().Done()
						return
					}
					panic(http.ErrAbortHandler)
				}),
			})
			tr := newTransport(t, Options{})
			g := &Gate{RT: tr, WaitBound: 20 * time.Second}
			if _, _, err := g.Do(mustReq(t, http.MethodGet, srv.URL()+"/warm", nil)); err != nil {
				t.Fatal(err)
			}
			_, role, err := g.Do(mustReq(t, http.MethodPost, srv.URL()+"/fail", []byte(`{"state":"s"}`)))
			var ne net.Error
			isNetErr := errors.As(err, &ne)
			result("spike", "S-T4", "case", "failure-after-write/"+kind, "role", role, "err", err, "chain", chain(err),
				"classify", fmt.Sprintf("%+v", flags(err)), "is_net_error", isNetErr, "accepts", srv.Accepts(),
				"seen_on", fmt.Sprint(seenOn(srv)))
			if err == nil {
				t.Errorf("want an error")
			}
		})
	}
}

// TestST4ALPN: an ALPN-speaking server without h2 (alert 120) and a server
// without ALPN, through the §6.3 transport and through the bare stock
// transport (no VerifyConnection).
func TestST4ALPN(t *testing.T) {
	tests := map[string]struct {
		mode    testsupport.ALPN
		noCheck bool
	}{
		"http1only/check":   {mode: testsupport.ALPNHTTP1Only},
		"http1only/nocheck": {mode: testsupport.ALPNHTTP1Only, noCheck: true},
		"none/check":        {mode: testsupport.ALPNNone},
		"none/nocheck":      {mode: testsupport.ALPNNone, noCheck: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{ALPN: tt.mode})
			tr := newTransport(t, Options{NoALPNCheck: tt.noCheck})
			g := &Gate{RT: tr, WaitBound: 20 * time.Second}
			c := do(t.Context(), g, srv.URL()+"/")
			var oe *net.OpError
			var opErr string
			if errors.As(c.Err, &oe) {
				opErr = fmt.Sprintf("Op=%q Net=%q Err=%T(%v)", oe.Op, oe.Net, oe.Err, oe.Err)
			}
			result("spike", "S-T4", "case", "alpn/"+name, "role", c.Role, "status", c.Status, "proto_major", c.ProtoMajor,
				"err", c.Err, "chain", chain(c.Err), "first_op_error", opErr, "classify", fmt.Sprintf("%+v", flags(c.Err)),
				"is_err_not_negotiated", errors.Is(c.Err, ErrNotNegotiated), "server_conns", fmt.Sprintf("%+v", srv.Conns()),
				"requests_seen", len(srv.Requests()), "gate_state", g.State())
		})
	}
}
