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
	"testing"

	"github.com/bytedance/sonic/encoder"
)

// BenchmarkCall measures one whole call per shape (q3: three questions and
// result.json; q20: twenty questions and result-20.json; a 1 KiB boxed-string
// state in both):
//
//   - floor: the Recorder called with a request built beforehand (its body
//     rewound each iteration), the response drained into io.Discard;
//   - esonic: sonic's EncodeInto of the state into a warm buffer, the other
//     part of the NF3 floor;
//   - sdk: the prototype's Client.SystemOne (AC-P6's call/sdk);
//   - naive: the comparator (AC-P6's call/naive).
//
// The collector runs, so a collection may empty a pool between iterations;
// the allocation budgets come from TestAllocCall (collector off). The
// transport answers at once, so everything a network costs is absent by
// design.
func BenchmarkCall(b *testing.B) {
	ctx := b.Context()
	for _, sc := range scenarios(b) {
		rec := newRecorder(sc.body)
		c := newClient(b, rec, Config{})
		naive := NewNaiveClient(rec, sc.questions.Questions)
		state := newState()
		fr := newFloorRequest(b, c, state, sc.questions)
		b.Run(sc.name+"/floor", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fr.rewind()
				if err := floorCall(rec, fr.req); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(sc.name+"/esonic", func(b *testing.B) {
			b.ReportAllocs()
			buf := make([]byte, 0, 4<<10)
			for b.Loop() {
				buf = buf[:0]
				if err := encoder.EncodeInto(&buf, state, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(sc.name+"/sdk", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				res, err := c.SystemOne(ctx, state, sc.questions)
				if err != nil {
					b.Fatal(err)
				}
				if res.Response.Answers.Len() != sc.answers {
					b.Fatalf("%d answers, want %d", res.Response.Answers.Len(), sc.answers)
				}
			}
		})
		b.Run(sc.name+"/naive", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out, err := naive.SystemOne(ctx, state)
				if err != nil {
					b.Fatal(err)
				}
				if answers, _ := out["answers"].(map[string]any); len(answers) != sc.answers {
					b.Fatalf("%d answers, want %d", len(answers), sc.answers)
				}
			}
		})
	}
}
