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
	"errors"
	"io"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	sd1 "github.com/zchee/typesafe-sdk-go/_spikes/s-d1"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// TestSystemOneDecodes checks that the prototype's answers are the S-D1
// decoder's answers for each fixture, and that the naive comparator reads
// the same number of answers.
func TestSystemOneDecodes(t *testing.T) {
	for _, sc := range scenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			rec := newRecorder(sc.body)
			c := newClient(t, rec, Config{})
			res, err := c.SystemOne(t.Context(), newState(), sc.questions)
			if err != nil {
				t.Fatal(err)
			}
			var want sd1.Response
			if err := sd1.NewDecoder().DecodeInto(sd1.VariantA1, sc.body, &want); err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(want.Answers.Entries(), res.Response.Answers.Entries()); diff != "" {
				t.Errorf("answers (-direct decode +call):\n%s", diff)
			}
			if got := res.Response.Answers.Len(); got != sc.answers {
				t.Errorf("answers: got %d, want %d", got, sc.answers)
			}
			if diff := gocmp.Diff(want.Usage, res.Response.Usage); diff != "" {
				t.Errorf("usage (-want +got):\n%s", diff)
			}
			meta := res.Meta()
			if meta.Status != http.StatusOK || !bytes.Equal(meta.Body, sc.body) || meta.Header.Get("Content-Type") != "application/json" {
				t.Errorf("meta: status %d, body %d bytes (want %d), content type %q", meta.Status, len(meta.Body), len(sc.body), meta.Header.Get("Content-Type"))
			}

			out, err := NewNaiveClient(rec, sc.questions.Questions).SystemOne(t.Context(), newState())
			if err != nil {
				t.Fatal(err)
			}
			answers, _ := out["answers"].(map[string]any)
			if len(answers) != sc.answers || out["model"] != "jev-latest" {
				t.Errorf("naive: %d answers, model %v; want %d answers, jev-latest", len(answers), out["model"], sc.answers)
			}
		})
	}
}

// TestRequestShape checks what the prototype puts on the wire: method, URL,
// the header template, ContentLength, GetBody, and a body byte-identical to
// the naive comparator's encoding/json body.
func TestRequestShape(t *testing.T) {
	for _, sc := range scenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			rec := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, sc.body)}}
			naive := &testsupport.Recorder{Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, sc.body)}}
			if _, err := newClient(t, rec, Config{}).SystemOne(t.Context(), newState(), sc.questions); err != nil {
				t.Fatal(err)
			}
			if _, err := NewNaiveClient(naive, sc.questions.Questions).SystemOne(t.Context(), newState()); err != nil {
				t.Fatal(err)
			}
			got, want := rec.Requests()[0], naive.Requests()[0]
			if got.Method != http.MethodPost || got.URL != "https://api.typesafe.ai/v1/systemone" || got.Host != "api.typesafe.ai" {
				t.Errorf("request line: %s %s host %q", got.Method, got.URL, got.Host)
			}
			wantHeader := http.Header{
				"Authorization":          {"Bearer " + placeholderKey},
				"Content-Type":           {"application/json"},
				"Accept":                 {"application/json"},
				"User-Agent":             {userAgent},
				"X-Typesafe-Sdk":         {userAgent},
				"X-Typesafe-Runtime":     {"go/" + runtime.Version()},
				"X-Typesafe-Retry-Count": {"0"},
			}
			if diff := gocmp.Diff(wantHeader, got.Header); diff != "" {
				t.Errorf("header (-want +got):\n%s", diff)
			}
			if diff := gocmp.Diff(want.Header, got.Header); diff != "" {
				t.Errorf("header, naive vs prototype (-naive +prototype):\n%s", diff)
			}
			if !got.HasGetBody || got.ContentLength != int64(len(got.Body)) {
				t.Errorf("GetBody %v, ContentLength %d for a %d-byte body", got.HasGetBody, got.ContentLength, len(got.Body))
			}
			if !bytes.Equal(got.Body, want.Body) {
				t.Errorf("body differs from encoding/json's:\n got %s\nwant %s", got.Body, want.Body)
			}
			var decoded map[string]any
			if err := json.Unmarshal(got.Body, &decoded); err != nil {
				t.Fatal(err)
			}
			if s, _ := decoded["state"].(string); len(s) != stateSize-2 {
				t.Errorf("state: %d bytes, want %d", len(s), stateSize-2)
			}
			if qs, _ := decoded["questions"].(map[string]any); len(qs) != sc.answers {
				t.Errorf("questions: %d, want %d", len(qs), sc.answers)
			}
		})
	}
}

