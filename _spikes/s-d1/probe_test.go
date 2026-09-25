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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
)

// recorder is an ast.Visitor that writes every event it receives, so a probe
// can show exactly what sonic hands a visitor.
type recorder struct{ sb strings.Builder }

func (r *recorder) OnNull() error       { r.sb.WriteString("null "); return nil }
func (r *recorder) OnBool(v bool) error { fmt.Fprintf(&r.sb, "bool(%v) ", v); return nil }

func (r *recorder) OnString(v string) error { fmt.Fprintf(&r.sb, "str(%q) ", v); return nil }

func (r *recorder) OnInt64(_ int64, n json.Number) error {
	fmt.Fprintf(&r.sb, "int(%s) ", n)
	return nil
}

func (r *recorder) OnFloat64(_ float64, n json.Number) error {
	fmt.Fprintf(&r.sb, "flt(%s) ", n)
	return nil
}
func (r *recorder) OnObjectBegin(int) error    { r.sb.WriteString("{ "); return nil }
func (r *recorder) OnObjectKey(k string) error { fmt.Fprintf(&r.sb, "key(%q) ", k); return nil }
func (r *recorder) OnObjectEnd() error         { r.sb.WriteString("} "); return nil }
func (r *recorder) OnArrayBegin(int) error     { r.sb.WriteString("[ "); return nil }
func (r *recorder) OnArrayEnd() error          { r.sb.WriteString("] "); return nil }

// primitiveVerdicts runs body through each sonic primitive a variant is built
// from and renders what each one says.
func primitiveVerdicts(body string) (events string, verdicts []string) {
	var rec recorder
	perr := ast.Preorder(body, &rec, &ast.VisitorOptions{OnlyNumber: true})
	verdicts = append(verdicts, "Preorder:"+verdict(perr))

	start, end := decoder.Skip([]byte(body))
	trailingOK := start >= 0 && strings.Trim(body[min(end, len(body)):], " \t\n\r") == ""
	verdicts = append(verdicts, fmt.Sprintf("Skip:(%d,%d) trail-ok=%v", start, end, trailingOK))

	verdicts = append(verdicts, fmt.Sprintf("ValidString:%v", sonic.ValidString(body)))

	var anyMap map[string]any
	verdicts = append(verdicts, "Unmarshal(map any):"+verdict(sonic.UnmarshalString(body, &anyMap)))

	var rawMap map[string]sonic.NoCopyRawMessage
	verdicts = append(verdicts, "Unmarshal(map raw):"+verdict(sonic.UnmarshalString(body, &rawMap)))

	strict := sonic.Config{ValidateString: true}.Froze()
	var anyMap2 map[string]any
	verdicts = append(verdicts, "ValidateString(map any):"+verdict(strict.UnmarshalFromString(body, &anyMap2)))
	var rawMap2 map[string]sonic.NoCopyRawMessage
	verdicts = append(verdicts, "ValidateString(map raw):"+verdict(strict.UnmarshalFromString(body, &rawMap2)))
	return rec.sb.String(), verdicts
}

func verdict(err error) string {
	if err == nil {
		return "accept"
	}
	return "REJECT"
}

// TestProbeR14 answers ruling R14 with evidence: what sonic v1.15.4's
// primitives do with raw control characters, escapes and number forms, and
// exactly which string a visitor's OnString receives.
func TestProbeR14(t *testing.T) {
	tests := map[string]string{
		"raw U+0001 in a value":          "{\"a\":\"x\x01y\"}",
		"raw U+0001 in a key":            "{\"a\x01\":1}",
		"raw U+001F in a value":          "{\"a\":\"x\x1fy\"}",
		"raw U+0000 in a value":          "{\"a\":\"x\x00y\"}",
		"raw TAB in a value":             "{\"a\":\"a\tb\"}",
		"raw LF in a value":              "{\"a\":\"a\nb\"}",
		"escaped \\n (a\\nb)":            `{"a":"a\nb"}`,
		"raw space (a b)":                `{"a":"a b"}`,
		"escaped \\u0001":                `{"a":"\u0001"}`,
		"escape plus raw U+0001":         "{\"a\":\"\\n\x01\"}",
		"lone surrogate \\ud800":         `{"a":"\ud800"}`,
		"invalid escape \\q":             `{"a":"\q"}`,
		"raw 0xFF in a value":            "{\"a\":\"\xff\"}",
		"leading zero 01":                `{"a":01}`,
		"negative leading zero -01":      `{"a":-01}`,
		"trailing dot 1.":                `{"a":1.}`,
		"leading dot .5":                 `{"a":.5}`,
		"bare exponent 1e":               `{"a":1e}`,
		"plus sign +1":                   `{"a":+1}`,
		"big exponent 1e400":             `{"a":1e400}`,
		"raw U+0001 between tokens":      "{\"a\":\x011}",
		"raw TAB between tokens":         "{\"a\":\t1}",
		"raw U+0001 in a skipped object": "{\"a\":{\"b\":\"x\x01y\"}}",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			events, verdicts := primitiveVerdicts(body)
			t.Logf("body=%q\n\tevents: %s\n\t%s", body, events, strings.Join(verdicts, "  "))
		})
	}
}

// TestProbePrimitivesOnFixtures renders the same primitive verdicts for every
// testdata fixture, so the ledger can show which primitive rejects which
// malformed body.
func TestProbePrimitivesOnFixtures(t *testing.T) {
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		body := testsupport.FixtureString(t, name)
		_, verdicts := primitiveVerdicts(body)
		t.Logf("%-40s %s", name, strings.Join(verdicts, "  "))
	}
}
