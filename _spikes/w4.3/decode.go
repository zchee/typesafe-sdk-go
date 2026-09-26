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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// NoulAnswer is laid out as the root package's NoulAnswer.
type NoulAnswer struct {
	w       wire.NoulAnswer
	present bool
}

// ChoiceAnswer is laid out as the root package's ChoiceAnswer.
type ChoiceAnswer struct {
	w       wire.ChoiceAnswer
	present bool
}

// ScoreAnswer is laid out as the root package's ScoreAnswer.
type ScoreAnswer struct {
	w       wire.ScoreAnswer
	present bool
}

// field is one answer field of a plan: typedField of the root package, plus
// the field's offset for variant 2.
type field struct {
	index    int
	offset   uintptr
	name     string
	kind     wire.Kind
	optional bool
	options  []string
	levels   []wire.Content
}

// plan is the root package's typedPlan without the prepared set and error.
type plan struct {
	fields []field
}

// plans caches the plan of each registered type, as the root package's
// typedPlans does: a reflect.Type key and a *plan value.
var plans sync.Map

// planFor returns the plan of T, which [Register] must have stored.
func planFor[T any]() *plan {
	p, _ := plans.Load(reflect.TypeFor[T]())
	return p.(*plan)
}

// Register builds the plan of the struct type T, whose fields are this
// package's answer types with the root package's tags, over q, the question
// set the root package's PreparedFor built for the same tags: the i-th
// answer field of T asks q's i-th question, and takes its option and level
// tables from it as buildPlan does. The tags are split on ";" and "=" only;
// the spike's types use no escapes.
func Register[T any](q *wire.Prepared) error {
	t := reflect.TypeFor[T]()
	entries := q.Entries()
	var fields []field
	for i := range t.NumField() {
		sf := t.Field(i)
		tag, ok := sf.Tag.Lookup("typesafe")
		if !ok {
			continue
		}
		f := field{index: i, offset: sf.Offset, name: sf.Name}
		for kv := range strings.SplitSeq(tag, ";") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "kind":
				f.kind = map[string]wire.Kind{"noul": wire.KindNoul, "choice": wire.KindChoice, "score": wire.KindScore}[v]
			case "name":
				f.name = v
			case "optional":
				f.optional = true
			}
		}
		j := len(fields)
		if j >= len(entries) || entries[j].Name != f.name || entries[j].Kind != f.kind {
			return fmt.Errorf("sd2: %s field %s asks %q, which is not question %d of the set", t, sf.Name, f.name, j)
		}
		f.options, f.levels = entries[j].Options, entries[j].Levels
		fields = append(fields, f)
	}
	if len(fields) != len(entries) {
		return fmt.Errorf("sd2: %s has %d answer fields for %d questions", t, len(fields), len(entries))
	}
	plans.Store(t, &plan{fields: fields})
	return nil
}

// Why an answer does not fit its field. The replicas return these bare:
// the success path, the one measured, never builds the root package's
// *ResponseValidationError.
var (
	errMissing = errors.New("the answer is absent, and the field is not optional")
	errKind    = errors.New("the answer is of another kind than its field")
	errOption  = errors.New("the answer names an option its field's tag does not list")
	errLevel   = errors.New("the answer names a level beyond the levels its field's tag lists")
)

