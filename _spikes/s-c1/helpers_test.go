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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	sd1 "github.com/zchee/typesafe-sdk-go/_spikes/s-d1"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// stateSize is the length of the state's JSON encoding: the plan's "1 KB
// state" (NF3), a string boxed in an any before the call, so B = 0.
const stateSize = 1 << 10

// newState returns the 1 KiB boxed-string state.
func newState() any { return strings.Repeat("s", stateSize-2) }

// question is one question of a scenario, before it is prepared.
type question struct {
	name         string
	kind         wire.Kind
	instructions string
	options      []string // choice
	levels       []string // score
}

// scenario is one call shape: the prepared questions, the fixture that
// answers them and the state.
type scenario struct {
	name      string // "q3" or "q20"
	fixture   string
	body      []byte
	questions *wire.Prepared
	answers   int
}

// prepare serialises qs as the "questions" member of a request (the wire
// form of py:_core/question_types.py: type, instructions, criteria) and
// returns the prepared set.
func prepare(tb testing.TB, qs []question) *wire.Prepared {
	tb.Helper()
	quote := func(b []byte, s string) []byte {
		q, err := json.Marshal(s)
		if err != nil {
			tb.Fatal(err)
		}
		return append(b, q...)
	}
	b := []byte{'{'}
	entries := make([]wire.PreparedQuestion, 0, len(qs))
	for i, q := range qs {
		if i > 0 {
			b = append(b, ',')
		}
		b = quote(b, q.name)
		b = append(b, `:{"type":`...)
		b = quote(b, q.kind.String())
		b = append(b, `,"instructions":`...)
		b = quote(b, q.instructions)
		e := wire.PreparedQuestion{Name: q.name, Kind: q.kind}
		switch q.kind {
		case wire.KindChoice:
			b = append(b, `,"criteria":{`...)
			for j, o := range q.options {
				if j > 0 {
					b = append(b, ',')
				}
				b = quote(b, o)
				b = append(b, ":null"...)
			}
			b = append(b, '}')
			e.Options = q.options
		case wire.KindScore:
			b = append(b, `,"criteria":[`...)
			for j, l := range q.levels {
				if j > 0 {
					b = append(b, ',')
				}
				b = quote(b, l)
				e.Levels = append(e.Levels, wire.Content{Text: l})
			}
			b = append(b, ']')
		}
		b = append(b, '}')
		entries = append(entries, e)
	}
	b = append(b, '}')
	p, err := wire.NewPrepared(b, entries)
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

// scenario3 is the plan's NF3 scenario: the three questions of the upstream
// round-trip test (pytest:test_clients.py:60-80), answered by result.json.
func scenario3(tb testing.TB) scenario {
	tb.Helper()
	qs := []question{
		{name: "spam", kind: wire.KindNoul, instructions: "Spam?"},
		{name: "tone", kind: wire.KindChoice, instructions: "Tone?", options: []string{"friendly", "hostile"}},
		{name: "quality", kind: wire.KindScore, instructions: "Quality?", levels: []string{"bad", "ok", "great"}},
	}
	return scenario{name: "q3", fixture: "result.json", body: testsupport.Fixture(tb, "result.json"), questions: prepare(tb, qs), answers: 3}
}

// scenario20 is the 20-question shape, answered by result-20.json; the
// questions follow from the fixture (testdata/README.md): a choice's options
// are the keys of its probabilities, a score's levels its legend values in
// level order.
func scenario20(tb testing.TB) scenario {
	tb.Helper()
	body := testsupport.Fixture(tb, "result-20.json")
	var res sd1.Response
	if err := sd1.NewDecoder().DecodeInto(sd1.VariantA1, body, &res); err != nil {
		tb.Fatal(err)
	}
	var qs []question
	for _, e := range res.Answers.Entries() {
		q := question{name: e.Name, kind: e.Answer.Kind, instructions: "Question about " + e.Name + "?"}
		switch e.Answer.Kind {
		case wire.KindChoice:
			for _, p := range e.Answer.Choice.Probabilities {
				q.options = append(q.options, p.Label)
			}
		case wire.KindScore:
			legend := slices.Clone(e.Answer.Score.Legend)
			slices.SortFunc(legend, func(a, b wire.LegendEntry) int { return int(a.Level) - int(b.Level) })
			for _, l := range legend {
				q.levels = append(q.levels, l.Description.Text)
			}
		}
		qs = append(qs, q)
	}
	if len(qs) != 20 {
		tb.Fatalf("result-20.json has %d answers, want 20", len(qs))
	}
	return scenario{name: "q20", fixture: "result-20.json", body: body, questions: prepare(tb, qs), answers: 20}
}

// scenarios returns both call shapes, in report order.
func scenarios(tb testing.TB) []scenario {
	tb.Helper()
	return []scenario{scenario3(tb), scenario20(tb)}
}

// newRecorder returns a discarding Recorder that answers every request with
// 200, Content-Type application/json and body (Content-Length declared).
func newRecorder(body []byte) *testsupport.Recorder {
	return &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
}

// newClient returns a prototype Client over rt.
func newClient(tb testing.TB, rt http.RoundTripper, cfg Config) *Client {
	tb.Helper()
	c, err := NewClient(rt, cfg)
	if err != nil {
		tb.Fatal(err)
	}
	return c
}

// floorRequest is the floor's request, built before the measured section: the
// prototype's URL and header template over a pre-encoded body.
type floorRequest struct {
	req *http.Request
	rd  *bytes.Reader
	pre []byte
}

// newFloorRequest encodes one body with c (outside any measurement) and
// wraps it in a request whose reader can be rewound.
func newFloorRequest(tb testing.TB, c *Client, state any, qs *wire.Prepared) *floorRequest {
	tb.Helper()
	body, err := c.encode(state, qs)
	if err != nil {
		tb.Fatal(err)
	}
	pre := bytes.Clone(body.Bytes())
	body.Release()
	rd := bytes.NewReader(pre)
	req := &http.Request{
		Method:        http.MethodPost,
		URL:           c.url,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        c.header(0),
		Body:          io.NopCloser(rd),
		ContentLength: int64(len(pre)),
		Host:          c.url.Host,
	}
	return &floorRequest{req: req, rd: rd, pre: pre}
}

// rewind makes the floor request's body readable again.
func (f *floorRequest) rewind() { f.rd.Reset(f.pre) }

// floorCall is the floor: the transport called with a request built
// beforehand, its response drained into io.Discard and closed. No client can
// cost less.
func floorCall(rt http.RoundTripper, req *http.Request) error {
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return err
}

// padded returns a valid response body of exactly size bytes: result.json
// with an unknown member "pad" holding a string that fills the rest. The
// decoder traverses and ignores it.
func padded(tb testing.TB, size int) []byte {
	tb.Helper()
	base := testsupport.FixtureString(tb, "result.json")
	const open = `{"pad":"`
	const closing = `",`
	n := size - len(base) - len(open) - len(closing) + 1 // base's '{' is dropped
	if n < 0 {
		tb.Fatalf("size %d is below the fixture's %d bytes", size, len(base))
	}
	b := make([]byte, 0, size)
	b = append(b, open...)
	b = append(b, bytes.Repeat([]byte{'x'}, n)...)
	b = append(b, closing...)
	b = append(b, base[1:]...)
	if len(b) != size {
		tb.Fatalf("padded body is %d bytes, want %d", len(b), size)
	}
	return b
}