// replayRT is a transport that reads the request body, replays it through
// GetBody as a transport's retry would, and keeps GetBody for a check after
// the call.
type replayRT struct {
	inner    http.RoundTripper
	mu       sync.Mutex
	first    []byte
	replay   []byte
	deadline time.Duration
	getBody  func() (io.ReadCloser, error)
}

func (r *replayRT) RoundTrip(req *http.Request) (*http.Response, error) {
	first, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	rd, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	replay, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	dl, ok := req.Context().Deadline()
	r.mu.Lock()
	r.first, r.replay, r.getBody = first, replay, req.GetBody
	if ok {
		r.deadline = time.Until(dl)
	}
	r.mu.Unlock()
	req2 := req.Clone(req.Context())
	req2.Body = rd
	return r.inner.RoundTrip(req2)
}

// TestGetBodyReplay checks that GetBody yields the same bytes as the first
// reader, that the attempt carries its deadline, and that a GetBody kept
// past the call cannot open the released body (plan 6.1.3).
func TestGetBodyReplay(t *testing.T) {
	sc := scenario3(t)
	rt := &replayRT{inner: newRecorder(sc.body)}
	c := newClient(t, rt, Config{Timeout: 3 * time.Second})
	if _, err := c.SystemOne(t.Context(), newState(), sc.questions); err != nil {
		t.Fatal(err)
	}
	if len(rt.first) == 0 || !bytes.Equal(rt.first, rt.replay) {
		t.Errorf("replay differs: first %d bytes, replay %d bytes", len(rt.first), len(rt.replay))
	}
	if rt.deadline <= 0 || rt.deadline > 3*time.Second {
		t.Errorf("per-attempt deadline: %v left, want (0, 3s]", rt.deadline)
	}
	if _, err := rt.getBody(); !errors.Is(err, codec.ErrBodyReleased) {
		t.Errorf("GetBody after the call: %v, want %v", err, codec.ErrBodyReleased)
	}
}

