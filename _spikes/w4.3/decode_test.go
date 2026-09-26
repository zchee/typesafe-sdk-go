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
	"strings"
	"testing"

	typesafe "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// TestVariantsAgree checks that each replica decodes every S-D2 fixture into
// the value typesafe.DecodeAs returns, field for field, the unexported
// answer and presence bit included: the benchmarks compare decodes that do
// the same work.
func TestVariantsAgree(t *testing.T) {
	for _, f := range fixtures(t) {
		t.Run(f.name, f.agree)
	}
}

// TestReplicasRefuse checks the replicas' failure paths against responses
// that do not fit review's tags (a missing answer, a kind mismatch, an
// undeclared option, a level beyond the tag's), so that the checks the
// benchmarks run are the ones DecodeAs makes, not no-ops.
func TestReplicasRefuse(t *testing.T) {
	const (
		spam    = `"spam":{"type":"noul","noul":0.98}`
		tone    = `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}}`
		quality = `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}`
	)
	tests := map[string]struct {
		answers []string
		want    error
	}{
		"success: result.json's three answers": {
			answers: []string{spam, tone, quality},
		},
		"error: the spam answer is absent": {
			answers: []string{tone, quality},
			want:    errMissing,
		},
		"error: spam is a choice": {
			answers: []string{`"spam":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9}}`, tone, quality},
			want:    errKind,
		},
		"error: tone picks an undeclared option": {
			answers: []string{spam, `"tone":{"type":"choice","choice":"calm","confidence":0.9,"probabilities":{"calm":0.9}}`, quality},
			want:    errOption,
		},
		"error: tone gives a probability to an undeclared option": {
			answers: []string{spam, `"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"calm":0.1}}`, quality},
			want:    errOption,
		},
		"error: quality's legend names level 3 of 3": {
			answers: []string{spam, tone, `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"3":"x"},"probabilities":{"0":0.1}}`},
			want:    errLevel,
		},
		"error: quality gives a probability to level 3 of 3": {
			answers: []string{spam, tone, `"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad"},"probabilities":{"3":0.1}}`},
			want:    errLevel,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res := decodeBody(t, `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{`+strings.Join(tt.answers, ",")+`}}`)
			for vname, decode := range map[string]func() error{
				reflectAddr:   func() error { _, err := DecodeAddr[reviewMirror](res); return err },
				unsafeOffset:  func() error { _, err := DecodeOffset[reviewMirror](res); return err },
				reflectSetVar: func() error { _, err := DecodeSet[reviewMirror](res); return err },
			} {
				if err := decode(); err != tt.want { //nolint:errorlint // the replicas return the sentinels bare
					t.Errorf("%s: error = %v, want %v", vname, err, tt.want)
				}
			}
		})
	}
}

// decodeBody decodes body with reviewRoot's question set, as a call would,
// and registers reviewMirror's plan over that set.
func decodeBody(t *testing.T, body string) *wire.SystemOneResult {
	t.Helper()
	qs, err := typesafe.PreparedFor[reviewRoot]()
	if err != nil {
		t.Fatal(err)
	}
	wp := wirePrepared(t, qs)
	if err := Register[reviewMirror](wp); err != nil {
		t.Fatal(err)
	}
	res := new(wire.SystemOneResult)
	if _, err := codec.DecodeSystemOne([]byte(body), wp, typesafe.DefaultModel, res); err != nil {
		t.Fatalf("DecodeSystemOne: %v", err)
	}
	return res
}
