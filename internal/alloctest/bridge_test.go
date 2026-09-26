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

package alloctest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	. "github.com/zchee/typesafe-sdk-go"
	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/engine"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// The bridge from the root package's public types to the state and the
// stages internal/engine holds for them. The root package declares Client,
// Prepared and SystemOneResponse as defined types over engine.Client,
// engine.Prepared and engine.Response, so each conversion below is free
// and compile-checked.

// engOf returns c's state.
func engOf(c *Client) *engine.Client { return (*engine.Client)(c) }

// wireOf returns the bytes and tables of qs, or nil for a nil set.
func wireOf(qs *Prepared) *wire.Prepared {
	if qs == nil {
		return nil
	}
	return (*engine.Prepared)(qs).Wire()
}

// responseOf returns a response holding res and no HTTP metadata.
func responseOf(res wire.SystemOneResult) *SystemOneResponse {
	r := new(SystemOneResponse)
	*(*engine.Response)(r).Result() = res
	return r
}

// encodeBody is the root package's body encode as a call makes it
// (engine.EncodeBody with the root package's RawJSON and Content); a
// failure is reported as an error of its kind, the member and the cause.
func encodeBody(state any, model string, qs *Prepared, extra []engine.BodyMember) (codec.Body, error) {
	body, f := engine.EncodeBody[RawJSON, Content](state, model, wireOf(qs), extra)
	if f.Kind != engine.FailNone {
		return codec.Body{}, &encodeFailure{f}
	}
	return body, nil
}

// encodeFailure is an engine.Failure as an error.
type encodeFailure struct{ f engine.Failure }

func (e *encodeFailure) Error() string {
	if e.f.Err != nil {
		return "encode " + e.f.Key + ": " + e.f.Err.Error()
	}
	return "encode failure kind " + string(rune('0'+e.f.Kind))
}

// decodeSystemOne is the root package's decode of a System One body as a
// call makes it (engine.DecodeSystemOne); endpoint and r name and redact
// the error a call would build, which the root package adds.
func decodeSystemOne(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, _ string, _ engine.HeaderRedactor, qs *Prepared, model string, dst *wire.SystemOneResult) error {
	return engine.DecodeSystemOne(ctx, logger, meta.Body, wireOf(qs), model, dst)
}

// decodeSystemOneInto is decodeSystemOne with spare as the room for the
// answers (engine.DecodeSystemOneInto).
func decodeSystemOneInto(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, _ string, _ engine.HeaderRedactor, qs *Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	return engine.DecodeSystemOneInto(ctx, logger, meta.Body, wireOf(qs), model, dst, spare)
}

// callSettings is what one call sends besides its body: the header every
// attempt starts from and the deadline of each attempt.
type callSettings struct {
	header  http.Header
	timeout time.Duration
}
