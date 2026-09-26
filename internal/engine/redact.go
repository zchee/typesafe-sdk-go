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
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// Redacted is what a credential is printed as.
const Redacted = "***"

// SecretHeaderNames are the header names, in lower case, whose values are
// credentials (py:_core/constants.py:21).
var SecretHeaderNames = []string{"authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "set-cookie"}

// IsSecretHeader reports whether the value of the header name is a
// credential by its name, compared without regard to case: one of
// secretHeaderNames, or a name that contains "token" or "secret"
// (py:_core/logging.py:32-34). The rule is strings.ToLower's: an ASCII
// name, as every name on the wire is (net/http refuses others), is
// compared with its letters folded in place, which allocates nothing,
// where lower-casing a mixed-case name such as "Content-Type" copies it
// (W5.3, from D-r103revert-rereview); a name with any other byte is
// lower-cased as before.
func IsSecretHeader(name string) bool {
	for i := range len(name) {
		if name[i] >= utf8.RuneSelf {
			lower := strings.ToLower(name)
			return slices.Contains(SecretHeaderNames, lower) || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
		}
	}
	for _, secret := range SecretHeaderNames {
		if strings.EqualFold(name, secret) {
			return true
		}
	}
	return containsFoldASCII(name, "token") || containsFoldASCII(name, "secret")
}

// containsFoldASCII reports whether the ASCII string s contains sub, which is
// lower case, with the letters of s compared in lower case.
func containsFoldASCII(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		j := 0
		for j < len(sub) && lowerASCII(s[i+j]) == sub[j] {
			j++
		}
		if j == len(sub) {
			return true
		}
	}
	return false
}

// lowerASCII returns c in lower case when it is an ASCII capital letter.
func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// MinKeyNeedleBytes is the shortest API key the SDK looks for inside other
// text: a header value it redacts, and a WithHeader name it refuses (ruling
// R68). A shorter key, such as a test's "test" or "k", occurs in ordinary
// names and values, and looking for it would hide or refuse them; real keys
// are far longer. Redaction by header name does not depend on the key and
// always applies.
const MinKeyNeedleBytes = 8

// KeyNeedle reports whether key is long enough to be looked for inside other
// text ([minKeyNeedleBytes]).
func KeyNeedle(key string) bool {
	return len(key) >= MinKeyNeedleBytes
}

// IsCredential reports whether the values of the header name must not be
// printed: the name marks them as credentials ([isSecretHeader]), or one of
// them holds the API key, under whatever name the caller sent it, when the
// key is at least [minKeyNeedleBytes] long. The second test goes past
// typesafe-sdk-python, which redacts by name alone.
func IsCredential(name string, values []string, apiKey string) bool {
	if IsSecretHeader(name) {
		return true
	}
	return KeyNeedle(apiKey) && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, apiKey) })
}

// HeaderRedactor redacts the response header that the error types keep
// (rulings R87, R93): a header is a credential by its name always, and by
// holding the client's API key when the key is at least
// [minKeyNeedleBytes] long ([isCredential]). Its zero value redacts by name
// alone, for an error built without a client's key, such as UnmarshalJSON's.
// Build a client's with [newHeaderRedactor] or [config.redactor].
type HeaderRedactor struct {
	// key is the API key when it is long enough to look for, else empty.
	key string
}

// NewHeaderRedactor returns the redactor for a client whose API key is
// apiKey: by name and by apiKey when apiKey is at least
// [minKeyNeedleBytes] long (ruling R68), by name alone otherwise.
func NewHeaderRedactor(apiKey string) HeaderRedactor {
	if !KeyNeedle(apiKey) {
		return HeaderRedactor{}
	}
	return HeaderRedactor{key: apiKey}
}

// Key returns the key r looks for, empty when it redacts by name alone.
func (r HeaderRedactor) Key() string { return r.key }

// redactor returns the redactor for the client c configures.

// Header returns h's headers in a new map in which every value of each
// Header that is a credential is replaced by "***", one "***" per value, and
// every other Header shares its value slice with h; nil stays nil. The error
// types that keep a response's Header store this map, so no rendering of
// them, and no caller that dumps their Header, shows a credential the server
// sent.
func (r HeaderRedactor) Header(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for name, values := range h {
		if !IsCredential(name, values, r.key) {
			out[name] = values
			continue
		}
		masked := make([]string, len(values))
		for i := range masked {
			masked[i] = Redacted
		}
		out[name] = masked
	}
	return out
}

// RequestID returns the request id in h as the error types show it from the
// header r redacted ([headerRedactor.header]): the x-typesafe-request-id
// values joined by ", ", each "***" when one of them holds the client's API
// key, and whether the header was present. The INFO "response" record reads
// the id through it, so the record shows "***" where Error does (ruling
// R87). The header's name is not a credential's ([isSecretHeader]), so only
// the key is looked for, and the name is not lower-cased as [isCredential]
// would. h is not copied: one value costs no allocation, and several cost
// the one string they are joined into.
func (r HeaderRedactor) RequestID(h http.Header) (string, bool) {
	values := h[wire.RequestIDHeader]
	if len(values) == 0 {
		return "", false
	}
	if r.key != "" && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, r.key) }) {
		return strings.Repeat(", "+Redacted, len(values))[len(", "):], true
	}
	return strings.Join(values, ", "), true
}

// RedactedHeaders is a header map as a log record shows it: a group with one
// attribute per header, in name order, whose value is the header's values
// joined by ", ", or "***" when they are a credential ([isCredential]). Build
// it with [newRedactedHeaders].
//
// It is a [slog.LogValuer], so the map is walked and its values joined only
// when a handler keeps the record. Boxing the value into an attribute still
// allocates, so a request path that must not allocate with debug records off
// checks [slog.Logger.Enabled] before it builds the attribute.
//
// No rendering prints the map or the key. Every fmt verb prints the redacted
// form ([RedactedHeaders.Format]), and so do an unresolved [slog.Value] and
// [slog.Attr], whose String methods print a LogValuer through fmt. The map
// and the key sit behind the one pointer field, so the two renderings fmt
// makes without calling Format, the %p verb and a RedactedHeaders held in an
// unexported field of another value, print an address.
type RedactedHeaders struct {
	p *headerLog
}

// headerLog is what a [redactedHeaders] renders.
type headerLog struct {
	header http.Header
	apiKey string
}

// NewRedactedHeaders returns header as a log record shows it, with any value
// that holds apiKey redacted whatever its header's name, when apiKey is at
// least [minKeyNeedleBytes] long.
func NewRedactedHeaders(header http.Header, apiKey string) RedactedHeaders {
	return RedactedHeaders{p: &headerLog{header: header, apiKey: apiKey}}
}

// LogValue returns the redacted headers as a group value; the zero
// redactedHeaders is an empty group.
func (r RedactedHeaders) LogValue() slog.Value {
	if r.p == nil {
		return slog.GroupValue()
	}
	attrs := make([]slog.Attr, 0, len(r.p.header))
	for _, name := range slices.Sorted(maps.Keys(r.p.header)) {
		values := r.p.header[name]
		value := Redacted
		if !IsCredential(name, values, r.p.apiKey) {
			value = strings.Join(values, ", ")
		}
		attrs = append(attrs, slog.String(name, value))
	}
	return slog.GroupValue(attrs...)
}

// Format writes the redacted form, LogValue().String(), such as
// "[Accept=application/json Authorization=***]", whatever the verb and its
// flags. A String method alone would not do: %d and %x bypass it and print
// the fields.
func (r RedactedHeaders) Format(f fmt.State, _ rune) {
	fmt.Fprint(f, r.LogValue().String())
}
