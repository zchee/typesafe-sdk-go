//go:build !race

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

package sd2

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// none is the input of a measured section that needs none.
func none() struct{} { return struct{}{} }

// TestSD2Allocs counts the allocations of each typed decode of each S-D2
// fixture, as TestAllocTypedDecode counts DecodeAs's (AC-P3):
// runtime.ReadMemStats deltas, collector off, GOMAXPROCS 1, the minimum that
// three of five runs share, after one warm-up decode. It checks the shape
// the replicas are expected to have: variant 1 costs what DecodeAs costs
// (the T moved to the heap), variant 2 nothing, the diagnostic 2h the T
// alone, variant 3 the T and one boxed answer per answer field.
func TestSD2Allocs(t *testing.T) {
	fs := fixtures(t)
	testsupport.QuietRuntime(t)
	for _, f := range fs {
		got := make(map[string]testsupport.Allocs, len(f.variants))
		var line strings.Builder
		fmt.Fprintf(&line, "SD2ALLOC %s fields=%d", f.name, f.fields)
		for _, v := range f.variants {
			if err := v.run(); err != nil {
				t.Fatalf("%s: %s: %v", f.name, v.name, err)
			}
			var err error
			got[v.name] = testsupport.MeasureMin(t, f.name+" "+v.name, none, func(struct{}) { err = v.run() })
			if err != nil {
				t.Fatalf("%s: %s: %v", f.name, v.name, err)
			}
			fmt.Fprintf(&line, " %s=%s", v.name, got[v.name])
		}
		t.Log(line.String())
		if got[reflectAddr] != got[asBuilt] {
			t.Errorf("%s: %s allocates %s, DecodeAs %s: the replica does not cost what the production decode costs", f.name, reflectAddr, got[reflectAddr], got[asBuilt])
		}
		if got[asBuilt].Mallocs != 1 {
			t.Errorf("%s: DecodeAs allocates %s, want 1 allocation, the T", f.name, got[asBuilt])
		}
		if got[unsafeOffset] != (testsupport.Allocs{}) {
			t.Errorf("%s: %s allocates %s, want nothing", f.name, unsafeOffset, got[unsafeOffset])
		}
		if got[unsafeHeap] != got[asBuilt] {
			t.Errorf("%s: %s allocates %s, want DecodeAs's %s, the T alone", f.name, unsafeHeap, got[unsafeHeap], got[asBuilt])
		}
		if want := 1 + uint64(f.fields); got[reflectSetVar].Mallocs != want { //nolint:gosec // G115: a field count is never negative
			t.Errorf("%s: %s makes %d allocations, want %d: the T and one per answer", f.name, reflectSetVar, got[reflectSetVar].Mallocs, want)
		}
	}
}

// firstCallEnv names the shape a child process of TestPreparedForFirstCall
// measures; it is unset in the parent.
const firstCallEnv = "SD2_FIRST_CALL"

// firstCallProfileEnv, when set in a child's environment, names a file
// for the allocation profile of the first call alone: the child samples
// every allocation from the end of the warm-up call to the end of the
// first call (runtime.MemProfileRate 1), then writes the allocs profile.
// Run by hand for the call sites:
//
//	go test -c -o sd2.test ./_spikes/w4.3/
//	SD2_FIRST_CALL=twenty SD2_FIRST_CALL_PROFILE=twenty.pprof ./sd2.test -test.run='^TestFirstCallChild$'
//	go tool pprof -sample_index=alloc_objects -traces sd2.test twenty.pprof
//
// An allocation that the runtime's tiny allocator fits into its current
// 16-byte block is counted in MemStats.Mallocs but never sampled.
const firstCallProfileEnv = "SD2_FIRST_CALL_PROFILE"

// writeAllocs writes the allocs profile to path, after two collections so
// that it holds every allocation sampled so far.
func writeAllocs(t *testing.T, path string) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	f, err := os.Create(path) //nolint:gosec // G703: the path is the caller's own, from the environment of a test it runs by hand
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
		t.Fatal(err)
	}
}

// firstCallLine is the line a child prints: the warm-up call, the first
// call for the shape's type and the second, each as mallocs/bytes.
var firstCallLine = regexp.MustCompile(`FIRSTCALL shape=(\S+) processFirst=(\d+)/(\d+) first=(\d+)/(\d+) second=(\d+)/(\d+)`)

