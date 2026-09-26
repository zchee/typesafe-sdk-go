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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

// This file renders text the SDK did not write: a server's message, a name
// the server chose, a request id, a field path, another layer's error. Such
// text keeps its printable characters, non-ASCII included, but a control
// character, a byte that is not UTF-8 or a format character that reorders or
// hides the text around it is written as a Go escape, so that it cannot break
// a log line, recolour a terminal or disguise itself; and it is cut at a
// number of characters counted after escaping (NF7, rulings R58 and R58b).
// The rules are the Rust port's src/text.rs in Go escapes.

// The caps on text the SDK did not write, in characters counted after
// escaping (NF7): a name, such as an extra body member's, and a sentence
// another layer wrote, such as the encoder's.
const (
	MaxNameChars    = 128
	MaxMessageChars = 200
)

// QuotedName returns name between double quotes, escaped with its
// backslashes doubled, and cut at [maxNameChars].
func QuotedName(name string) string {
	b := make([]byte, 0, min(len(name), MaxNameChars)+8)
	b = append(b, '"')
	b = AppendSafeText(b, name, MaxNameChars, true)
	return string(append(b, '"'))
}

// AppendSafeText appends s, text the SDK did not write, to dst escaped and
// cut at limit characters, as the Rust port's src/text.rs renders such text
// (NF7). Printable characters, non-ASCII and U+FFFD included, are written as
// they are; a control character (C0, DEL, C1), a byte that is not UTF-8 and a
// format character that reorders or hides the text around it (hidesText)
// are written as Go escapes: \n, \r, \t, \x1b, \xff, \u2028. A backslash is
// doubled when double is set, for a name, so that a name spelling \x1b cannot
// read like one holding the byte; a sentence keeps its backslashes, which
// its own layer wrote. A cut never splits a character or an escape and is
// marked with U+2026.
func AppendSafeText(dst []byte, s string, limit int, double bool) []byte {
	const hex = "0123456789abcdef"
	var buf [16]byte
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		var esc []byte // the escape written for this character, if any
		switch {
		case r == utf8.RuneError && size == 1:
			esc = append(buf[:0], '\\', 'x', hex[s[i]>>4], hex[s[i]&0xf])
		case r == '\\':
			if double {
				esc = append(buf[:0], '\\', '\\')
			}
		case r < 0x20 || 0x7f <= r && r < 0xa0 || HidesText(r):
			q := strconv.AppendQuoteRune(buf[:0], r) // '\n', '\x1b', '\u2028'
			esc = q[1 : len(q)-1]
		}
		w := 1
		if esc != nil {
			w = len(esc)
		}
		if n+w > limit {
			return append(dst, "\u2026"...)
		}
		if esc != nil {
			dst = append(dst, esc...)
		} else {
			dst = append(dst, s[i:i+size]...)
		}
		n += w
		i += size
	}
	return dst
}

// HidesText reports whether r is a Unicode format character that reorders,
// joins or hides the text around it: the soft hyphen, the Arabic letter
// mark, the Mongolian vowel separator, the zero-width characters, the line
// and paragraph separators, the bidirectional embeddings, overrides and
// isolates, the byte-order mark, the interlinear annotation marks and the
// invisible tag characters (the Rust port's hides_text).
func HidesText(r rune) bool {
	switch {
	case r == 0x00ad, r == 0x061c, r == 0x180e, r == 0xfeff:
		return true
	case 0x200b <= r && r <= 0x200f, 0x2028 <= r && r <= 0x202e, 0x2060 <= r && r <= 0x206f:
		return true
	case 0xfff9 <= r && r <= 0xfffb, 0xe0000 <= r && r <= 0xe007f:
		return true
	}
	return false
}

// MaxPathChars caps a whole field path in characters, counted after
// escaping: it holds the deepest path of the response schema,
// answers.<name>.probabilities.<key>, with both server-chosen names at
// [maxNameChars] (the Rust port's MAX_PATH_CHARS).
const MaxPathChars = 320

