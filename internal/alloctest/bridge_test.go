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

// The bridge from the root package's public types, aliases of
// internal/engine's, to the state and the stages the engine holds for them
// (design D2: the whole implementation in internal/engine, the root package
// a facade of aliases and wrappers).

// wireOf returns the bytes and tables of qs, or nil for a nil set.
func wireOf(qs *Prepared) *wire.Prepared { return engine.WireOf(qs) }

// responseOf returns a response holding res and no HTTP metadata.
func responseOf(res wire.SystemOneResult) *SystemOneResponse {
	r := new(SystemOneResponse)
	*engine.ResponseResultOf(r) = res
	return r
}

// encodeBody is the body encode a call makes (engine.EncodeBody).
func encodeBody(state any, model string, qs *Prepared, extra []engine.BodyMember) (codec.Body, error) {
	return engine.EncodeBody(state, model, qs, extra)
}

// decodeSystemOne is the decode of a System One body a call makes.
func decodeSystemOne(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r engine.HeaderRedactor, qs *Prepared, model string, dst *wire.SystemOneResult) error {
	return engine.DecodeSystemOne(ctx, logger, meta, endpoint, r, qs, model, dst)
}

// decodeSystemOneInto is decodeSystemOne with spare as the room for the
// answers.
func decodeSystemOneInto(ctx context.Context, logger *slog.Logger, meta *wire.ResponseMeta, endpoint string, r engine.HeaderRedactor, qs *Prepared, model string, dst *wire.SystemOneResult, spare []wire.AnswerEntry) error {
	return engine.DecodeSystemOneInto(ctx, logger, meta, endpoint, r, qs, model, dst, spare)
}

// callSettings is what one call sends besides its body: the header every
// attempt starts from and the deadline of each attempt.
type callSettings struct {
	header  http.Header
	timeout time.Duration
}
