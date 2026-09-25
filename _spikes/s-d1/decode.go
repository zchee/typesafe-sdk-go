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

package sd1

import (
	"errors"
	"slices"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Variant names one of the candidate decoders.
type Variant uint8

const (
	// VariantA1 is the visitor, then the decoder.Skip trailing-data check,
	// then the lazy pass over the root.
	VariantA1 Variant = iota
	// VariantA2 is the sonic.ValidString pre-pass, then the visitor, then the
	// lazy pass over the root.
	VariantA2
	// VariantB is sonic.UnmarshalString into a map of NoCopyRawMessage, then
	// the visitor over each top-level member, then the lazy pass over the
	// answers member.
	VariantB
)

// Variants lists every variant, in report order.
var Variants = []Variant{VariantA1, VariantA2, VariantB}

func (v Variant) String() string {
	switch v {
	case VariantA1:
		return "a1"
	case VariantA2:
		return "a2"
	default:
		return "b"
	}
}

// Response is a decoded System One response.
type Response struct {
	Model   string
	Usage   wire.Usage
	Answers wire.Answers
}

// ModelsResponse is a decoded list-models response.
type ModelsResponse struct {
	Models []wire.ModelCard
}

// ValidationError stands in for the SDK's *ResponseValidationError: the body
// was refused, and Path says where ("." for the JSON layer).
type ValidationError struct {
	Path string
	Err  error
}

func (e *ValidationError) Error() string { return e.Path + ": " + e.Err.Error() }

func (e *ValidationError) Unwrap() error { return e.Err }

var (
	errTrailing = errors.New("data after the top-level value")
	errInvalid  = errors.New("invalid JSON")
	errMissing  = errors.New("missing")
)

func jsonErr(err error) error { return &ValidationError{Path: ".", Err: err} }

func pendErr(p pend) error { return &ValidationError{Path: p.path(), Err: errors.New(p.msg)} }

// Stats describes the work of the last decode.
type Stats struct {
	// BodyScans counts the whole-body raw-control scans (0 or 1).
	BodyScans int
	// LazyPasses counts the lazy passes (0 or 1).
	LazyPasses int
	// Members is the number of members the lazy pass iterated: root members,
	// answers members, the flagged answers' members and their legend levels
	// (AC-P8's "members visited").
	Members int
	// Structured is the number of structured legend levels filled in.
	Structured int
	// Warns counts the unknown answer types dropped.
	Warns int
}

// Decoder decodes response bodies. It keeps its scratch between calls, as a
// pooled production decoder would, so a warm Decoder shows the steady-state
// cost. A Decoder is not safe for concurrent use.
type Decoder struct {
	v       visitor
	opts    ast.VisitorOptions
	rootMap map[string]sonic.NoCopyRawMessage
	nodes   []ast.Node
	raws    []string
	rawBase []int
	stats   Stats

	// perLevelCopy makes the lazy pass copy each structured level on its own
	// instead of into one arena (ledger comparison only).
	perLevelCopy bool
}

// NewDecoder returns a Decoder whose traversals run with OnlyNumber.
func NewDecoder() *Decoder {
	return &Decoder{opts: ast.VisitorOptions{OnlyNumber: true}}
}

// Stats returns the work of the last decode.
func (d *Decoder) Stats() Stats { return d.stats }

// DecodeInto decodes body with variant into res. body must stay unmodified
// while res is in use: strings in res may alias it.
func (d *Decoder) DecodeInto(variant Variant, body []byte, res *Response) error {
	s := codec.NoCopyString(body)
	d.stats = Stats{}
	answersSrc, fromRoot, err := d.traverse(variant, s, body, modeAnswersBody)
	if err != nil {
		return err
	}
	return d.finish(answersSrc, fromRoot, res)
}

// DecodeModelsInto decodes a list-models body with variant into res.
func (d *Decoder) DecodeModelsInto(variant Variant, body []byte, res *ModelsResponse) error {
	s := codec.NoCopyString(body)
	d.stats = Stats{}
	if _, _, err := d.traverse(variant, s, body, modeModelsBody); err != nil {
		return err
	}
	v := &d.v
	switch {
	case !v.hasCards:
		return &ValidationError{Path: "models", Err: errMissing}
	case v.cardsErr.set:
		return pendErr(v.cardsErr)
	}
	res.Models = slices.Clone(v.cards)
	return nil
}

// traverse runs variant's validation and visitor passes over s. It returns
// the source the lazy pass starts from and whether that is the root.
func (d *Decoder) traverse(variant Variant, s string, body []byte, m mode) (string, bool, error) {
	v := &d.v
	switch variant {
	case VariantA1:
		v.reset(s, m)
		if err := ast.Preorder(s, v, &d.opts); err != nil {
			return "", false, jsonErr(err)
		}
		if err := trailing(body); err != nil {
			return "", false, err
		}
		return s, true, nil
	case VariantA2:
		if !sonic.ValidString(s) {
			return "", false, jsonErr(errInvalid)
		}
		v.reset(s, m)
		if err := ast.Preorder(s, v, &d.opts); err != nil {
			return "", false, jsonErr(err)
		}
		return s, true, nil
	default:
		return d.traverseB(s, m)
	}
}

// trailing is variant A1's trailing-data check: decoder.Skip finds the end of
// the root value, and only JSON whitespace may follow it.
func trailing(body []byte) error {
	start, end := decoder.Skip(body)
	if start < 0 {
		return jsonErr(errInvalid)
	}
	for _, c := range body[end:] {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return jsonErr(errTrailing)
		}
	}
	return nil
}

