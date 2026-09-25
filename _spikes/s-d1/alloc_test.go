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

//go:build !race

package sd1

import (
	"strings"
	"testing"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// acceptedFixtures lists the fixtures a variant must accept, with the models
// body left out (a different response type).
func acceptedFixtures(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, n := range testsupport.FixtureNames(t, "*.json") {
		if _, reject := wantReject[n]; reject || n == "models.json" {
			continue
		}
		names = append(names, n)
	}
	return names
}

// TestAllocDecode measures, per variant and fixture, the allocations of one
// warm decode into a fresh Response (the Decoder's scratch is warm, as a
// pooled decoder's is), split into the validation and visitor passes and the
// lazy pass's iteration. Counts are runtime.ReadMemStats deltas, minimum of
// five runs that at least three share (testsupport.MeasureMin), collector
// off, GOMAXPROCS 1. Each line is one ledger row.
func TestAllocDecode(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, v := range Variants {
		for _, name := range acceptedFixtures(t) {
			body := testsupport.Fixture(t, name)
			d := NewDecoder()
			var warm Response
			if err := d.DecodeInto(v, body, &warm); err != nil {
				if _, pinned := knownGateFailures[v][name]; pinned {
					continue
				}
				t.Fatalf("%s %s: %v", v, name, err)
			}
			label := v.String() + " " + name
			total := testsupport.MeasureMin(t, label+" total", func() *Response { return new(Response) }, func(res *Response) {
				if err := d.DecodeInto(v, body, res); err != nil {
					t.Fatal(err)
				}
			})
			st := d.Stats()
			s := codec.NoCopyString(body)
			traverse := testsupport.MeasureMin(t, label+" traverse", func() struct{} { return struct{}{} }, func(struct{}) {
				if _, _, err := d.traverse(v, s, body, modeAnswersBody); err != nil {
					t.Fatal(err)
				}
			})
			lazy := testsupport.Allocs{}
			if st.LazyPasses > 0 {
				src, fromRoot := s, true
				if v == VariantB {
					src, fromRoot = codec.NoCopyString(d.rootMap["answers"]), false
				}
				lazy = testsupport.MeasureMin(t, label+" lazyScan", func() struct{} { return struct{}{} }, func(struct{}) {
					if err := d.lazyScan(src, fromRoot); err != nil {
						t.Fatal(err)
					}
				})
			}
			perLevel := "-"
			if st.LazyPasses > 0 {
				d.perLevelCopy = true
				perLevel = testsupport.MeasureMin(t, label+" per-level copy", func() *Response { return new(Response) }, func(res *Response) {
					if err := d.DecodeInto(v, body, res); err != nil {
						t.Fatal(err)
					}
				}).String()
				d.perLevelCopy = false
			}
			t.Logf("ALLOC %-3s %-34s bytes=%-7d total=%-14s traverse=%-10s lazyScan=%-12s perLevelCopyTotal=%-14s members=%-6d structured=%-6d scans=%d",
				v, strings.TrimSuffix(name, ".json"), len(body), total, traverse, lazy, perLevel, st.Members, st.Structured, st.BodyScans)
		}
	}
}
