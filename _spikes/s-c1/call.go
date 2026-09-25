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

package sc1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/bytedance/sonic/encoder"

	sd1 "github.com/zchee/typesafe-sdk-go/_spikes/s-d1"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

const (
	// DefaultMaxResponseBytes is the NF5 cap on a response body.
	DefaultMaxResponseBytes = 16 << 20
	// DefaultInitialDeclared is the NF5 bound on the first buffer of a body
	// whose length is declared: the buffer is min(Content-Length, this).
	DefaultInitialDeclared = 256 << 10
	// minGrow is the smallest capacity a growth step makes, so a body that
	// declared zero bytes and sent some does not grow one byte at a time.
	minGrow = 512
	// placeholderKey stands in for an API key. It is not a credential.
	placeholderKey = "ts-spike-placeholder-not-a-key"
	// userAgent is the SDK product the prototype sends.
	userAgent = "typesafe-sdk-go/0.0.0-spike"
)

// Canonical header names the prototype sends (plan 1.1.3,
// py:_core/transport.py:116-127).
const (
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	headerAccept        = "Accept"
	headerUserAgent     = "User-Agent"
	headerSDK           = "X-Typesafe-Sdk"
	headerRuntime       = "X-Typesafe-Runtime"
	headerRetryCount    = "X-Typesafe-Retry-Count"
)

// retryCounts holds the value of the retry-count header per attempt, so an
// attempt never formats its number (plan 6.3: "retry-count from a static
// table").
var retryCounts = [...][]string{{"0"}, {"1"}, {"2"}, {"3"}, {"4"}, {"5"}, {"6"}, {"7"}, {"8"}, {"9"}}

// Config configures a [Client]. Zero fields take the defaults.
type Config struct {
	// BaseURL is the API's base URL; the call posts to BaseURL/v1/systemone.
	BaseURL string
	// Model is the default model.
	Model string
	// Timeout is the per-attempt deadline.
	Timeout time.Duration
	// MaxResponseBytes is the NF5 cap.
	MaxResponseBytes int
	// InitialDeclared bounds the first buffer of a declared body.
	InitialDeclared int
	// InitialUndeclared is the first buffer of an undeclared (chunked)
	// body; zero means InitialDeclared, the plan's reading of NF5.
	InitialUndeclared int
}

// headerPair is one entry of the header template.
type headerPair struct {
	key    string
	values []string
}

// Client is the prototype of the SDK client, reduced to one System One call
// on one RoundTripper. It is safe for concurrent use.
type Client struct {
	rt         http.RoundTripper
	url        *url.URL
	model      []byte // the default model as a JSON string, quoted once
	template   []headerPair
	timeout    time.Duration
	maxBytes   int
	initDecl   int
	initUndecl int
}

// NewClient returns a Client that sends its requests through rt.
func NewClient(rt http.RoundTripper, cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.typesafe.ai"
	}
	if cfg.Model == "" {
		cfg.Model = "jev-latest"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if cfg.InitialDeclared == 0 {
		cfg.InitialDeclared = DefaultInitialDeclared
	}
	if cfg.InitialUndeclared == 0 {
		cfg.InitialUndeclared = cfg.InitialDeclared
	}
	u, err := url.Parse(cfg.BaseURL + "/v1/systemone")
	if err != nil {
		return nil, fmt.Errorf("sc1: base URL: %w", err)
	}
	model, err := encoder.Encode(cfg.Model, 0)
	if err != nil {
		return nil, fmt.Errorf("sc1: model: %w", err)
	}
	sdk := []string{userAgent}
	return &Client{
		rt:    rt,
		url:   u,
		model: model,
		// Every value slice has len == cap, so a transport that appends to
		// one reallocates instead of writing into the shared template.
		template: []headerPair{
			{headerAuthorization, []string{"Bearer " + placeholderKey}},
			{headerContentType, []string{"application/json"}},
			{headerAccept, []string{"application/json"}},
			{headerUserAgent, sdk},
			{headerSDK, sdk},
			{headerRuntime, []string{"go/" + runtime.Version()}},
		},
		timeout:    cfg.Timeout,
		maxBytes:   cfg.MaxResponseBytes,
		initDecl:   cfg.InitialDeclared,
		initUndecl: cfg.InitialUndeclared,
	}, nil
}

