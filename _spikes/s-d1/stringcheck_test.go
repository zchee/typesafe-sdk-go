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
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/bytedance/sonic/ast"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// checkFixtures are the bodies whose string-length mix decides the
// per-string check (W0.2 review input).
var checkFixtures = []string{
	"result.json",
	"result-20.json",
	"escaped-member-names.json",
	"structured-legend-flood-1k.json",
	"structured-legend-flood-10k.json",
}

// collector keeps every key and string value a traversal hands over.
type collector struct {
	noop
	strs []string
}

func (c *collector) OnString(s string) error    { c.strs = append(c.strs, s); return nil }
func (c *collector) OnObjectKey(s string) error { c.strs = append(c.strs, s); return nil }

func collectStrings(tb testing.TB, name string) []string {
	tb.Helper()
	var c collector
	if err := ast.Preorder(testsupport.FixtureString(tb, name), &c, &ast.VisitorOptions{OnlyNumber: true}); err != nil {
		tb.Fatal(err)
	}
	return c.strs
}

// stringChecks are the two per-string checks: codec.ValidString's one loop,
// and utf8.ValidString followed by the word-at-a-time control test.
var stringChecks = []struct {
	name  string
	check func(string) bool
}{
	{"codec", codec.ValidString},
	{"utf8+swar", func(s string) bool { return utf8.ValidString(s) && !HasControlByte(s) }},
}

// TestStringLengthMix logs how many strings each body hands the visitor and
// how long they are, the input of the per-string check decision.
func TestStringLengthMix(t *testing.T) {
	for _, name := range checkFixtures {
		strs := collectStrings(t, name)
		lens := make([]int, len(strs))
		total := 0
		for i, s := range strs {
			lens[i] = len(s)
			total += len(s)
		}
		slices.Sort(lens)
		t.Logf("MIX %-34s strings=%-6d bytes=%-7d mean=%-5.1f p50=%-3d p90=%-3d max=%d",
			name, len(strs), total, float64(total)/float64(len(strs)), lens[len(lens)/2], lens[len(lens)*9/10], lens[len(lens)-1])
		for _, c := range stringChecks {
			for _, s := range strs {
				if !c.check(s) {
					t.Errorf("%s: %s refused %q", name, c.name, s)
				}
			}
		}
	}
}

var sinkBool bool

// BenchmarkStringCheck runs each per-string check over every string one body
// hands the visitor: the check's whole cost for that body.
func BenchmarkStringCheck(b *testing.B) {
	for _, name := range checkFixtures {
		strs := collectStrings(b, name)
		n := 0
		for _, s := range strs {
			n += len(s)
		}
		for _, c := range stringChecks {
			b.Run(shortName(name)+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(n))
				for b.Loop() {
					ok := true
					for _, s := range strs {
						ok = c.check(s) && ok
					}
					sinkBool = ok
				}
			})
		}
	}
}

// BenchmarkDecodeCheck is variant A1's decode with each per-string check.
func BenchmarkDecodeCheck(b *testing.B) {
	for _, c := range []struct {
		name string
		fast bool
	}{{"codec", false}, {"utf8+swar", true}} {
		for _, name := range checkFixtures {
			body := testsupport.Fixture(b, name)
			d := NewDecoder()
			d.v.fastCheck = c.fast
			b.Run(c.name+"/"+shortName(name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					var res Response
					if err := d.DecodeInto(VariantA1, body, &res); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