// SafeName returns name, which the server chose, escaped with its
// backslashes doubled and cut at [maxNameChars], without quotes.
func SafeName(name string) string {
	return string(AppendSafeText(make([]byte, 0, min(len(name), MaxNameChars)+4), name, MaxNameChars, true))
}

// SafeMessage returns s, a sentence the SDK did not write, escaped with its
// backslashes kept and cut at [maxMessageChars].
func SafeMessage(s string) string {
	return string(AppendSafeText(make([]byte, 0, min(len(s), MaxMessageChars)+4), s, MaxMessageChars, false))
}

// Credentials are the values a transport error's text must not show: the
// Credentials of the request that failed, each in the forms an error text
// may quote it in, longest first. Build them with [requestCredentials].
//
// A transport's error text is written by code the SDK does not control (a
// caller's RoundTripper or dialer, a proxy, net/http), which may repeat a
// header value, and it reaches the SDK error's message, its log record and
// the chain errors.Unwrap walks (Appendix B: "Transport error text verbatim
// → escaped, cut at 200, Credentials ***"; note N-W2.5).
type Credentials []string

// RequestCredentials returns the credentials of a request with header h, as
// typesafe-sdk-python collects them (py:_core/logging.py:43-51): the value of
// every header whose name marks a credential ([isSecretHeader]), and the
// credential after the scheme of an Authorization or Proxy-Authorization
// value, which for the SDK's own Authorization is the API key. Each is
// looked for as it is, as Go's %q and %+q quote it, and as encoding/json
// escapes it: the Go analogues of the Python SDK's raw, repr and json.dumps
// forms, which differ from them above the Basic Multilingual Plane and on
// '<', '>' and '&'. A value shorter than [minKeyNeedleBytes] is not looked
// for, as the API key is not (ruling R68): it would match ordinary text; the
// Python SDK looks for every value.
func RequestCredentials(h http.Header) Credentials {
	var c Credentials
	add := func(v string) {
		if !KeyNeedle(v) {
			return
		}
		for _, form := range [...]string{v, QuotedForm(strconv.Quote(v)), QuotedForm(strconv.QuoteToASCII(v)), JSONForm(v)} {
			if !slices.Contains(c, form) {
				c = append(c, form)
			}
		}
	}
	for name, values := range h {
		if !IsSecretHeader(name) {
			continue
		}
		scheme := strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization")
		for _, v := range values {
			add(v)
			if cred, ok := afterScheme(v); scheme && ok {
				add(cred)
			}
		}
	}
	// A whole value is replaced before a credential inside it; equal lengths
	// in a fixed order, so the result does not depend on map order.
	slices.SortFunc(c, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	return c
}

// afterScheme returns what follows the scheme of an authorization value,
// "<scheme> <credential>", as Python's value.split(maxsplit=1) takes it
// (py:_core/logging.py:45-48), and false when there is nothing after it.
func afterScheme(v string) (string, bool) {
	v = strings.TrimLeft(v, " \t")
	i := strings.IndexAny(v, " \t")
	if i < 0 {
		return "", false
	}
	cred := strings.TrimLeft(v[i:], " \t")
	return cred, cred != ""
}

// QuotedForm returns q, a Go-quoted string, without its quotes.
func QuotedForm(q string) string { return q[1 : len(q)-1] }

// JSONForm returns v as a JSON string holds it, without its quotes, escaped
// as encoding/json escapes it by default on Go 1.27: '"' and '\\' with a
// backslash, the controls, '<', '>', '&', U+2028 and U+2029 as \u escapes
// (\n, \r and \t as those), and a byte that is not UTF-8 as U+FFFD itself,
// unescaped. TestJSONFormMatchesEncodingJSON checks it against
// encoding/json, whose spelling of that last case differs between
// encoders.
func JSONForm(v string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(v); {
		r, size := utf8.DecodeRuneInString(v[i:])
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(r))
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == utf8.RuneError && size == 1:
			b.WriteString("\ufffd") // U+FFFD itself, unescaped
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			b.WriteString(`\u`)
			for shift := 12; shift >= 0; shift -= 4 {
				b.WriteByte(hex[r>>shift&0xf])
			}
		default:
			b.WriteString(v[i : i+size])
		}
		i += size
	}
	return b.String()
}

