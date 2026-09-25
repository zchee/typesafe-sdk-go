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

package se1

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic/encoder"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// emptyPool drops every pooled scratch: sync.Pool keeps a victim cache for
// one collection, so two collections empty it. The next PooledCall is a
// fresh client's first call.
func emptyPool() {
	runtime.GC()
	runtime.GC()
}

// TestAllocEncode measures, per kind and size, the allocations of one pooled
// encode with a warm pool: the pool is emptied, one call warms it, then
// measureRuns takes five runtime.ReadMemStats deltas (collector off,
// GOMAXPROCS 1) and stable returns the malloc minimum at least three share.
// A scratch that outgrows the 8 MiB ceiling is dropped after every call, so
// its row is the steady cost of a pool miss plus growth. Each line is one
// ledger row.
func TestAllocEncode(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, kind := range Kinds {
		for _, size := range Sizes {
			call := NewCall(kind, Value(kind, size, EncodeMap))
			emptyPool()
			n, warmCap, err := PooledCall(call)
			if err != nil {
				t.Fatal(err)
			}
			var capAfter int
			runs := measureRuns(func() struct{} { return struct{}{} }, func(struct{}) {
				if _, capAfter, err = PooledCall(call); err != nil {
					t.Fatal(err)
				}
			})
			got := stable(t, string(kind)+" "+SizeName(size), runs)
			t.Logf("ENC %-14s %-6s encoded=%-8d allocs/bytes=%-24s warmCap=%-9d capAfter=%-9d pooled=%v",
				kind, SizeName(size), n, got, warmCap, capAfter, capAfter <= codec.ScratchCeiling)
		}
	}
}

// measureRuns takes testsupport.AllocRuns runtime.ReadMemStats deltas of
// body, each on a fresh input() made outside the measured section.
func measureRuns[T any](input func() T, body func(T)) []testsupport.Allocs {
	runs := make([]testsupport.Allocs, testsupport.AllocRuns)
	for i := range runs {
		in := input()
		runs[i] = testsupport.Measure(func() { body(in) })
	}
	return runs
}

// stable applies the three-of-five rule to the malloc count only and renders
// "<mallocs>/<bytes>", with the byte range of the agreeing runs when it
// varies: a map[string]any state is encoded in random iteration order, and
// its byte count changes from run to run while its malloc count does not.
//
// A state with nested maps is not even stable in its malloc count (sonic's
// map iterators, measured ±1), so for [KindMap] the rule is not applied and
// the malloc range is rendered instead.
func stable(t *testing.T, label string, runs []testsupport.Allocs) string {
	t.Helper()
	least, most := runs[0].Mallocs, runs[0].Mallocs
	for _, r := range runs[1:] {
		least, most = min(least, r.Mallocs), max(most, r.Mallocs)
	}
	if strings.HasPrefix(label, string(KindMap)+" ") || strings.Contains(label, " "+string(KindMap)+" ") {
		lo, hi := runs[0].Bytes, runs[0].Bytes
		for _, r := range runs[1:] {
			lo, hi = min(lo, r.Bytes), max(hi, r.Bytes)
		}
		return fmt.Sprintf("%d-%d/%d-%d", least, most, lo, hi)
	}
	agree := 0
	var lo, hi uint64
	for _, r := range runs {
		if r.Mallocs != least {
			continue
		}
		if agree == 0 || r.Bytes < lo {
			lo = r.Bytes
		}
		hi = max(hi, r.Bytes)
		agree++
	}
	if agree < testsupport.AllocAgree {
		t.Errorf("%s: only %d of %d runs share the minimum malloc count %d: %v", label, agree, len(runs), least, runs)
	}
	if lo == hi {
		return fmt.Sprintf("%d/%d", least, lo)
	}
	return fmt.Sprintf("%d/%d-%d", least, lo, hi)
}

// TestGrowth measures g, the allocations of one EncodeInto of a large body
// into a fresh 4 KiB scratch (the pool's initial capacity), per kind; g₆ and
// g₉ are the 6 MiB and 9 MiB rows. The count includes the encoder's own
// allocation (E_sonic) and, for the string kind, the boxing (B).
func TestGrowth(t *testing.T) {
	testsupport.QuietRuntime(t)
	for _, kind := range Kinds {
		for _, size := range Sizes[1:] {
			call := NewCall(kind, Value(kind, size, EncodeMap))
			scratch := make([]byte, 0, 4<<10)
			if err := call(&scratch); err != nil { // compile and warm the encoder
				t.Fatal(err)
			}
			var capAfter int
			runs := measureRuns(func() *[]byte {
				b := make([]byte, 0, 4<<10)
				return &b
			}, func(buf *[]byte) {
				if err := call(buf); err != nil {
					t.Fatal(err)
				}
				capAfter = cap(*buf)
			})
			got := stable(t, "grow "+string(kind)+" "+SizeName(size), runs)
			emptyPool()
			t.Logf("GROW %-14s %-6s g=%-26s capAfter=%-9d pooled=%v", kind, SizeName(size), got, capAfter, capAfter <= codec.ScratchCeiling)
		}
	}
}