// countingReader serves data in reads of at most chunk bytes, counting the
// reads and the bytes it hands out; short makes it end with
// io.ErrUnexpectedEOF, as a connection that closes early.
type countingReader struct {
	data  []byte
	off   int
	chunk int
	short bool
	reads int
	bytes int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	if r.off >= len(r.data) {
		if r.short {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, io.EOF
	}
	if r.chunk > 0 && len(p) > r.chunk {
		p = p[:r.chunk]
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	r.bytes += n
	return n, nil
}

// TestReadBody checks the NF5 rules of readBody with a 4 KiB cap and a
// 1 KiB initial buffer.
func TestReadBody(t *testing.T) {
	const limit, initial = 4 << 10, 1 << 10
	tests := map[string]struct {
		size     int   // bytes the body holds
		declared int64 // Content-Length, -1 for none
		short    bool
		chunk    int
		wantErr  error // nil, io.ErrUnexpectedEOF or a *TooLargeError
		wantLen  int
		wantCap  int // capacity of the returned buffer, 0 to skip
		maxBytes int // most bytes the reader may hand out, 0 to skip
	}{
		"success: declared body fits its exact buffer":                        {size: 364, declared: 364, wantLen: 364, wantCap: 364},
		"success: declared body above the initial buffer grows to its length": {size: 3000, declared: 3000, wantLen: 3000, wantCap: 3000},
		"success: declared body exactly at the cap":                           {size: limit, declared: limit, wantLen: limit, wantCap: limit},
		"success: undeclared body exactly at the cap":                         {size: limit, declared: -1, wantLen: limit, wantCap: limit},
		"success: undeclared body in small chunks":                            {size: 2500, declared: -1, chunk: 7, wantLen: 2500, wantCap: 4096},
		"success: declared zero, empty body":                                  {size: 0, declared: 0, wantLen: 0, wantCap: 0},
		"success: undeclared empty body":                                      {size: 0, declared: -1, wantLen: 0},
		"success: body longer than declared is read on":                       {size: 20, declared: 10, wantLen: 20},
		"error: declared length over the cap is refused before any read":      {size: limit + 1, declared: limit + 1, wantErr: &TooLargeError{Limit: limit, Declared: limit + 1}, maxBytes: -1},
		"error: undeclared body over the cap stops at cap + 1":                {size: 3 * limit, declared: -1, wantErr: &TooLargeError{Limit: limit, Declared: -1, Read: limit + 1}, maxBytes: limit + 1},
		"error: declared at the cap but longer stops at cap + 1":              {size: 2 * limit, declared: limit, wantErr: &TooLargeError{Limit: limit, Declared: limit, Read: limit + 1}, maxBytes: limit + 1},
		"error: declared longer than sent ends early":                         {size: 10, declared: 2 << 10, short: true, wantErr: io.ErrUnexpectedEOF},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := &countingReader{data: bytes.Repeat([]byte{'x'}, tt.size), chunk: tt.chunk, short: tt.short}
			var probe [1]byte
			got, err := readBody(r, tt.declared, limit, initial, probe[:])
			switch want := tt.wantErr.(type) {
			case nil:
				if err != nil {
					t.Fatalf("readBody: %v", err)
				}
			case *TooLargeError:
				var tl *TooLargeError
				if !errors.As(err, &tl) {
					t.Fatalf("readBody: %v, want %v", err, want)
				}
				if diff := gocmp.Diff(want, tl); diff != "" {
					t.Errorf("TooLargeError (-want +got):\n%s", diff)
				}
			default:
				if !errors.Is(err, want) {
					t.Fatalf("readBody: %v, want %v", err, want)
				}
			}
			switch {
			case tt.maxBytes < 0 && (r.reads != 0 || r.bytes != 0):
				t.Errorf("refused body was read: %d reads, %d bytes", r.reads, r.bytes)
			case tt.maxBytes > 0 && r.bytes > tt.maxBytes:
				t.Errorf("read %d bytes, want at most %d", r.bytes, tt.maxBytes)
			}
			if tt.wantErr != nil {
				return
			}
			if len(got) != tt.wantLen {
				t.Errorf("len %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantCap > 0 && cap(got) != tt.wantCap {
				t.Errorf("cap %d, want %d", cap(got), tt.wantCap)
			}
		})
	}
}

// TestSystemOneCap checks the AC-P5 outcomes through the whole call at the
// real 16 MiB cap.
func TestSystemOneCap(t *testing.T) {
	const cap16 = DefaultMaxResponseBytes
	over := bytes.Repeat([]byte{' '}, cap16+1)
	tests := map[string]struct {
		reply   testsupport.Reply
		wantErr error
	}{
		"error: declared 16 MiB with a 10-byte body ends early": {
			reply:   testsupport.Reply{Body: []byte(`{"model":"`), ContentLength: cap16},
			wantErr: io.ErrUnexpectedEOF,
		},
		"error: declared 16 MiB + 1 is refused before reading": {
			reply:   testsupport.Reply{Body: over},
			wantErr: &TooLargeError{Limit: cap16, Declared: cap16 + 1},
		},
		"error: undeclared 16 MiB + 1 stops at cap + 1": {
			reply:   testsupport.Reply{Body: over, ContentLength: -1},
			wantErr: &TooLargeError{Limit: cap16, Declared: -1, Read: cap16 + 1},
		},
		"success: declared 16 MiB exactly": {
			reply: testsupport.Reply{Body: padded(t, cap16)},
		},
		"success: undeclared 16 MiB exactly": {
			reply: testsupport.Reply{Body: padded(t, cap16), ContentLength: -1},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{tt.reply}}
			res, err := newClient(t, rec, Config{}).SystemOne(t.Context(), newState(), scenario3(t).questions)
			switch want := tt.wantErr.(type) {
			case nil:
				if err != nil {
					t.Fatal(err)
				}
				if res.Response.Answers.Len() != 3 || len(res.Body) != cap16 {
					t.Errorf("%d answers, %d-byte body", res.Response.Answers.Len(), len(res.Body))
				}
			case *TooLargeError:
				var tl *TooLargeError
				if !errors.As(err, &tl) {
					t.Fatalf("SystemOne: %v, want %v", err, want)
				}
				if diff := gocmp.Diff(want, tl); diff != "" {
					t.Errorf("TooLargeError (-want +got):\n%s", diff)
				}
			default:
				if !errors.Is(err, want) {
					t.Fatalf("SystemOne: %v, want %v", err, want)
				}
			}
		})
	}
}
