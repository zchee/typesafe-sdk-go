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

// Package gzipspike measures what the live API's gzip encoding costs a call
// (ledger W6.4-07, for the owner's DisableCompression question): the
// recorded live bodies served over TLS HTTP/2 to the SDK's own default
// transport, once as the API sends them when the client asks for gzip
// (Content-Encoding: gzip, no declared length after the transport undoes
// it) and once as it sends them when the client does not (identity, with
// Content-Length). The server writes bytes compressed once at start, so
// its own work is the same in both modes and the difference is the
// client's: the transport's gzip reader and the SDK's undeclared-length
// body read.
package gzipspike

import (
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// body is one recorded live body, identity and gzip-compressed.
type body struct {
	plain, gz []byte
}

func load(tb testing.TB, name string) body {
	tb.Helper()
	plain, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "live", name))
	if err != nil {
		tb.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return body{plain: plain, gz: buf.Bytes()}
}

// server answers GET /v1/models with models and POST /v1/systemone with
// answers, gzip-encoded when gzipOn and the request accepts gzip.
func server(tb testing.TB, gzipOn bool, models, answers body, gzipped *atomic.Int64) (*typesafe.Client, func()) {
	tb.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := models
		if r.URL.Path == "/v1/systemone" {
			b = answers
			_, _ = bytes.NewBuffer(nil).ReadFrom(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		if gzipOn && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			gzipped.Add(1)
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(b.gz)))
			_, _ = w.Write(b.gz)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b.plain)))
		_, _ = w.Write(b.plain)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	c, err := typesafe.NewClient(typesafe.WithAPIKey("gzip-spike-key-0000"), typesafe.WithBaseURL(srv.URL), typesafe.WithRootCAs(pool))
	if err != nil {
		tb.Fatal(err)
	}
	return c, func() { _ = c.Close(); srv.Close() }
}

// questions is TestLiveQuestions' question set, which questions.json
// answers.
func questions(tb testing.TB) *typesafe.Prepared {
	tb.Helper()
	qs, err := typesafe.NewQuestions().
		Raw("billing", typesafe.RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Is this ticket about billing?"}}).
		Choice("tone", typesafe.Choice{Options: typesafe.Options{{Label: "calm"}, {Label: "frustrated"}, {Label: "angry"}}}).
		Score("urgency", typesafe.Score{Levels: []typesafe.Content{typesafe.Text("can wait"), typesafe.Text("this week"), typesafe.Text("today")}}).
		Prepare()
	if err != nil {
		tb.Fatal(err)
	}
	return qs
}

func BenchmarkLiveBody(b *testing.B) {
	models, answers := load(b, "models.json"), load(b, "questions.json")
	qs := questions(b)
	state := map[string]any{"subject": "Charged twice this month"}
	for _, endpoint := range []string{"models", "systemone"} {
		for _, gz := range []bool{false, true} {
			name := endpoint + "/identity"
			if gz {
				name = endpoint + "/gzip"
			}
			b.Run(name, func(b *testing.B) {
				var gzipped atomic.Int64
				c, done := server(b, gz, models, answers, &gzipped)
				defer done()
				call := func() {
					var err error
					if endpoint == "models" {
						_, err = c.Models().List(b.Context())
					} else {
						_, err = c.SystemOne(b.Context(), state, qs)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				call() // dial and warm up
				b.ReportAllocs()
				for b.Loop() {
					call()
				}
				if gz != (gzipped.Load() > 0) {
					b.Fatalf("gzip mode %t, gzip responses %d", gz, gzipped.Load())
				}
			})
		}
	}
}
