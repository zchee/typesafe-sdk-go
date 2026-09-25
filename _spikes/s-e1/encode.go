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

import (
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic/encoder"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
)

// Call encodes one state into buf, as the SDK's body builder does with the
// request state (plan 6.1.2).
type Call func(buf *[]byte) error

// NewCall returns kind's encode of v, a value from [Value].
func NewCall(kind Kind, v any) Call {
	switch kind {
	case KindString:
		s := v.(string)
		// s is converted to the any parameter here, on every call.
		return func(buf *[]byte) error { return encoder.EncodeInto(buf, s, 0) }
	case KindBoxed, KindMap, KindMapFlat, KindStruct:
		return func(buf *[]byte) error { return encoder.EncodeInto(buf, v, 0) }
	case KindRaw:
		raw := v.([]byte)
		return func(buf *[]byte) error {
			*buf = append(*buf, raw...)
			return nil
		}
	case KindRawMessage:
		var rm any = json.RawMessage(v.([]byte))
		return func(buf *[]byte) error { return encoder.EncodeInto(buf, rm, 0) }
	default:
		panic(fmt.Sprintf("se1: unknown kind %q", kind))
	}
}

// EncodeMap returns the encoding of v, for the raw kinds' bytes.
func EncodeMap(v any) []byte {
	b, err := encoder.Encode(v, 0)
	if err != nil {
		panic(err)
	}
	return b
}

// PooledCall is one request encode with the SDK's scratch: take a body from
// the codec pool, encode into it, drop the call's reference (which returns
// the scratch to the pool when its capacity is within the ceiling). It
// returns the encoded length and the scratch capacity after the encode.
func PooledCall(call Call) (n, capacity int, err error) {
	body := codec.NewBody()
	err = call(body.Buffer())
	n, capacity = body.Len(), cap(*body.Buffer())
	body.Release()
	return n, capacity, err
}
