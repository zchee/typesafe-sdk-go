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
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Options configures [NewTransport], the §6.3 default transport.
type Options struct {
	// ConnectTimeout is the dial timeout and the TLSHandshakeTimeout.
	ConnectTimeout time.Duration
	// RootCAs trusts the test certificate.
	RootCAs *x509.CertPool
	// Auto selects HTTPAuto: Protocols{HTTP1, HTTP2}, MaxConnsPerHost 0, no
	// ALPN refusal.
	Auto bool
	// NonStrict clears HTTP2Config.StrictMaxConcurrentRequests.
	NonStrict bool
	// Resolve maps a dial address to the address actually dialed (the
	// example.com routes); nil dials the address as given.
	Resolve func(addr string) (string, error)
	// DialHold, when set, returns a channel that dial number n (from 0)
	// waits on (respecting the dial context) before it dials; nil does not
	// wait. Tests close the channel to let the dial proceed, so no case
	// depends on a timer margin.
	DialHold func(n int) <-chan struct{}
	// Proxy is the transport's Proxy func.
	Proxy func(*http.Request) (*url.URL, error)
	// APIURL is the API base URL, for the build-time proxy decision.
	APIURL *url.URL
	// NoALPNCheck leaves VerifyConnection unset (the bare stock transport).
	NoALPNCheck bool
	// OnVerify, when set, sees every handshake's ConnectionState.
	OnVerify func(tls.ConnectionState)
	// ForceDecision overrides the build-time ALPN scope, to show what a
	// wrong scope does.
	ForceDecision string
}

// Transport is the §6.3 default transport plus the spike's counters.
type Transport struct {
	*http.Transport
	// Decision is the build-time ALPN scope: "every-handshake", "sni" or
	// "post-check-only".
	Decision string

	mu    sync.Mutex
	dials int
}

// Dials returns the number of TCP dials started.
func (t *Transport) Dials() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dials
}

// IsProxyFromEnvironment reports whether p is http.ProxyFromEnvironment, by
// function identity (func values are not comparable).
func IsProxyFromEnvironment(p func(*http.Request) (*url.URL, error)) bool {
	return p != nil && reflect.ValueOf(p).Pointer() == reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
}

// HostnameInSNI is crypto/tls's hostnameInSNI (handshake_client.go:1305-1318):
// empty for an IP literal, trailing dots stripped.
func HostnameInSNI(name string) string {
	host := name
	if len(host) > 0 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	if i := strings.LastIndex(host, "%"); i > 0 {
		host = host[:i]
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	for len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	return name
}

// Eff is the plan's eff(h) = hostnameInSNI(cmp.Or(cfg.ServerName, h)).
func Eff(cfgServerName, h string) string { return HostnameInSNI(cmp.Or(cfgServerName, h)) }

// ProxyDecision is the build-time decision of §6.3 for the API URL: with
// Proxy nil or ProxyFromEnvironment it is process-constant, so a nil proxy
// URL means every handshake is the API hop; any other Proxy func means a
// proxy may apply and the SNI rule decides per handshake, unless the API
// host's effective SNI is empty or ServerName is overridden, when only the
// ProtoMajor post-check is left.
func ProxyDecision(proxy func(*http.Request) (*url.URL, error), api *url.URL, cfgServerName string) (string, error) {
	mayProxy := proxy != nil
	if proxy == nil || IsProxyFromEnvironment(proxy) {
		mayProxy = false
		if proxy != nil {
			u, err := proxy(&http.Request{URL: api})
			if err != nil {
				return "", err
			}
			mayProxy = u != nil
		}
	}
	switch {
	case !mayProxy:
		return "every-handshake", nil
	case Eff(cfgServerName, api.Hostname()) == "" || cfgServerName != "":
		return "post-check-only", nil
	default:
		return "sni", nil
	}
}

// NewTransport builds the §6.3 default transport.
func NewTransport(o Options) (*Transport, error) {
	t := &Transport{}
	var p http.Protocols
	if o.Auto {
		p.SetHTTP1(true)
	}
	p.SetHTTP2(true)
	dialer := &net.Dialer{Timeout: o.ConnectTimeout}
	tr := &http.Transport{
		Protocols:       &p,
		MaxConnsPerHost: 1,
		HTTP2: &http.HTTP2Config{
			StrictMaxConcurrentRequests: !o.NonStrict,
			SendPingTimeout:             30 * time.Second,
			PingTimeout:                 15 * time.Second,
		},
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: o.ConnectTimeout,
		Proxy:               o.Proxy,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.mu.Lock()
			n := t.dials
			t.dials++
			t.mu.Unlock()
			if o.DialHold != nil {
				if hold := o.DialHold(n); hold != nil {
					select {
					case <-hold:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			if o.Resolve != nil {
				to, err := o.Resolve(addr)
				if err != nil {
					return nil, err
				}
				addr = to
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	if o.Auto {
		tr.MaxConnsPerHost = 0
	}
	t.Transport = tr
	t.Decision = "none"
	if o.Auto || o.NoALPNCheck {
		if o.OnVerify != nil {
			tr.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
				o.OnVerify(cs)
				return nil
			}
		}
		return t, nil
	}
	api := o.APIURL
	if api == nil {
		api = &url.URL{Scheme: "https", Host: "example.com"}
	}
	decision, err := ProxyDecision(o.Proxy, api, tr.TLSClientConfig.ServerName)
	if err != nil {
		return nil, err
	}
	if o.ForceDecision != "" {
		decision = o.ForceDecision
	}
	t.Decision = decision
	want := Eff(tr.TLSClientConfig.ServerName, api.Hostname())
	tr.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if o.OnVerify != nil {
			o.OnVerify(cs)
		}
		apiHop := decision == "every-handshake" || (decision == "sni" && cs.ServerName == want)
		if apiHop && cs.NegotiatedProtocol != "h2" {
			return ErrNotNegotiated
		}
		return nil
	}
	return t, nil
}

// Timing is one call's timeline, from httptrace.
type Timing struct {
	Start, GotConn, WroteHeaders, Done time.Time
	Role                               Role
	Err                                error
	Status                             int
	ProtoMajor                         int
}

// Tracer records GotConn and WroteHeaders times; the hooks may run on the
// transport's goroutines, so reads go through Get.
type Tracer struct {
	mu                    sync.Mutex
	gotConn, wroteHeaders time.Time
}

// Get returns the recorded GotConn and WroteHeaders times.
func (tr *Tracer) Get() (gotConn, wroteHeaders time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.gotConn, tr.wroteHeaders
}

// Traced returns req with a trace that records into a new Tracer.
func Traced(req *http.Request) (*http.Request, *Tracer) {
	tr := &Tracer{}
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			tr.mu.Lock()
			if tr.gotConn.IsZero() {
				tr.gotConn = time.Now()
			}
			tr.mu.Unlock()
		},
		WroteHeaders: func() {
			tr.mu.Lock()
			if tr.wroteHeaders.IsZero() {
				tr.wroteHeaders = time.Now()
			}
			tr.mu.Unlock()
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), tr
}
