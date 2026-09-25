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

// Package se1 is throwaway code for spike S-E1 (port plan sections 6.1.5 and
// 6.1.6): the allocations and time of sonic's encoder.EncodeInto into a
// pooled codec.Body scratch, by state kind and size, the growth counts g₆
// and g₉, and a prototype of the AC-P1 mixed-size sequence.
//
// The leading underscore of the directory keeps the package out of ./...,
// CI, golangci-lint and coverage. Run it with an explicit path:
//
//	go test ./_spikes/s-e1/
package se1

import (
	"strconv"
	"strings"
	"sync"
)

// Sizes are the target encoded sizes S-E1 measures.
var Sizes = []int{1 << 10, 64 << 10, 1 << 20, 6 << 20, 9 << 20}

// SizeName renders a size as 1KiB, 64KiB, 1MiB, ...
func SizeName(n int) string {
	if n >= 1<<20 {
		return strconv.Itoa(n>>20) + "MiB"
	}
	return strconv.Itoa(n>>10) + "KiB"
}

// Kind is a request state kind (plan section 5: string, map, slice, struct,
// RawJSON).
type Kind string

const (
	// KindString is a bare string: the caller's variable is boxed into the
	// state interface at the call site (NF1's B = 1).
	KindString Kind = "string"
	// KindBoxed is a string already boxed in an any, as the SDK receives it.
	KindBoxed Kind = "boxed"
	// KindMap is a map[string]any with nested maps, slices and numbers.
	KindMap Kind = "map"
	// KindMapFlat is a map[string]any whose values are all strings: the
	// map's own cost without nested maps.
	KindMapFlat Kind = "map-flat"
	// KindStruct is a pointer to a struct with nested structs and slices.
	KindStruct Kind = "struct-ptr"
	// KindRaw is RawJSON: verbatim bytes appended to the scratch, no encoder.
	KindRaw Kind = "raw-verbatim"
	// KindRawMessage is json.RawMessage through EncodeInto, for comparison
	// with KindRaw (the Marshaler path validates and compacts).
	KindRawMessage Kind = "raw-encodeinto"
)

// Kinds lists every kind, in report order.
var Kinds = []Kind{KindString, KindBoxed, KindMap, KindMapFlat, KindStruct, KindRaw, KindRawMessage}

// Item is one element of [State].
type Item struct {
	ID    int64    `json:"id"`
	Text  string   `json:"text"`
	Tags  []string `json:"tags"`
	Score float64  `json:"score"`
	Meta  ItemMeta `json:"meta"`
}

// ItemMeta is a nested struct of [Item].
type ItemMeta struct {
	Source string `json:"source"`
	Rank   int    `json:"rank"`
}

// State is the struct kind's state: nested fields, a slice of structs.
type State struct {
	Name  string `json:"name"`
	Items []Item `json:"items"`
}

var (
	textChunk = strings.Repeat("abcdefghij", 4) // 40 bytes, no escapes
	valueText = strings.Repeat("0123456789abcdef", 3)
)

// MakeString returns a string whose JSON encoding is size bytes.
func MakeString(size int) string { return strings.Repeat("s", size-2) }

// MakeStruct returns a *State with n items.
func MakeStruct(n int) *State {
	st := &State{Name: "state", Items: make([]Item, max(n, 1))}
	for i := range st.Items {
		st.Items[i] = Item{
			ID: int64(i), Text: textChunk, Tags: []string{"alpha", "beta"},
			Score: 0.5, Meta: ItemMeta{Source: "spike", Rank: i % 10},
		}
	}
	return st
}

// MakeMap returns a map[string]any with n entries: strings, numbers, nested
// maps and slices in turn, or only strings when flat.
func MakeMap(n int, flat bool) map[string]any {
	n = max(n, 1)
	m := make(map[string]any, n)
	for i := range n {
		k := "k" + strconv.Itoa(1000000+i)
		switch {
		case flat || i%4 == 0:
			m[k] = valueText
		case i%4 == 1:
			m[k] = float64(i) + 0.25
		case i%4 == 2:
			m[k] = map[string]any{"a": "nested", "b": int64(i)}
		default:
			m[k] = []any{"x", 1.5, true}
		}
	}
	return m
}

// calibrate returns the element count whose encoding is about size bytes:
// it encodes a 1000-element sample and scales.
func calibrate(size int, build func(n int) any, encode func(any) []byte) int {
	const sample = 1000
	per := float64(len(encode(build(sample)))) / sample
	return max(int(float64(size)/per+0.5), 1)
}

type stateKey struct {
	kind Kind
	size int
}

var (
	stateMu    sync.Mutex
	stateCache = map[stateKey]any{}
)

// Value returns the value of kind whose encoding is about size bytes, built
// once per process. For KindString it is the string itself (the caller boxes
// it at the call site), for KindRaw and KindRawMessage the map kind's
// encoding.
func Value(kind Kind, size int, encode func(any) []byte) any {
	stateMu.Lock()
	defer stateMu.Unlock()
	return value(kind, size, encode)
}

// value is Value with stateMu held.
func value(kind Kind, size int, encode func(any) []byte) any {
	key := stateKey{kind, size}
	if v, ok := stateCache[key]; ok {
		return v
	}
	var v any
	switch kind {
	case KindString, KindBoxed:
		v = MakeString(size)
	case KindMap, KindMapFlat:
		flat := kind == KindMapFlat
		build := func(n int) any { return MakeMap(n, flat) }
		v = build(calibrate(size, build, encode))
	case KindStruct:
		build := func(n int) any { return MakeStruct(n) }
		v = build(calibrate(size, build, encode))
	case KindRaw, KindRawMessage:
		v = encode(value(KindMap, size, encode))
	}
	stateCache[key] = v
	return v
}
