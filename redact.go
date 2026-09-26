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
	"net/http"

	"github.com/zchee/typesafe-sdk-go/internal/engine"
)

// Header redaction lives in internal/engine; these names keep the root
// package's call sites short.

// redacted replaces each credential value in a log line, an error's header
// or a transport error's text.
const redacted = engine.Redacted

// minKeyNeedleBytes is the length below which the API key is not looked for
// in text (ruling R68).
const minKeyNeedleBytes = engine.MinKeyNeedleBytes

type (
	headerRedactor  = engine.HeaderRedactor
	redactedHeaders = engine.RedactedHeaders
)

func isSecretHeader(name string) bool { return engine.IsSecretHeader(name) }

func keyNeedle(key string) bool { return engine.KeyNeedle(key) }

func isCredential(name string, values []string, apiKey string) bool {
	return engine.IsCredential(name, values, apiKey)
}

func newHeaderRedactor(apiKey string) headerRedactor { return engine.NewHeaderRedactor(apiKey) }

func newRedactedHeaders(header http.Header, apiKey string) redactedHeaders {
	return engine.NewRedactedHeaders(header, apiKey)
}