// sequence is the AC-P1 mixed-size sequence (plan 6.1.6).
func sequence() []int {
	var seq []int
	add := func(size, n int) {
		for range n {
			seq = append(seq, size)
		}
	}
	add(1<<10, 10)
	add(6<<20, 1)
	add(1<<10, 10)
	add(9<<20, 1)
	add(1<<10, 10)
	return seq
}

// TestSequence runs the AC-P1 mixed-size sequence prototype: 32 pooled calls
// on a pool emptied first (a fresh client), each call's allocations taken as
// a runtime.ReadMemStats delta, five repetitions, and per call the minimum
// that at least three repetitions share. The printed per-call counts are
// what W5.2's TestAllocScratchSequence asserts.
func TestSequence(t *testing.T) {
	testsupport.QuietRuntime(t)
	seq := sequence()
	for _, kind := range Kinds {
		calls := map[int]Call{}
		for _, size := range seq {
			if calls[size] == nil {
				calls[size] = NewCall(kind, Value(kind, size, EncodeMap))
				if _, _, err := PooledCall(calls[size]); err != nil { // compile the encoder
					t.Fatal(err)
				}
			}
		}
		runs := make([][]testsupport.Allocs, len(seq))
		caps := make([]int, len(seq))
		for range testsupport.AllocRuns {
			emptyPool()
			for i, size := range seq {
				call := calls[size]
				a := testsupport.Measure(func() {
					var err error
					if _, caps[i], err = PooledCall(call); err != nil {
						t.Fatal(err)
					}
				})
				runs[i] = append(runs[i], a)
			}
		}
		counts := make([]string, len(seq))
		var capText strings.Builder
		for i := range seq {
			counts[i] = stable(t, fmt.Sprintf("%s call %d", kind, i+1), runs[i])
			fmt.Fprintf(&capText, " %d", caps[i])
		}
		t.Logf("SEQ %-14s mallocs/bytes per call 1..32: %s", kind, strings.Join(counts, " "))
		t.Logf("SEQ %-14s scratch cap after call:%s", kind, capText.String())
	}
}

// coldA and coldB have State's shape with element types of their own, used
// nowhere else, so the first encode of each compiles every encoder it needs.
type (
	coldItemA Item
	coldItemB Item
	coldA     struct {
		Name  string      `json:"name"`
		Items []coldItemA `json:"items"`
	}
	coldB struct {
		Name  string      `json:"name"`
		Items []coldItemB `json:"items"`
	}
)

func coldItems[T ~struct {
	ID    int64    `json:"id"`
	Text  string   `json:"text"`
	Tags  []string `json:"tags"`
	Score float64  `json:"score"`
	Meta  ItemMeta `json:"meta"`
}](n int) []T {
	items := make([]T, n)
	for i, it := range MakeStruct(n).Items {
		items[i] = T(it)
	}
	return items
}

// TestFirstCall measures the one-time JIT compile: the first EncodeInto of a
// fresh *struct type without Pretouch, and codec.Pretouch of another fresh
// type followed by its first EncodeInto; then the second call of each. The
// string and map rows are cold only when this test runs alone in its process
// (go test -run '^TestFirstCall$'). Times are single wall-clock readings.
func TestFirstCall(t *testing.T) {
	testsupport.QuietRuntime(t)
	measure := func(label string, f func()) {
		var d time.Duration
		a := testsupport.Measure(func() {
			start := time.Now()
			f()
			d = time.Since(start)
		})
		t.Logf("FIRST %-40s allocs/bytes=%-16s time=%v", label, a, d)
	}
	buf := make([]byte, 0, 64<<10)
	encode := func(v any) func() {
		return func() {
			buf = buf[:0]
			if err := encoder.EncodeInto(&buf, v, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := MakeString(1 << 10)
	measure("string 1KiB: first call", func() { buf = buf[:0]; _ = encoder.EncodeInto(&buf, s, 0) })
	measure("string 1KiB: second call", func() { buf = buf[:0]; _ = encoder.EncodeInto(&buf, s, 0) })
	var m any = MakeMap(14, false)
	measure("map 1KiB: first call", encode(m))
	measure("map 1KiB: second call", encode(m))

	var a any = &coldA{Name: "state", Items: coldItems[coldItemA](10)}
	measure("*coldA 1KiB: first call, no Pretouch", encode(a))
	measure("*coldA 1KiB: second call", encode(a))

	var b any = &coldB{Name: "state", Items: coldItems[coldItemB](10)}
	measure("codec.Pretouch(*coldB)", func() {
		if err := codec.Pretouch(reflect.TypeOf(b)); err != nil {
			t.Fatal(err)
		}
	})
	measure("*coldB 1KiB: first call after Pretouch", encode(b))
	measure("*coldB 1KiB: second call", encode(b))
}
