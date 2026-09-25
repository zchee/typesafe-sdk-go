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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// hops records the ConnectionState of every handshake.
type hops struct {
	mu sync.Mutex
	cs []string
	st []tls.ConnectionState
}

func (h *hops) add(cs tls.ConnectionState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.st = append(h.st, cs)
	h.cs = append(h.cs, fmt.Sprintf("{ServerName:%q NegotiatedProtocol:%q HandshakeComplete:%t Version:%#x}", cs.ServerName, cs.NegotiatedProtocol, cs.HandshakeComplete, cs.Version))
}

func (h *hops) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return fmt.Sprint(h.cs)
}

// answerExample answers 200 "h2 example.com" to an HTTP/2 request for
// example.com and 400 otherwise.
func answerExample(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 || r.Host != "example.com" {
		http.Error(w, "not an HTTP/2 request for example.com", http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, "h2 example.com")
}

// TestST5PlainProxy sends a 16-way cold fan-out for https://example.com
// through a plain HTTP/1.1 CONNECT proxy (the proxy's Routes resolve
// example.com to the loopback server), with and without the gate.
func TestST5PlainProxy(t *testing.T) {
	const n = 16
	for _, v := range []variant{{name: "gate", gated: true}, {name: "nogate"}} {
		t.Run(v.name, func(t *testing.T) {
			srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
			proxy := testsupport.NewProxy(t, testsupport.ProxyPlain, testsupport.Routes{"example.com:443": srv.Addr()})
			var h hops
			tr := newTransport(t, Options{
				Proxy:    http.ProxyURL(proxy.URL()),
				Resolve:  testsupport.Routes{}.Resolve, // the transport dials only the proxy's loopback literal
				OnVerify: h.add,
			})
			g := wrap(v, tr, 20*time.Second+time.Minute+10*time.Second) // + the stock CONNECT limit + a second handshake
			calls := fanOut(n, func(i int) call { return do(t.Context(), g, "https://example.com/"+strconv.Itoa(i)) })
			okN := 0
			for _, c := range calls {
				if c.Err == nil && c.Status == http.StatusOK && c.Body == "h2 example.com" {
					okN++
				}
			}
			result("spike", "S-T5", "case", "plain-proxy-fanout", "variant", v.name, "calls", n, "ok", okN,
				"decision", tr.Decision, "proxy_accepts", proxy.Accepts(), "connects", fmt.Sprintf("%+v", proxy.Connects()),
				"server_accepts", srv.Accepts(), "dials", tr.Dials(), "handshakes", h.String())
			if okN != n {
				t.Errorf("ok %d/%d: %v", okN, n, firstOther(calls))
			}
		})
	}
}

// TestST5TLSProxy runs one request through each TLS proxy mode, with the
// §6.3 build-time scope ("sni") and with a wrong scope that checks every
// handshake.
func TestST5TLSProxy(t *testing.T) {
	modes := map[string]testsupport.ProxyMode{
		"lenient": testsupport.ProxyTLSLenient, "strict": testsupport.ProxyTLSStrict, "offer-h2": testsupport.ProxyTLSOfferH2,
	}
	for name, mode := range modes {
		for _, force := range []string{"", "every-handshake"} {
			t.Run(name+"/"+map[string]string{"": "sni-scope", "every-handshake": "every-handshake"}[force], func(t *testing.T) {
				srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
				proxy := testsupport.NewProxy(t, mode, testsupport.Routes{"example.com:443": srv.Addr()})
				var h hops
				tr := newTransport(t, Options{
					Proxy:         http.ProxyURL(proxy.URL()),
					Resolve:       testsupport.Routes{}.Resolve,
					OnVerify:      h.add,
					ForceDecision: force,
				})
				g := &Gate{RT: tr, WaitBound: time.Minute}
				c := do(t.Context(), g, "https://example.com/")
				var oe *net.OpError
				outer := ""
				if errors.As(c.Err, &oe) {
					outer = fmt.Sprintf("Op=%q Net=%q Source=%v Addr=%v Err=%T", oe.Op, oe.Net, oe.Source, oe.Addr, oe.Err)
				}
				result("spike", "S-T5b", "case", "tls-proxy/"+name, "scope", tr.Decision, "status", c.Status, "body", c.Body,
					"err", c.Err, "chain", chain(c.Err), "outer_op_error", outer, "classify", fmt.Sprintf("%+v", flags(c.Err)),
					"handshakes", h.String(), "proxy_conns", fmt.Sprintf("%+v", proxy.Conns()),
					"connects", fmt.Sprintf("%+v", proxy.Connects()), "server_accepts", srv.Accepts(), "gate_state", g.State())
			})
		}
	}
}

// TestST5ProxyDecision confirms the §6.3 build-time decision logic: the
// ProxyFromEnvironment identity, eff() and the per-hop ServerName values
// from real handshakes.
func TestST5ProxyDecision(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
	pu := &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
	byURL := http.ProxyURL(pu)
	wrapped := func(r *http.Request) (*url.URL, error) { return http.ProxyFromEnvironment(r) }
	clone := (&http.Transport{Proxy: http.ProxyFromEnvironment}).Clone()
	identity := map[string]bool{
		"ProxyFromEnvironment":           IsProxyFromEnvironment(http.ProxyFromEnvironment),
		"ProxyURL":                       IsProxyFromEnvironment(byURL),
		"closure-calling-it":             IsProxyFromEnvironment(wrapped),
		"Transport.Clone().Proxy":        IsProxyFromEnvironment(clone.Proxy),
		"DefaultTransport.Proxy":         IsProxyFromEnvironment(http.DefaultTransport.(*http.Transport).Proxy),
		"nil":                            IsProxyFromEnvironment(nil),
		"method-value-of-a-func-var(pe)": IsProxyFromEnvironment(func() func(*http.Request) (*url.URL, error) { p := http.ProxyFromEnvironment; return p }()),
	}
	result("spike", "S-T5b", "case", "proxy-identity", "identity", fmt.Sprint(identity))

	api := &url.URL{Scheme: "https", Host: "example.com"}
	ipAPI := &url.URL{Scheme: "https", Host: "127.0.0.1:8443"}
	type dcase struct {
		proxy      func(*http.Request) (*url.URL, error)
		api        *url.URL
		serverName string
	}
	decisions := map[string]dcase{
		"nil/example.com":                  {proxy: nil, api: api},
		"ProxyFromEnvironment/example.com": {proxy: http.ProxyFromEnvironment, api: api},
		"ProxyURL/example.com":             {proxy: byURL, api: api},
		"ProxyURL/127.0.0.1":               {proxy: byURL, api: ipAPI},
		"ProxyURL/example.com+ServerName":  {proxy: byURL, api: api, serverName: "example.com"},
		"closure/example.com":              {proxy: wrapped, api: api},
	}
	out := map[string]string{}
	for name, d := range decisions {
		got, err := ProxyDecision(d.proxy, d.api, d.serverName)
		if err != nil {
			got = "error: " + err.Error()
		}
		out[name] = got
	}
	result("spike", "S-T5b", "case", "proxy-decision", "decisions", fmt.Sprint(out),
		"eff", fmt.Sprint(map[string]string{
			"example.com": Eff("", "example.com"), "example.com.": Eff("", "example.com."), "127.0.0.1": Eff("", "127.0.0.1"),
			"[::1]": Eff("", "[::1]"), "override": Eff("api.internal", "127.0.0.1"),
		}))

	// Real ConnectionState values from both hops (lenient TLS proxy).
	srv := testsupport.NewLoopbackServer(t, testsupport.ServerConfig{Handler: http.HandlerFunc(answerExample)})
	proxy := testsupport.NewProxy(t, testsupport.ProxyTLSLenient, testsupport.Routes{"example.com:443": srv.Addr()})
	var h hops
	tr := newTransport(t, Options{Proxy: http.ProxyURL(proxy.URL()), Resolve: testsupport.Routes{}.Resolve, OnVerify: h.add})
	c := do(t.Context(), &Gate{RT: tr, WaitBound: time.Minute}, "https://example.com/")
	h.mu.Lock()
	st := h.st
	h.mu.Unlock()
	var proxyHop, apiHop tls.ConnectionState
	if len(st) == 2 {
		proxyHop, apiHop = st[0], st[1]
	}
	proxyHost, _, _ := net.SplitHostPort(proxy.Addr())
	result("spike", "S-T5b", "case", "hop-servernames", "status", c.Status, "err", c.Err, "handshakes", len(st),
		"proxy_hop_servername", fmt.Sprintf("%q", proxyHop.ServerName), "eff_proxy_host", fmt.Sprintf("%q", Eff("", proxyHost)),
		"api_hop_servername", fmt.Sprintf("%q", apiHop.ServerName), "eff_api_host", fmt.Sprintf("%q", Eff("", "example.com")),
		"api_hop_checked", apiHop.ServerName == Eff("", "example.com"), "proxy_hop_checked", proxyHop.ServerName == Eff("", "example.com"),
		"api_hop_alpn", apiHop.NegotiatedProtocol, "proxy_hop_alpn", fmt.Sprintf("%q", proxyHop.NegotiatedProtocol))
	if len(st) != 2 || apiHop.ServerName != "example.com" || proxyHop.ServerName != "" || c.Err != nil {
		t.Errorf("hops %s, err %v", h.String(), c.Err)
	}
}
