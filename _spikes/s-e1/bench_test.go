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

package se1

import "testing"

// BenchmarkEncode measures one pooled encode per kind and size with a warm
// pool (one call before the loop). The collector runs, so a collection may
// empty the pool between iterations; allocation budgets come from
// TestAllocEncode (collector off), this benchmark gives ns/op.
func BenchmarkEncode(b *testing.B) {
	for _, kind := range Kinds {
		for _, size := range Sizes {
			call := NewCall(kind, Value(kind, size, EncodeMap))
			n, _, err := PooledCall(call)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(string(kind)+"/"+SizeName(size), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(n))
				for b.Loop() {
					if _, _, err := PooledCall(call); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
