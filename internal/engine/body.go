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

package engine

import (
	"fmt"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// BodyMember is one member a call adds to the top level of its request
// body, as the Python SDK's extra_body does: the member's name and its value.
type BodyMember struct {
	Key   string
	Value any
}

// The members every request body starts with, in this order.
const (
	MemberState     = "state"
	memberModel     = "model"
	memberQuestions = "questions"
)

// content is the root package's Content seen through its methods: IsZero
// reports content that was never set, Text and JSON give its two forms, of
// which JSON is non-nil for a JSON object or array. The body encoder reaches
// Content only through them, since this package cannot name the root
// package's types; R is the root package's RawJSON, which JSON returns.
type content[R ~[]byte] interface {
	IsZero() bool
	Text() string
	JSON() R
}

// FailureKind is what kept encodeBody from building a body.
type FailureKind uint8

const (
	// FailNone: the body was built.
	FailNone FailureKind = iota
	// FailNoQuestions: the question set is nil or holds no question.
	FailNoQuestions
	// FailModel: the model is not valid UTF-8.
	FailModel
	// FailEncode: a member could not be encoded.
	FailEncode
)

// Failure is why encodeBody built no body, returned by value so that a
// successful call allocates nothing for it; the root package turns it into
// its public error: a *ConfigError for failNoQuestions and failModel, an
// *InvalidRequestError naming the member for failEncode.
type Failure struct {
	Kind FailureKind
	// Key is the member that could not be encoded: "state", or the Key of
	// an extra body member when extra is set.
	Key   string
	Extra bool
	Err   error
}

// EncodeBody encodes the request body of one System One call into a pooled
// scratch buffer. It returns the body holding the call's reference, which
// the caller drops with Release when the call returns.
//
// The body is the JSON object {"state":…,"model":…,"questions":…} followed
// by the members of extra, laid out as the Python SDK's {**body,
// **extra_body}: an extra member named "state", "model" or "questions"
// replaces that member's value where it stands, and the value it replaces is
// not encoded at all; any other name is appended in the order extra first
// names it. When extra names a member more than once, the last value is
// written.
//
// R and C are the root package's RawJSON and Content: the state, and an
// extra "state" too, must be text, a JSON object or an array: sonic writes
// it (codec.EncodeState); an R state, or the R a non-nil *R points to, is
// written as it is after codec.AppendRawState's check; a C is written as a
// question writes it. model is written as a JSON string and q's bytes as
// they are. Any other value may be any JSON value: sonic writes it
// (codec.EncodeValue), an R value as it is after codec.AppendRawValue's
// check, a C as a question writes it and an unset C as null. The float
// spelling, map order and Marshaler rules are the root package's
// EncodeBody's (rulings R46, R55, R59, R61).
//
// It fails, with a failure the root package turns into its error, when q is
// nil or holds no question, when model is not valid UTF-8, or when a member
// cannot be encoded. The configuration is checked first.
func EncodeBody[R ~[]byte, C content[R]](state any, model string, q *wire.Prepared, extra []BodyMember) (codec.Body, Failure) {
	if q == nil || len(q.Entries()) == 0 {
		return codec.Body{}, Failure{Kind: FailNoQuestions}
	}
	stateAt, modelAt, questionsAt := -1, -1, -1 // the last extra member that replaces each
	for i := range extra {
		switch extra[i].Key {
		case MemberState:
			stateAt = i
		case memberModel:
			modelAt = i
		case memberQuestions:
			questionsAt = i
		}
	}
	if modelAt < 0 && !utf8.ValidString(model) {
		return codec.Body{}, Failure{Kind: FailModel}
	}

	body := codec.NewBody()
	buf := body.Buffer()
	*buf = append(*buf, `{"state":`...)
	var f Failure
	if stateAt < 0 {
		if err := AppendState[R, C](buf, state); err != nil {
			f = Failure{Kind: FailEncode, Key: MemberState, Err: err}
		}
	} else if err := AppendState[R, C](buf, extra[stateAt].Value); err != nil {
		f = Failure{Kind: FailEncode, Key: MemberState, Extra: true, Err: err}
	}
	if f.Kind == FailNone {
		*buf = append(*buf, `,"model":`...)
		if modelAt < 0 {
			*buf, _ = wire.AppendString(*buf, model) // valid UTF-8: cannot fail
		} else if err := AppendValue[R, C](buf, extra[modelAt].Value); err != nil {
			f = Failure{Kind: FailEncode, Key: memberModel, Extra: true, Err: err}
		}
	}
	if f.Kind == FailNone {
		*buf = append(*buf, `,"questions":`...)
		if questionsAt < 0 {
			*buf = append(*buf, q.Questions...)
		} else if err := AppendValue[R, C](buf, extra[questionsAt].Value); err != nil {
			f = Failure{Kind: FailEncode, Key: memberQuestions, Extra: true, Err: err}
		}
	}
	if f.Kind == FailNone {
		f = appendExtra[R, C](buf, extra)
	}
	if f.Kind != FailNone {
		body.Release()
		return codec.Body{}, f
	}
	*buf = append(*buf, '}')
	return body, Failure{}
}

// appendExtra appends the extra members other than the three that replace a
// built-in member, each as ,"key":value: in the order extra first names a
// key, with the value extra gives it last.
func appendExtra[R ~[]byte, C content[R]](buf *[]byte, extra []BodyMember) Failure {
	var last map[string]int // key -> index of its last member, while unwritten; nil for short lists
	if len(extra) > RepeatScanLimit {
		last = make(map[string]int, len(extra))
		for i := range extra {
			last[extra[i].Key] = i
		}
	}
next:
	for i := range extra {
		key := extra[i].Key
		if key == MemberState || key == memberModel || key == memberQuestions {
			continue
		}
		j := i // the member whose value is written
		if last != nil {
			var unwritten bool
			if j, unwritten = last[key]; !unwritten {
				continue
			}
			delete(last, key)
		} else {
			for k := range i {
				if extra[k].Key == key {
					continue next // written at its first position
				}
			}
			for k := i + 1; k < len(extra); k++ {
				if extra[k].Key == key {
					j = k
				}
			}
		}
		*buf = append(*buf, ',')
		var err error
		if *buf, err = wire.AppendString(*buf, key); err != nil {
			return Failure{Kind: FailEncode, Key: key, Extra: true, Err: err}
		}
		*buf = append(*buf, ':')
		if err := AppendValue[R, C](buf, extra[j].Value); err != nil {
			return Failure{Kind: FailEncode, Key: key, Extra: true, Err: err}
		}
	}
	return Failure{}
}

// AppendState appends a request state: text, a JSON object or an array. R
// and C are the root package's RawJSON and Content (see encodeBody).
func AppendState[R ~[]byte, C content[R]](buf *[]byte, state any) error {
	switch v := state.(type) {
	case R:
		return codec.AppendRawState(buf, []byte(v))
	case *R:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrStateShape)
		}
		return codec.AppendRawState(buf, []byte(*v))
	case C:
		if v.IsZero() {
			return fmt.Errorf("unset Content encodes as null, %w", codec.ErrStateShape)
		}
		var err error
		*buf, err = wire.AppendContent(*buf, wire.Content{Text: v.Text(), JSON: []byte(v.JSON())})
		return err
	default:
		return codec.EncodeState(buf, state)
	}
}

// AppendValue appends any JSON value, for a body member other than the
// state. R and C are the root package's RawJSON and Content.
func AppendValue[R ~[]byte, C content[R]](buf *[]byte, value any) error {
	switch v := value.(type) {
	case R:
		return codec.AppendRawValue(buf, []byte(v))
	case *R:
		if v == nil {
			return fmt.Errorf("nil *RawJSON holds no JSON value, %w", codec.ErrRawValue)
		}
		return codec.AppendRawValue(buf, []byte(*v))
	case C:
		if v.IsZero() {
			*buf = append(*buf, "null"...)
			return nil
		}
		var err error
		*buf, err = wire.AppendContent(*buf, wire.Content{Text: v.Text(), JSON: []byte(v.JSON())})
		return err
	default:
		return codec.EncodeValue(buf, value)
	}
}
