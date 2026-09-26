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

package engine

import (
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Config is a client's settings once every source has been consulted and
// every value checked. Nothing changes it after [options.resolve] returns,
// so requests read it without locking.
type Config struct {
	// APIKey is the key after trimming, which the redaction of logged
	// headers looks for in their values.
	APIKey string

	// SystemOneURL and modelsURL are the two endpoints, shared read-only by
	// every request.
	SystemOneURL *url.URL
	ModelsURL    *url.URL

	// SystemOneLog and modelsLog are how log records name the endpoints: the
	// URL, or the API path alone under WithLogEndpointHost(false).
	SystemOneLog string
	ModelsLog    string

	// Model is the Model a request names when the call names none.
	Model string

	// Timeout is the deadline of each attempt; zero means none
	// (WithNoTimeout), since a zero WithTimeout is refused.
	Timeout time.Duration

	// ConnectTimeout is the deadline for opening a connection.
	ConnectTimeout time.Duration

	// MaxResponseBytes is the largest response body a request reads.
	MaxResponseBytes int64

	// Logger receives the client's records; it is never nil.
	Logger *slog.Logger

	// SystemOneHeader and modelsHeader are the headers of every POST
	// /v1/systemone and GET /v1/models request, built once. A request clones
	// its template and never writes to it.
	SystemOneHeader http.Header
	ModelsHeader    http.Header

	// Transport carries every request of the client.
	Transport *Transport

	// Retry is the policy of a call that passes none: the root package's
	// RetryPolicy, which this package cannot name, held as an interface value
	// that the root package asserts back (one allocation when the client is
	// built, none per call).
	Retry any
}

// Redactor returns the redactor for the client c configures.
func (c *Config) Redactor() HeaderRedactor { return NewHeaderRedactor(c.APIKey) }

// ConfigRef holds a client's *Config, so that the key is two pointers away
// from the Client (ruling R66): fmt prints a pointer field as an address,
// except under a verb a pointer does not take, such as %s or %q, where it
// prints the value the field points to, which is this struct, whose one
// field is again a pointer, printed as an address. The configuration's
// fields are promoted through it.
type ConfigRef struct {
	*Config
}

// Client is the state of a client, behind the root package's Client, which
// is declared as a defined type over it: the configuration, how errors name
// the endpoints, and the client's counters. Its fields are unexported, so
// that the root package's Client shows none; its methods are this
// package's, which the root package's Client does not inherit.
type Client struct {
	// cfg holds the key and the header templates, two pointers away from
	// the Client (ConfigRef).
	cfg *ConfigRef

	// systemOneEndpoint and modelsEndpoint name the endpoints in errors: the
	// method and the URL.
	systemOneEndpoint string
	modelsEndpoint    string

	closed   atomic.Bool
	attempts atomic.Uint64

	// random is the jitter source of the retry backoff; nil means
	// math/rand/v2's Float64. Tests set it before the first call.
	random func() float64
}

// NewClient returns the state of a client configured by cfg, whose errors
// name its endpoints systemOneEndpoint and modelsEndpoint.
func NewClient(cfg *Config, systemOneEndpoint, modelsEndpoint string) *Client {
	return &Client{cfg: &ConfigRef{cfg}, systemOneEndpoint: systemOneEndpoint, modelsEndpoint: modelsEndpoint}
}

// Built reports whether c was built by NewClient.
func (c *Client) Built() bool { return c != nil && c.cfg != nil && c.cfg.Config != nil }

// Config returns the client's configuration.
func (c *Client) Config() *Config { return c.cfg.Config }

// SystemOneEndpoint returns how errors name the System One endpoint.
func (c *Client) SystemOneEndpoint() string { return c.systemOneEndpoint }

// ModelsEndpoint returns how errors name the list-models endpoint.
func (c *Client) ModelsEndpoint() string { return c.modelsEndpoint }

// Closed is the client's closed flag.
func (c *Client) Closed() *atomic.Bool { return &c.closed }

// Attempts counts the attempts the client sent.
func (c *Client) Attempts() *atomic.Uint64 { return &c.attempts }

// Random returns the jitter source of the retry backoff, nil for
// math/rand/v2's Float64.
func (c *Client) Random() func() float64 { return c.random }

// SetRandom sets the jitter source of the retry backoff; tests set it before
// the first call.
func (c *Client) SetRandom(random func() float64) { c.random = random }

// Prepared is the state of a prepared question set, behind the root
// package's Prepared, which is declared as a defined type over it.
type Prepared struct {
	w wire.Prepared
}

// Wire returns the prepared set's bytes and tables.
func (p *Prepared) Wire() *wire.Prepared { return &p.w }

// Response is the state of a System One response, behind the root package's
// SystemOneResponse, which is declared as a defined type over it: the
// decoded result and the HTTP metadata of the response it came from.
type Response struct {
	res  wire.SystemOneResult
	meta wire.ResponseMeta
}

// Result returns the decoded result.
func (r *Response) Result() *wire.SystemOneResult { return &r.res }

// Meta returns the HTTP metadata: status, header and body.
func (r *Response) Meta() *wire.ResponseMeta { return &r.meta }

// SystemOneAlloc is what a SystemOne call keeps on the heap, in one piece:
// the response it returns and its first attempt's copy of the endpoint URL
// (request.firstURL), which that attempt's request points to. The copy
// lives as long as the response, 144 B that a held response keeps alive,
// and nothing reads it but the transport.
type SystemOneAlloc struct {
	Resp Response
	URL  url.URL
}

// systemOneAllocWith is a systemOneAlloc with the array of the response's
// answer entries, E, in the same allocation.
type systemOneAllocWith[E any] struct {
	SystemOneAlloc
	entries E
}

// MaxInlineAnswers is the largest question set whose answer entries are
// allocated with the call's response.
const MaxInlineAnswers = 4

// NewSystemOneAlloc allocates a call's systemOneAlloc and, for a question
// set of n questions, 1 to maxInlineAnswers, an array of n answer entries
// in the same allocation, which it returns as an empty slice for the
// decode (codec.DecodeSystemOneInto): a response that answers the
// questions asked, the usual case, then costs no allocation for its
// entries. Each size is its own type, so the array is exactly n entries.
// A larger set gets its entries from the decode, in an allocation of their
// own, as does a response with more answers than questions.
//
// Lifetime: the entries share one block with the response and the first
// attempt's URL copy, so an Answers taken from the response keeps the whole
// block reachable (704 B for three questions): the response and the
// decode's array it kept before, and also the 144 B URL copy, which a held
// response keeps alive since W5.3's N1 and which was freed with its request
// before it; an answer value copied out of it holds no pointer into the
// block. No entry points into the body: the decode copies or interns every
// string it stores, whichever array holds the entries
// (TestDecodeDoesNotAliasBody, TestAnswersOutliveTheirResponse).
func NewSystemOneAlloc(n int) (*SystemOneAlloc, []wire.AnswerEntry) {
	switch n {
	case 1:
		a := new(systemOneAllocWith[[1]wire.AnswerEntry])
		return &a.SystemOneAlloc, a.entries[:0]
	case 2:
		a := new(systemOneAllocWith[[2]wire.AnswerEntry])
		return &a.SystemOneAlloc, a.entries[:0]
	case 3:
		a := new(systemOneAllocWith[[3]wire.AnswerEntry])
		return &a.SystemOneAlloc, a.entries[:0]
	case MaxInlineAnswers:
		a := new(systemOneAllocWith[[MaxInlineAnswers]wire.AnswerEntry])
		return &a.SystemOneAlloc, a.entries[:0]
	default:
		return new(SystemOneAlloc), nil
	}
}

// Request is what every attempt of one call sends.
type Request struct {
	Method string
	URL    *url.URL
	// FirstURL is where the first attempt copies url: storage allocated
	// with the call's response, so that the copy costs no allocation of
	// its own. A retry copies url to a fresh URL.
	FirstURL *url.URL
	LogURL   string // how log records name the endpoint
	Endpoint string // how errors name the endpoint
	// Header is the call's Header template, which every attempt's Header
	// starts from, and timeout the deadline of each attempt (zero: none).
	Header  http.Header
	Timeout time.Duration
	// Body and getBody are the encoded Body and its GetBody, set for a
	// request with a Body; getBody is made once per call.
	Body    codec.Body
	GetBody func() (io.ReadCloser, error)
}

// AttemptHeader returns the header map of attempt (ruling R28). The first
// attempt sends the call's template itself: the client's, built once, or
// the call's own when it sets headers. That map is shared by every call and
// never written, and net/http's RoundTripper contract forbids a transport to
// modify a request, so no copy is made. A retry sends a fresh map over the
// template, whose value slices it shares (each has len == cap, so an append
// reallocates), with X-TypeSafe-Retry-Count, which only retries carry
// (py:_core/transport.py:66-68).
func (rq *Request) AttemptHeader(attempt int) http.Header {
	if attempt == 0 {
		return rq.Header
	}
	h := make(http.Header, len(rq.Header)+1)
	maps.Copy(h, rq.Header)
	h[CanonicalRetryCount] = RetryCountValue(attempt)
	return h
}
