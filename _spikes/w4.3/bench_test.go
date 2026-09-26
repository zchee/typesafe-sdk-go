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
	"testing"
)

// BenchmarkSD2 times the typed decode of each S-D2 fixture's response:
// typesafe.DecodeAs itself (0-DecodeAs), and the replicas that differ from
// it only in how an answer reaches its field (1-reflect-addr, the store as
// built; 2-unsafe-offset; 3-reflect-set), with the diagnostic
// 2h-unsafe-offset-heap (variant 2 with the struct on the heap). The
// response is decoded once, before the loop; each iteration fills a fresh
// struct from it, as DecodeAs does per call.
func BenchmarkSD2(b *testing.B) {
	for _, f := range fixtures(b) {
		b.Run(f.name, func(b *testing.B) {
			for _, v := range f.variants {
				b.Run(v.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if err := v.run(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