// DecodeAddr is variant 1, the store as built: decodeTyped of the root
// package with the plan lookup of typedPlanFor.
func DecodeAddr[T any](res *wire.SystemOneResult) (T, error) {
	p := planFor[T]()
	var t T
	if err := p.decodeAddr(res, reflect.ValueOf(&t).Elem()); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// decodeAddr is typedPlan.decode of the root package: each field's address
// as an interface holding a pointer, type-asserted, and the answer stored
// through it.
func (p *plan) decodeAddr(res *wire.SystemOneResult, v reflect.Value) error {
	answers := &res.Answers
	for i := range p.fields {
		f := &p.fields[i]
		a, ok := answers.Get(f.name)
		switch {
		case !ok && f.optional:
			continue
		case !ok:
			return errMissing
		case a.Kind != f.kind:
			return errKind
		}
		dst := v.Field(f.index).Addr().Interface()
		switch f.kind {
		case wire.KindNoul:
			*dst.(*NoulAnswer) = NoulAnswer{w: a.Noul, present: true}
		case wire.KindChoice:
			if _, bad := undeclaredOption(&a.Choice, f.options); bad {
				return errOption
			}
			*dst.(*ChoiceAnswer) = ChoiceAnswer{w: a.Choice, present: true}
		default:
			if _, bad := undeclaredLevel(&a.Score, uint64(len(f.levels))); bad {
				return errLevel
			}
			*dst.(*ScoreAnswer) = ScoreAnswer{w: a.Score, present: true}
		}
	}
	return nil
}

// DecodeOffset is variant 2: the T's address passed as an unsafe.Pointer,
// each answer stored at the field's offset from it.
func DecodeOffset[T any](res *wire.SystemOneResult) (T, error) {
	p := planFor[T]()
	var t T
	if err := p.decodeOffset(res, unsafe.Pointer(&t)); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// HeapT holds the T of the last DecodeOffsetHeap call: storing it in a
// package variable is what moves the T to the heap.
var HeapT unsafe.Pointer

// DecodeOffsetHeap is variant 2 with the T on the heap, where DecodeAddr's
// reflect.ValueOf(&t) puts it: a diagnostic, not a candidate. Against
// variant 1 it leaves the reflect work per field; against variant 2, the
// allocation of the T (and the collector's share of it).
func DecodeOffsetHeap[T any](res *wire.SystemOneResult) (T, error) {
	p := planFor[T]()
	t := new(T)
	HeapT = unsafe.Pointer(t)
	if err := p.decodeOffset(res, unsafe.Pointer(t)); err != nil {
		var zero T
		return zero, err
	}
	return *t, nil
}

// decodeOffset is decodeAddr with the store through unsafe.Add(base,
// offset) in place of the field's address as an interface.
func (p *plan) decodeOffset(res *wire.SystemOneResult, base unsafe.Pointer) error {
	answers := &res.Answers
	for i := range p.fields {
		f := &p.fields[i]
		a, ok := answers.Get(f.name)
		switch {
		case !ok && f.optional:
			continue
		case !ok:
			return errMissing
		case a.Kind != f.kind:
			return errKind
		}
		dst := unsafe.Add(base, f.offset)
		switch f.kind {
		case wire.KindNoul:
			*(*NoulAnswer)(dst) = NoulAnswer{w: a.Noul, present: true}
		case wire.KindChoice:
			if _, bad := undeclaredOption(&a.Choice, f.options); bad {
				return errOption
			}
			*(*ChoiceAnswer)(dst) = ChoiceAnswer{w: a.Choice, present: true}
		default:
			if _, bad := undeclaredLevel(&a.Score, uint64(len(f.levels))); bad {
				return errLevel
			}
			*(*ScoreAnswer)(dst) = ScoreAnswer{w: a.Score, present: true}
		}
	}
	return nil
}

// DecodeSet is variant 3: DecodeAddr with reflect.Value.Set in place of the
// store through the field's pointer.
func DecodeSet[T any](res *wire.SystemOneResult) (T, error) {
	p := planFor[T]()
	var t T
	if err := p.decodeSet(res, reflect.ValueOf(&t).Elem()); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// decodeSet is decodeAddr with each answer set as a reflect.Value.
func (p *plan) decodeSet(res *wire.SystemOneResult, v reflect.Value) error {
	answers := &res.Answers
	for i := range p.fields {
		f := &p.fields[i]
		a, ok := answers.Get(f.name)
		switch {
		case !ok && f.optional:
			continue
		case !ok:
			return errMissing
		case a.Kind != f.kind:
			return errKind
		}
		dst := v.Field(f.index)
		switch f.kind {
		case wire.KindNoul:
			dst.Set(reflect.ValueOf(NoulAnswer{w: a.Noul, present: true}))
		case wire.KindChoice:
			if _, bad := undeclaredOption(&a.Choice, f.options); bad {
				return errOption
			}
			dst.Set(reflect.ValueOf(ChoiceAnswer{w: a.Choice, present: true}))
		default:
			if _, bad := undeclaredLevel(&a.Score, uint64(len(f.levels))); bad {
				return errLevel
			}
			dst.Set(reflect.ValueOf(ScoreAnswer{w: a.Score, present: true}))
		}
	}
	return nil
}

// undeclaredOption is the root package's, verbatim.
func undeclaredOption(a *wire.ChoiceAnswer, options []string) (at codec.FieldPath, bad bool) {
	if !slices.Contains(options, a.Choice) {
		return codec.FieldPath{Member: "choice"}, true
	}
	for _, p := range a.Probabilities {
		if !slices.Contains(options, p.Label) {
			return codec.FieldPath{Member: "probabilities", Key: p.Label, HasKey: true}, true
		}
	}
	return codec.FieldPath{}, false
}

// undeclaredLevel is the root package's, verbatim.
func undeclaredLevel(a *wire.ScoreAnswer, levels uint64) (at codec.FieldPath, bad bool) {
	for _, e := range a.Legend {
		if uint64(e.Level) >= levels {
			return codec.FieldPath{Member: "legend", Key: strconv.FormatUint(uint64(e.Level), 10), HasKey: true}, true
		}
	}
	for _, p := range a.Probabilities {
		if uint64(p.Level) >= levels {
			return codec.FieldPath{Member: "probabilities", Key: strconv.FormatUint(uint64(p.Level), 10), HasKey: true}, true
		}
	}
	return codec.FieldPath{}, false
}