// Result is what one call returns: the decoded response and its HTTP side.
type Result struct {
	// Response holds model, usage and answers. Its strings may alias Body.
	Response sd1.Response
	// Status is the HTTP status code.
	Status int
	// Header is the response header, shared with the transport's response.
	Header http.Header
	// Body is the body as it was received; it must not be modified.
	Body []byte
}

// Meta returns the HTTP side of the result, as the SDK's Meta() will.
func (r *Result) Meta() wire.ResponseMeta {
	return wire.ResponseMeta{Status: r.Status, Header: r.Header, Body: r.Body}
}

// TooLargeError stands in for the SDK's *ResponseTooLargeError.
type TooLargeError struct {
	// Limit is the cap.
	Limit int
	// Declared is the Content-Length, or -1 when the body declared none.
	Declared int64
	// Read is how many bytes were read before the body was refused; zero
	// when it was refused on its declared length.
	Read int
}

func (e *TooLargeError) Error() string {
	return "sc1: response body over " + strconv.Itoa(e.Limit) + " bytes (declared " +
		strconv.FormatInt(e.Declared, 10) + ", read " + strconv.Itoa(e.Read) + ")"
}

// StatusError stands in for the SDK's *APIError.
type StatusError struct {
	// Status is the HTTP status code.
	Status int
}

func (e *StatusError) Error() string { return "sc1: HTTP status " + strconv.Itoa(e.Status) }

// callScratch is the per-call state that outlives no call: the decoder,
// whose scratch is reused as a pooled production decoder's would be, and a
// one-byte probe the body read uses to tell a full buffer's end of body from
// more data without growing it.
type callScratch struct {
	dec   *sd1.Decoder
	probe [1]byte
}

var scratchPool = sync.Pool{
	New: func() any { return &callScratch{dec: sd1.NewDecoder()} },
}

// SystemOne makes one call: it posts state and the prepared questions and
// returns the decoded response. It makes one attempt; retries are W2.3's.
func (c *Client) SystemOne(ctx context.Context, state any, qs *wire.Prepared) (*Result, error) {
	body, err := c.encode(state, qs)
	if err != nil {
		return nil, err
	}
	defer body.Release()
	req, cancel, err := c.newRequest(ctx, body, 0)
	if err != nil {
		return nil, err
	}
	defer cancel()
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	s := scratchPool.Get().(*callScratch)
	defer scratchPool.Put(s)
	raw, err := c.read(resp, s)
	if err != nil {
		return nil, err
	}
	return c.decode(resp, raw, s)
}

// encode builds the request body into a pooled scratch (plan 6.1.2). The
// returned body holds the call's reference.
func (c *Client) encode(state any, qs *wire.Prepared) (codec.Body, error) {
	body := codec.NewBody()
	buf := body.Buffer()
	*buf = append(*buf, `{"state":`...)
	if err := encoder.EncodeInto(buf, state, 0); err != nil {
		body.Release()
		return codec.Body{}, fmt.Errorf("sc1: state: %w", err)
	}
	*buf = append(*buf, `,"model":`...)
	*buf = append(*buf, c.model...)
	*buf = append(*buf, `,"questions":`...)
	*buf = append(*buf, qs.Questions...)
	*buf = append(*buf, '}')
	return body, nil
}

// newRequest builds attempt's request over body: a reader holding one more
// reference, GetBody for a replay, the header template and the per-attempt
// deadline. The caller runs cancel once the response body has been read.
func (c *Client) newRequest(ctx context.Context, body codec.Body, attempt int) (*http.Request, context.CancelFunc, error) {
	rd, err := body.Open()
	if err != nil {
		return nil, nil, err
	}
	h := c.header(attempt)
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	return c.request(actx, h, rd, getBody(body), int64(body.Len())), cancel, nil
}

