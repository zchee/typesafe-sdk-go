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

// Package validrace is a W1.2 probe of sonic v1.15.4's check of a
// json.Marshaler's output (internal/encoder/prim EncodeJsonMarshaler →
// alg.Valid → native.ValidateOne), the check a nested RawJSON or JSON Content
// relies on (rulings R52, R57). In a normal build it refuses every invalid
// output; built with -race it accepts some complete but invalid outputs a
// fraction of the time, while truncated outputs are refused in both builds.
// The leading underscore of _spikes keeps it out of ./...; run it with an
// explicit path, once without and once with -race:
//
//	GOEXPERIMENT=nosimd,noruntimesecret go test -count=1 -v ./_spikes/w1.2/validrace/
//	GOEXPERIMENT=nosimd,noruntimesecret go test -race -count=1 -v ./_spikes/w1.2/validrace/
//
// results/validrace-{normal,race}-M.txt are its runs on (M).
package validrace

import (
	"encoding/json"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bytedance/sonic/encoder"
)

var (
	// complete: every bracket closed, invalid inside.
	complete = []string{`{"a":}`, `[1 2]`, `{"a" 1}`, `{"a":1,}`, `{,}`}
	// truncated: the output ends before the value does.
	truncated = []string{`{"a":`, `[1,`, `{"a":1`, `"abc`, `[`, `tru`}
	// trailing: a valid value followed by more.
	trailing = []string{`{"a":1}x`, `1 2`}
	// valid outputs, which must always pass.
	valid = []string{`{"a":1}`, `[1,2,{"b":null}]`, `"x"`, `{"k":[true,false,null,1.5e3]}`, `  {"a": [ ] }  `}
)

// encode encodes s as a json.RawMessage nested in a map, as a nested RawJSON
// reaches sonic, and reports whether sonic accepted it.
func encode(s string) bool {
	var buf []byte
	return encoder.EncodeInto(&buf, map[string]any{"c": json.RawMessage([]byte(s))}, 0) == nil
}

// report logs, per input, how many times sonic accepted it out of tries.
func report(t *testing.T, label string, tries int, accepted map[string]int, inputs []string) {
	t.Helper()
	var sb strings.Builder
	for _, s := range inputs {
		sb.WriteString("  ")
		sb.WriteString(s)
		sb.WriteString(": accepted ")
		sb.WriteString(strconv.Itoa(accepted[s]))
		sb.WriteString(" of ")
		sb.WriteString(strconv.Itoa(tries))
		sb.WriteByte('\n')
	}
	t.Logf("RESULT %s\n%s", label, sb.String())
}

// TestValidMarshalerOutput encodes each invalid output rounds times,
// interleaved with the valid ones, and reports how often sonic accepted it.
func TestValidMarshalerOutput(t *testing.T) {
	const rounds = 20000
	invalid := slices.Concat(complete, truncated, trailing)
	accepted := map[string]int{}
	for i := range rounds {
		for _, s := range valid {
			if !encode(s) {
				t.Errorf("valid output %s refused", s)
			}
		}
		for _, s := range invalid {
			if encode(s) {
				accepted[s]++
			}
		}
		if i%5000 == 0 {
			runtime.GC()
		}
	}
	report(t, "complete but invalid", rounds, accepted, complete)
	report(t, "truncated", rounds, accepted, truncated)
	report(t, "trailing data", rounds, accepted, trailing)
}

// TestValidAfterPoolRefill empties sonic's pools (two collections) before
// every encode, so each check starts from a new state machine.
func TestValidAfterPoolRefill(t *testing.T) {
	const rounds = 4000
	accepted := map[string]int{}
	for range rounds {
		for _, s := range complete {
			runtime.GC()
			runtime.GC()
			if encode(s) {
				accepted[s]++
			}
		}
	}
	report(t, "complete but invalid, pools emptied before each encode", rounds, accepted, complete)
}