// traverseB is variant B: sonic validates the whole body into a map of raw
// members, and the visitor then reads each member's raw bytes. It uses
// sonic's default configuration: ValidateString would refuse raw control
// characters, but it also rewrites invalid UTF-8 to U+FFFD in keys and inside
// the raw members (which are then copies), so the invalid-UTF-8 fixture
// would pass (probe in the ledger).
func (d *Decoder) traverseB(s string, m mode) (string, bool, error) {
	if d.rootMap == nil {
		d.rootMap = make(map[string]sonic.NoCopyRawMessage, 4)
	}
	clear(d.rootMap)
	if err := sonic.UnmarshalString(s, &d.rootMap); err != nil {
		return "", false, jsonErr(err)
	}
	v := &d.v
	v.reset(s, m)
	answersSrc := ""
	for k, raw := range d.rootMap {
		sub := modeCheckOnly
		switch {
		case m == modeModelsBody && k == "models":
			sub = modeModelsValue
		case m == modeAnswersBody && k == "model":
			sub = modeModelValue
		case m == modeAnswersBody && k == "usage":
			sub = modeUsageValue
		case m == modeAnswersBody && k == "answers":
			sub = modeAnswersValue
			answersSrc = codec.NoCopyString(raw)
		}
		v.start(sub)
		if err := ast.Preorder(codec.NoCopyString(raw), v, &d.opts); err != nil {
			return "", false, jsonErr(err)
		}
	}
	return answersSrc, false, nil
}

// finish reports the first pending error, runs the lazy pass when a legend
// level is structured, and moves the answers into res.
func (d *Decoder) finish(src string, fromRoot bool, res *Response) error {
	v := &d.v
	switch {
	case !v.hasModel:
		return &ValidationError{Path: "model", Err: errMissing}
	case v.modelErr.set:
		return pendErr(v.modelErr)
	case !v.hasUsage:
		return &ValidationError{Path: "usage", Err: errMissing}
	case v.usageErr.set:
		return pendErr(v.usageErr)
	case v.answersErr.set:
		return pendErr(v.answersErr)
	}
	known, structured := 0, 0
	for i := range v.set {
		e := &v.set[i]
		if e.err.set {
			return pendErr(e.err)
		}
		if e.ans.Kind != wire.KindUnknown {
			known++
			structured += e.structured
		} else {
			d.stats.Warns++
		}
	}
	d.stats.BodyScans = v.scans
	if structured > 0 {
		if err := d.lazy(src, fromRoot); err != nil {
			return err
		}
	}
	res.Model, res.Usage = v.model, v.usage
	res.Answers.Grow(known)
	for i := range v.set {
		if e := &v.set[i]; e.ans.Kind != wire.KindUnknown {
			res.Answers.Put(e.name, e.ans)
		}
	}
	return nil
}