// TestPreparedForFirstCall counts the allocations of the first
// PreparedFor[T] call for each shape (W4.3 deliverable B), beside the second
// call and the same question set built by hand and prepared.
//
// The first call for a type happens once per process, so each run is a
// child process: this test binary run again with only TestFirstCallChild,
// which calls PreparedFor once for a warm-up type and then twice for the
// shape's type, counting each call with runtime.ReadMemStats deltas
// (collector off, GOMAXPROCS 1). Five children per shape; the result is the
// minimum that three of the five share, as MeasureMin's. testing.AllocsPerRun
// cannot count a first call (its warm-up call is the first call), and a
// fresh type per run in one process would not cost the same each run: the
// plan cache is a sync.Map, whose hash trie allocates a new 16-way node when
// the new type's hash shares a prefix with a cached type's, which becomes
// likelier with every type cached before it. In a child the cache holds
// only the warm-up type, so that happens in one first call in 16.
func TestPreparedForFirstCall(t *testing.T) {
	if os.Getenv(firstCallEnv) != "" {
		t.Skip("in a child process of this test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type runs struct{ processFirst, first, second []testsupport.Allocs }
	all := make([]runs, len(shapes))
	for i, s := range shapes {
		for range testsupport.AllocRuns {
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestFirstCallChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), firstCallEnv+"="+s.name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child for %s: %v\n%s", s.name, err, out)
			}
			m := firstCallLine.FindSubmatch(out)
			if m == nil || string(m[1]) != s.name {
				t.Fatalf("child for %s printed no FIRSTCALL line:\n%s", s.name, out)
			}
			n := make([]uint64, 6)
			for j := range n {
				if n[j], err = strconv.ParseUint(string(m[2+j]), 10, 64); err != nil {
					t.Fatal(err)
				}
			}
			all[i].processFirst = append(all[i].processFirst, testsupport.Allocs{Mallocs: n[0], Bytes: n[1]})
			all[i].first = append(all[i].first, testsupport.Allocs{Mallocs: n[2], Bytes: n[3]})
			all[i].second = append(all[i].second, testsupport.Allocs{Mallocs: n[4], Bytes: n[5]})
		}
	}

	testsupport.QuietRuntime(t)
	for i, s := range shapes {
		warm := testsupport.StableMin(t, s.name+" warm-up call (process first)", all[i].processFirst)
		first := testsupport.StableMin(t, s.name+" first call", all[i].first)
		second := testsupport.StableMin(t, s.name+" second call", all[i].second)
		var perr error
		byHand := testsupport.MeasureMin(t, s.name+" by hand", none, func(struct{}) { _, perr = s.byHand().Prepare() })
		if perr != nil {
			t.Fatal(perr)
		}
		prepare := testsupport.MeasureMin(t, s.name+" Prepare", s.byHand, func(q *typesafe.Questions) { _, perr = q.Prepare() })
		if perr != nil {
			t.Fatal(perr)
		}
		extraM := float64(first.Mallocs) - float64(byHand.Mallocs)
		extraB := float64(first.Bytes) - float64(byHand.Bytes)
		t.Logf("FIRST %s fields=%d first=%s second=%s byHand=%s prepare=%s typedExtra=%.0f/%.0f perField=%.2f/%.1f warmUp=%s",
			s.name, s.fields, first, second, byHand, prepare, extraM, extraB, extraM/float64(s.fields), extraB/float64(s.fields), warm)
		if second != (testsupport.Allocs{}) {
			t.Errorf("%s: second call allocates %s, want nothing (W4.1's cache pin)", s.name, second)
		}
		if first.Mallocs <= prepare.Mallocs {
			t.Errorf("%s: first call %s allocates no more than Prepare alone %s", s.name, first, prepare)
		}
	}
}

// TestFirstCallChild is TestPreparedForFirstCall's child: it runs only in a
// process that test starts, and prints one FIRSTCALL line.
func TestFirstCallChild(t *testing.T) {
	name := os.Getenv(firstCallEnv)
	if name == "" {
		t.Skip("TestPreparedForFirstCall runs this test in a child process")
	}
	i := slices.IndexFunc(shapes, func(s shape) bool { return s.name == name })
	if i < 0 {
		t.Fatalf("no shape %q", name)
	}
	s := shapes[i]
	testsupport.QuietRuntime(t)
	var err error
	warm := testsupport.Measure(func() { _, err = typesafe.PreparedFor[warmUp]() })
	if err != nil {
		t.Fatal(err)
	}
	profile := os.Getenv(firstCallProfileEnv)
	if profile != "" {
		runtime.MemProfileRate = 1
	}
	first := testsupport.Measure(func() { _, err = s.preparedFor() })
	if err != nil {
		t.Fatal(err)
	}
	if profile != "" {
		writeAllocs(t, profile)
	}
	second := testsupport.Measure(func() { _, err = s.preparedFor() })
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FIRSTCALL shape=%s processFirst=%s first=%s second=%s\n", s.name, warm, first, second)
}