// Redact returns s with every credential replaced by [redacted], then the
// userinfo of every URL in it ([scrubUserinfo]), which may hold a proxy's
// password, and reports whether it replaced anything.
func (c Credentials) Redact(s string) (string, bool) {
	found := false
	for _, v := range c {
		if strings.Contains(s, v) {
			s, found = strings.ReplaceAll(s, v, Redacted), true
		}
	}
	if u, ok := ScrubUserinfo(s); ok {
		s, found = u, true
	}
	return s, found
}

// LogErrorText renders err, an error of the SDK's transport for the request
// req, for the transport's DEBUG records "h2: gate error" and "h2: redial
// error" (h2gate.Config.ErrorText, ruling R84): every credential of req's
// header and every URL userinfo replaced by "***" ([credentials.redact]),
// then escaped and cut at 200 characters ([safeMessage]), as the text of a
// *ConnectionError is. The transport calls it only for a record the logger
// keeps.
func LogErrorText(req *http.Request, err error) string {
	text, _ := RequestCredentials(req.Header).Redact(err.Error())
	return SafeMessage(text)
}

// MaxChainErrors bounds the errors [credentials.cause] reads in a chain; a
// longer chain is treated as holding a credential.
const MaxChainErrors = 64

// Cause returns the error an SDK error made from the transport's error err
// unwraps to: err itself, unless a printed form of err or of an error its
// chain wraps (errors.Unwrap, both forms) holds a credential
// ([credentials.printed]), in which case it returns a [scrubbedError]
// standing in for err, so that no printed form of the SDK error or of
// anything it unwraps to shows the credential. The stand-in keeps err's
// text and the rendering of its chain ([credentials.detail]), each with its
// credentials replaced.
//
// An error whose fields point to the request, such as a caller's error type
// that keeps the *http.Request, is kept: fmt prints a pointer inside a value
// as an address, so no printed form shows the credential, and errors.As
// reaches the error with the request the caller's own RoundTripper, dialer
// or body gave it (review W2.5 MINOR 2, ruling R82).
func (c Credentials) Cause(err error) error {
	if err == nil || !c.inChain(err) {
		return err
	}
	msg, _ := c.Redact(err.Error())
	s := &ScrubbedError{msg: msg, detail: c.detail(err)}
	for _, sentinel := range causeSentinels {
		if errors.Is(err, sentinel) {
			s.sentinels = append(s.sentinels, sentinel)
		}
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		s.sentinels = append(s.sentinels, errno)
	}
	return s
}

// MaxDetailChars caps a [scrubbedError]'s rendering of the chain it stands
// in for, in characters counted after escaping: one line holding the
// transport error's text and the text of each cause it wraps, which a
// sentence's 200 characters would cut before the first cause.
const MaxDetailChars = 1024

// detail returns the rendering of err's chain that a [scrubbedError] prints
// for %+v and %#v (ruling R95): the %+v form of err, then that of each error
// its chain wraps (errors.Unwrap, both forms, depth first, at most
// [maxChainErrors]) whose text the rendering does not already hold, joined
// by ": ", with every credential replaced by "***" ([credentials.redact]),
// then escaped, a line break as \n, and cut at [maxDetailChars]; the
// credentials are replaced before the cut. So a cause the transport's text
// does not print, such as one only errors.Unwrap reaches, still shows, as
// the Python SDK's rebuilt chain does, but as text. It runs on the error
// path only.
func (c Credentials) detail(err error) string {
	var b strings.Builder
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < MaxChainErrors; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if text := fmt.Sprintf("%+v", e); !strings.Contains(b.String(), text) {
			if b.Len() > 0 {
				b.WriteString(": ")
			}
			b.WriteString(text)
		}
		switch u := e.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			stack = append(stack, u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, w := range slices.Backward(u.Unwrap()) {
				stack = append(stack, w)
			}
		}
	}
	text, _ := c.Redact(b.String())
	return string(AppendSafeText(make([]byte, 0, min(len(text), MaxDetailChars)+4), text, MaxDetailChars, false))
}

