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

// Package engine holds the stages of a call that the root package's public
// types wrap: the request body's assembly, the response body's read, the
// decode's entry, header redaction and the credential scrub, and the state
// a client, a question set and a response keep behind their public types.
// It imports neither the root package nor anything that imports it; the
// root package declares each public type whose state lives here as a
// defined type over this package's type (type Client engine.Client), so a
// value converts between the two for free and each keeps its own methods.
package engine
