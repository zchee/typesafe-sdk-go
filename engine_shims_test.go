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

package typesafe

import (
	"github.com/zchee/typesafe-sdk-go/internal/engine"
)

// Names of internal/engine that the root package's tests use from before the
// W6.5 spike moved them; the execution wave moves these tests to
// internal/engine instead.

const (
	msgSkippedAnswer  = engine.MsgSkippedAnswer
	msgSkippedAnswers = engine.MsgSkippedAnswers
	maxDetailChars    = engine.MaxDetailChars
	maxChainErrors    = engine.MaxChainErrors
)

var secretHeaderNames = engine.SecretHeaderNames

type (
	scrubbedError = engine.ScrubbedError
)

func quotedForm(q string) string { return engine.QuotedForm(q) }

func jsonForm(v string) string { return engine.JSONForm(v) }

func hidesText(r rune) bool { return engine.HidesText(r) }