// inChain reports whether a printed form of err, or of an error its chain
// wraps, holds a credential ([credentials.printed]); a chain of more than
// [maxChainErrors] errors counts as holding one.
func (c Credentials) inChain(err error) bool {
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < MaxChainErrors; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if c.printed(e) {
			return true
		}
		switch u := e.(type) { //nolint:errorlint // visits each link of the chain as it is; errors.As would skip links.
		case interface{ Unwrap() error }:
			stack = append(stack, u.Unwrap())
		case interface{ Unwrap() []error }:
			stack = append(stack, u.Unwrap()...)
		}
	}
	return len(stack) > 0
}

// printed reports whether a form in which e can be printed holds a
// credential: its text; %+v, which an fmt.Formatter may widen beyond the
// text; and %#v, which shows the fields of an error held by value, a
// request header among them. It runs on the error path only.
func (c Credentials) printed(e error) bool {
	for _, form := range [...]string{e.Error(), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e)} {
		if _, found := c.Redact(form); found {
			return true
		}
	}
	return false
}

// causeSentinels are the errors a [scrubbedError] still matches with
// errors.Is when the transport's error did: the ends of a deadline, a
// cancellation, a connection or a stream a caller may branch on.
var causeSentinels = [...]error{context.DeadlineExceeded, context.Canceled, os.ErrDeadlineExceeded, io.ErrUnexpectedEOF, io.EOF, net.ErrClosed}

// ScrubbedError stands in for a transport error whose chain printed a
// credential of the request (Appendix B: "cause via errors.Unwrap unless it
// printed a credential"). Its text is the transport error's with every
// credential replaced by "***"; it unwraps to the [causeSentinels] and the
// [syscall.Errno] the transport error matched, so errors.Is(err,
// context.DeadlineExceeded) or errors.Is(err, syscall.ECONNRESET) still
// answers as it would have, and to nothing else: errors.As cannot reach the
// transport's error or any value inside it. %+v and %#v print the
// rendering of the transport error's chain with its credentials replaced
// ([credentials.detail]), so a cause's diagnostic survives as text; every
// other verb prints the text, as for any error.
type ScrubbedError struct {
	msg       string
	detail    string
	sentinels []error
}

// Error returns the transport error's text with its credentials replaced.
func (e *ScrubbedError) Error() string { return e.msg }

// Format writes the rendering of the transport error's chain for %+v and
// %#v, and the text, under the verb and its flags, for every other verb.
func (e *ScrubbedError) Format(f fmt.State, verb rune) {
	if verb == 'v' && (f.Flag('+') || f.Flag('#')) {
		_, _ = io.WriteString(f, e.detail)
		return
	}
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), e.msg)
}

// Unwrap returns the sentinels the transport's error matched.
func (e *ScrubbedError) Unwrap() []error { return e.sentinels }

// ScrubUserinfo replaces the userinfo of every URL in s ("scheme://user@" or
// "scheme://user:password@") with "***", and reports whether it replaced
// any. A URL's authority is taken to run to the next whitespace or quote, not
// to the next "/": a password written with a raw "/" is still scrubbed, at
// the cost of scrubbing a path that holds an "@".
func ScrubUserinfo(s string) (string, bool) {
	var b strings.Builder
	rest, scrubbed := s, false
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			break
		}
		b.WriteString(rest[:i+3])
		rest = rest[i+3:]
		end := strings.IndexAny(rest, " \t\n\"'<>")
		if end < 0 {
			end = len(rest)
		}
		if at := strings.LastIndexByte(rest[:end], '@'); at >= 0 {
			b.WriteString("***")
			rest, scrubbed = rest[at:], true
		}
	}
	if !scrubbed {
		return s, false
	}
	b.WriteString(rest)
	return b.String(), true
}

// shield wraps a caller's httptrace hooks for one request (K28, K28b, K28c):
// a hook that panics inside net/http, which may hold its connection pool's
// lock or a reserved stream there, is recovered in place, and done raises the
// panic again on the goroutine that called RoundTrip. A panic it does not
// raise is logged at WARN with its hook and stack, never its value, which
