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

package sd1

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// benchFixtures are the valid bodies every decode benchmark runs; the
// primitive and scan benchmarks run the first five.
var benchFixtures = []string{
	"result.json",
	"result-20.json",
	"escaped-names.json",
	"structured-legend-flood-1k.json",
	"structured-legend-flood-10k.json",
	"type-last.json",
	"escaped-member-names.json",
	"structured-legend.json",
	"deviation-lone-surrogate.json",
	"score-flood-mini.json",
	"unknown-answer-type.json",
	"no-answers.json",
	"parity-big-exp-unknown.json",
}

func shortName(name string) string { return strings.TrimSuffix(name, ".json") }

// BenchmarkDecode measures one warm decode per variant and fixture: the
// Decoder's scratch is reused, the Response is fresh each iteration.
func BenchmarkDecode(b *testing.B) {
	for _, v := range Variants {
		for _, name := range benchFixtures {
			body := testsupport.Fixture(b, name)
			d := NewDecoder()
			var res Response
			if err := d.DecodeInto(v, body, &res); err != nil {
				continue // variant B refuses parity-big-exp-unknown (gate failure)
			}
			b.Run(v.String()+"/"+shortName(name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					res = Response{}
					if err := d.DecodeInto(v, body, &res); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// noop is an ast.Visitor that does nothing: the traversal's own cost.
type noop struct{}

func (noop) OnNull() error                        { return nil }
func (noop) OnBool(bool) error                    { return nil }
func (noop) OnString(string) error                { return nil }
func (noop) OnInt64(int64, json.Number) error     { return nil }
func (noop) OnFloat64(float64, json.Number) error { return nil }
func (noop) OnObjectBegin(int) error              { return nil }
func (noop) OnObjectKey(string) error             { return nil }
func (noop) OnObjectEnd() error                   { return nil }
func (noop) OnArrayBegin(int) error               { return nil }
func (noop) OnArrayEnd() error                    { return nil }

// BenchmarkPrimitive measures the sonic passes the variants are built from,
// so a variant's time can be read as the sum of its passes.
func BenchmarkPrimitive(b *testing.B) {
	opts := &ast.VisitorOptions{OnlyNumber: true}
	for _, name := range benchFixtures[:5] {
		body := testsupport.Fixture(b, name)
		s := codec.NoCopyString(body)
		prims := []struct {
			name string
			run  func() bool
		}{
			{"preorder-noop", func() bool { return ast.Preorder(s, noop{}, opts) == nil }},
			{"skip", func() bool { start, _ := decoder.Skip(body); return start >= 0 }},
			{"validstring", func() bool { return sonic.ValidString(s) }},
			{"unmarshal-rawmap", func() bool {
				var m map[string]sonic.NoCopyRawMessage
				return sonic.UnmarshalString(s, &m) == nil
			}},
		}
		for _, p := range prims {
			b.Run(shortName(name)+"/"+p.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					if !p.run() {
						b.Fatal("refused")
					}
				}
			})
		}
	}
}

// BenchmarkControlScan measures the R14 candidates over whole bodies: the
// SWAR test for any byte below 0x20, the string-tracking scan, the two
// chained (the unconditional rule), and utf8.ValidString for comparison.
func BenchmarkControlScan(b *testing.B) {
	for _, name := range benchFixtures[:5] {
		s := testsupport.FixtureString(b, name)
		scans := []struct {
			name string
			run  func() bool
		}{
			{"swar", func() bool { return HasControlByte(s) }},
			{"tracked", func() bool { return controlInString(s) }},
			{"rule", func() bool { return RawControlInString(s) }},
			{"utf8", func() bool { return !utf8.ValidString(s) }},
		}
		for _, sc := range scans {
			b.Run(shortName(name)+"/"+sc.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(s)))
				for b.Loop() {
					if sc.run() {
						b.Fatal("found a control byte or invalid UTF-8")
					}
				}
			})
		}
	}
}
