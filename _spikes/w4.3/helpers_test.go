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
	"net/http"
	"os"
	"reflect"
	"testing"
	"unsafe"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The fixture types over the root package's answer types and over this
// package's.
type (
	reviewRoot     = Review[typesafe.NoulAnswer, typesafe.ChoiceAnswer, typesafe.ScoreAnswer]
	reviewMirror   = Review[NoulAnswer, ChoiceAnswer, ScoreAnswer]
	result20Root   = Result20[typesafe.NoulAnswer, typesafe.ChoiceAnswer, typesafe.ScoreAnswer]
	result20Mirror = Result20[NoulAnswer, ChoiceAnswer, ScoreAnswer]
	flood1kRoot    = Flood1k[typesafe.NoulAnswer, typesafe.ChoiceAnswer, typesafe.ScoreAnswer]
	flood1kMirror  = Flood1k[NoulAnswer, ChoiceAnswer, ScoreAnswer]
)

// Variant names, as the benchmarks and the allocation lines print them.
const (
	asBuilt       = "0-DecodeAs"
	reflectAddr   = "1-reflect-addr"
	unsafeOffset  = "2-unsafe-offset"
	unsafeHeap    = "2h-unsafe-offset-heap"
	reflectSetVar = "3-reflect-set"
)

// variant is one way to decode a fixture's answers into its struct type.
type variant struct {
	name string
	// run decodes once; the result is kept in a variable of the fixture
	// case, so no variant's copy of the struct can be elided.
	run func() error
}

// fixture is one S-D2 fixture with the five decodes of its answers.
type fixture struct {
	name string
	// fields is the number of answer fields of the fixture's type.
	fields   int
	variants []variant
	// agree runs every variant once and compares the result of each
	// replica with DecodeAs's.
	agree func(t *testing.T)
}

// fixtures returns the three S-D2 fixtures, in the order the tables list
// them.
func fixtures(tb testing.TB) []fixture {
	tb.Helper()
	return []fixture{
		newFixture[reviewRoot, reviewMirror](tb, "result.json"),
		newFixture[result20Root, result20Mirror](tb, "result-20.json"),
		newFixture[flood1kRoot, flood1kMirror](tb, "structured-legend-flood-1k.json"),
	}
}

// newFixture prepares name, a fixture under testdata, for R, its struct
// type over the root package's answer types, and M, the same type over
// this package's: the response of a call over the Recorder (for DecodeAs)
// and the same body decoded with the same question set by the codec (for
// the replicas), both before any measurement.
func newFixture[R, M any](tb testing.TB, name string) fixture {
	tb.Helper()
	checkLayout[R, M](tb)
	qs, err := typesafe.PreparedFor[R]()
	if err != nil {
		tb.Fatal(err)
	}
	wp := wirePrepared(tb, qs)
	if err := Register[M](wp); err != nil {
		tb.Fatal(err)
	}
	body := testsupport.Fixture(tb, name)
	resp := call(tb, body, qs)
	res := new(wire.SystemOneResult)
	if _, err := codec.DecodeSystemOne(body, wp, typesafe.DefaultModel, res); err != nil {
		tb.Fatalf("%s: DecodeSystemOne: %v", name, err)
	}
	var (
		outR R
		outM M
	)
	f := fixture{
		name:   name,
		fields: len(wp.Entries()),
		variants: []variant{
			{asBuilt, func() (err error) { outR, err = typesafe.DecodeAs[R](resp); return err }},
			{reflectAddr, func() (err error) { outM, err = DecodeAddr[M](res); return err }},
			{unsafeOffset, func() (err error) { outM, err = DecodeOffset[M](res); return err }},
			{unsafeHeap, func() (err error) { outM, err = DecodeOffsetHeap[M](res); return err }},
			{reflectSetVar, func() (err error) { outM, err = DecodeSet[M](res); return err }},
		},
	}
	f.agree = func(t *testing.T) {
		t.Helper()
		outR = *new(R)
		if err := f.variants[0].run(); err != nil {
			t.Fatalf("%s: DecodeAs: %v", name, err)
		}
		if diff := gocmp.Diff(*new(R), outR, exportAll); diff == "" {
			t.Fatalf("%s: DecodeAs returned the zero %T", name, outR)
		}
		for _, v := range f.variants[1:] {
			outM = *new(M)
			if err := v.run(); err != nil {
				t.Errorf("%s: %s: %v", name, v.name, err)
				continue
			}
			got := *(*R)(unsafe.Pointer(&outM))
			if diff := gocmp.Diff(outR, got, exportAll); diff != "" {
				t.Errorf("%s: %s disagrees with DecodeAs (-DecodeAs +%s):\n%s", name, v.name, v.name, diff)
			}
		}
	}
	return f
}

// exportAll lets gocmp read the answer types' unexported fields.
var exportAll = gocmp.Exporter(func(reflect.Type) bool { return true })

// checkLayout fails unless R and M have the same size and every field of
// both is at the same offset with the same size, so that an M read as an R
// is the value the replica built.
func checkLayout[R, M any](tb testing.TB) {
	tb.Helper()
	r, m := reflect.TypeFor[R](), reflect.TypeFor[M]()
	if r.Size() != m.Size() || r.NumField() != m.NumField() {
		tb.Fatalf("%s (%d B) and %s (%d B) differ", r, r.Size(), m, m.Size())
	}
	for i := range r.NumField() {
		rf, mf := r.Field(i), m.Field(i)
		if rf.Offset != mf.Offset || rf.Type.Size() != mf.Type.Size() || rf.Tag != mf.Tag {
			tb.Fatalf("field %d: %s at %d (%d B) vs %s at %d (%d B)", i, rf.Name, rf.Offset, rf.Type.Size(), mf.Name, mf.Offset, mf.Type.Size())
		}
	}
}

// wirePrepared returns the wire set q wraps: typesafe.Prepared is a struct
// whose one field is a wire.Prepared, which the check below confirms.
func wirePrepared(tb testing.TB, q *typesafe.Prepared) *wire.Prepared {
	tb.Helper()
	t := reflect.TypeFor[typesafe.Prepared]()
	if t.NumField() != 1 || t.Field(0).Type != reflect.TypeFor[wire.Prepared]() || t.Field(0).Offset != 0 {
		tb.Fatalf("%s is not a struct holding one wire.Prepared", t)
	}
	return (*wire.Prepared)(unsafe.Pointer(q))
}

// call returns the response of a SystemOne call over a Recorder that
// replies body, with the question set q, as TestAllocTypedDecode makes it.
func call(tb testing.TB, body []byte, q *typesafe.Prepared) *typesafe.SystemOneResponse {
	tb.Helper()
	for _, name := range []string{typesafe.APIKeyEnv, typesafe.BaseURLEnv, typesafe.DefaultModelEnv} {
		tb.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			tb.Fatalf("unset %s: %v", name, err)
		}
	}
	rec := &testsupport.Recorder{Discard: true, Replies: []testsupport.Reply{testsupport.JSON(http.StatusOK, body)}}
	c, err := typesafe.NewClient(typesafe.WithAPIKey("test-key"), typesafe.WithRoundTripper(rec), typesafe.WithRetry(typesafe.NoRetry()))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = c.Close() })
	resp, err := c.SystemOne(tb.Context(), "x", q)
	if err != nil {
		tb.Fatal(err)
	}
	return resp
}