// header returns a fresh header map filled from the template: the map is
// the call's, the value slices are shared (plan 6.3).
func (c *Client) header(attempt int) http.Header {
	h := make(http.Header, len(c.template)+1)
	for _, p := range c.template {
		h[p.key] = p.values
	}
	h[headerRetryCount] = retryCounts[min(attempt, len(retryCounts)-1)]
	return h
}

// getBody returns the request's GetBody: a new reader over the same bytes,
// for a replay or a retry.
func getBody(body codec.Body) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		rd, err := body.Open()
		if err != nil {
			return nil, err
		}
		return rd, nil
	}
}

// request assembles the *http.Request. The literal stays on the stack;
// WithContext makes the one heap copy the transport gets.
func (c *Client) request(ctx context.Context, h http.Header, rd io.ReadCloser, gb func() (io.ReadCloser, error), n int64) *http.Request {
	r := http.Request{
		Method:        http.MethodPost,
		URL:           c.url,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          rd,
		GetBody:       gb,
		ContentLength: n,
		Host:          c.url.Host,
	}
	return r.WithContext(ctx)
}

// read reads resp's body under the client's NF5 settings.
func (c *Client) read(resp *http.Response, s *callScratch) ([]byte, error) {
	initial := c.initUndecl
	if resp.ContentLength >= 0 {
		initial = c.initDecl
	}
	return readBody(resp.Body, resp.ContentLength, c.maxBytes, initial, s.probe[:])
}

// readBody reads a response body under the NF5 rules. declared is the
// Content-Length, or -1 when the body declared none.
//
//   - A declared length above limit is refused before any read.
//   - The first buffer is min(declared, initial) for a declared body and
//     initial for an undeclared one.
//   - A full buffer grows by doubling, never past limit, and for a declared
//     body never past the declared length while the body is still within
//     it; a full buffer first reads one byte into probe, so a body that
//     ends exactly at the buffer's capacity never grows it.
//   - The read stops at limit + 1 bytes: the byte after the cap makes the
//     body too large.
func readBody(r io.Reader, declared int64, limit, initial int, probe []byte) ([]byte, error) {
	if declared > int64(limit) {
		return nil, &TooLargeError{Limit: limit, Declared: declared}
	}
	size := initial
	if declared >= 0 {
		size = int(min(declared, int64(initial)))
	}
	buf := make([]byte, 0, size)
	for {
		if len(buf) == cap(buf) {
			n, err := r.Read(probe)
			if n > 0 {
				if len(buf)+n > limit {
					return nil, &TooLargeError{Limit: limit, Declared: declared, Read: len(buf) + n}
				}
				buf = grow(buf, n, declared, limit)
				buf = append(buf, probe[:n]...)
			}
			if err != nil {
				return finishRead(buf, err)
			}
			continue
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			return finishRead(buf, err)
		}
	}
}

// grow returns buf with room for at least need more bytes: double the
// capacity (at least minGrow), capped at limit and, while the body is within
// its declared length, at that length.
func grow(buf []byte, need int, declared int64, limit int) []byte {
	target := min(max(2*cap(buf), minGrow), limit)
	if declared > int64(len(buf)) {
		target = min(target, int(declared))
	}
	target = max(target, len(buf)+need)
	nb := make([]byte, len(buf), target)
	copy(nb, buf)
	return nb
}

// finishRead maps the error that ended a body read.
func finishRead(buf []byte, err error) ([]byte, error) {
	if errors.Is(err, io.EOF) {
		return buf, nil
	}
	return nil, err
}

// decode turns a read body into the call's result.
func (c *Client) decode(resp *http.Response, raw []byte, s *callScratch) (*Result, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &StatusError{Status: resp.StatusCode}
	}
	res := &Result{Status: resp.StatusCode, Header: resp.Header, Body: raw}
	if err := s.dec.DecodeInto(sd1.VariantA1, raw, &res.Response); err != nil {
		return nil, err
	}
	return res, nil
}
