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
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/typesafe-sdk-go/internal/testsupport"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// wantReject maps every fixture a decoder must refuse to the field path the
// Go column of testdata/README.md names ("." for the JSON layer).
var wantReject = map[string]string{
	"malformed-empty.json":              ".",
	"malformed-whitespace.json":         ".",
	"malformed-truncated.json":          ".",
	"malformed-trailing-garbage.json":   ".",
	"malformed-trailing-value.json":     ".",
	"malformed-trailing-nbsp.json":      ".",
	"malformed-trailing-formfeed.json":  ".",
	"malformed-root-array.json":         ".",
	"malformed-invalid-utf8.json":       ".",
	"malformed-control-char.json":       ".",
	"malformed-control-char-key.json":   ".",
	"malformed-invalid-utf8-key.json":   ".",
	"malformed-invalid-escape.json":     ".",
	"malformed-bad-literal.json":        ".",
	"malformed-double-comma.json":       ".",
	"malformed-leading-zero.json":       ".",
	"malformed-trailing-comma.json":     ".",
	"malformed-big-exp.json":            "usage.input_tokens",
	"malformed-usage-type.json":         "usage.input_tokens",
	"malformed-missing-model.json":      "model",
	"malformed-missing-usage.json":      "usage",
	"malformed-answers-not-object.json": "answers",
	"deviation-big-exp-noul.json":       "answers.spam.noul",
}

// outcome is one variant's verdict on one fixture.
type outcome struct {
	accepted bool
	path     string // the refusal's field path
	err      error
}

func (o outcome) String() string {
	if o.accepted {
		return "accept"
	}
	return "reject@" + o.path
}

func decodeFixture(t *testing.T, d *Decoder, v Variant, name string) (Response, outcome) {
	t.Helper()
	body := testsupport.Fixture(t, name)
	var res Response
	var err error
	if name == "models.json" {
		var mres ModelsResponse
		err = d.DecodeModelsInto(v, body, &mres)
	} else {
		err = d.DecodeInto(v, body, &res)
	}
	if err == nil {
		return res, outcome{accepted: true}
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("%s %s: error %v is not a *ValidationError", v, name, err)
	}
	return res, outcome{path: ve.Path, err: err}
}

// knownGateFailures pins the evidence that disqualifies a variant: the
// fixtures on which it gives the wrong verdict, per GOARCH. Variant B's
// sonic.UnmarshalString into NoCopyRawMessage refuses 1e400 anywhere on
// arm64 ("float infinity") and accepts it on amd64, so on arm64 it refuses a
// body both Python and the plan accept, and it reports the JSON layer (".")
// for 1e400 in a known member; its verdicts depend on the architecture.
var knownGateFailures = map[string]map[Variant]map[string]string{
	"arm64": {
		VariantB: {
			"parity-big-exp-unknown.json": "reject@.",
			"malformed-big-exp.json":      "reject@.",
			"deviation-big-exp-noul.json": "reject@.",
		},
	},
}[runtime.GOARCH]

// TestValidityGate is S-D1's first criterion: a variant must refuse every
// malformed-*.json with the README's field path, accept every other fixture
// (deviation-*.json per Appendix B), and the log is the validity matrix.
func TestValidityGate(t *testing.T) {
	names := testsupport.FixtureNames(t, "*.json")
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%-42s %-26s %-26s %-26s %-26s\n", "fixture", "want", "a1", "a2", "b")
	for _, name := range names {
		want := "accept"
		if p, ok := wantReject[name]; ok {
			want = "reject@" + p
		}
		row := []string{want}
		for _, v := range Variants {
			d := NewDecoder()
			_, got := decodeFixture(t, d, v, name)
			row = append(row, got.String())
			mismatch := got.String() != want
			known, isKnown := knownGateFailures[v][name]
			switch {
			case mismatch && isKnown && known == got.String():
				// pinned evidence of a disqualified variant
			case mismatch:
				t.Errorf("%s %s: got %s (%v), want %s", v, name, got, got.err, want)
			case isKnown:
				t.Errorf("%s %s: pinned gate failure %s no longer happens; update knownGateFailures", v, name, known)
			}
		}
		fmt.Fprintf(&sb, "%-42s %-26s %-26s %-26s %-26s\n", name, row[0], row[1], row[2], row[3])
	}
	t.Log(sb.String())
}

// TestDecodedValues checks what the decoders return, not only whether they
// accept: wire order, escapes, exact legend bytes, unknown types dropped,
// and the same result from every variant.
func TestDecodedValues(t *testing.T) {
	floodLevel := func(i int) string {
		if i%2 == 0 {
			return `{"summary":"level ` + strconv.Itoa(i) + `","examples":["example ` + strconv.Itoa(i) + `"]}`
		}
		return `["level ` + strconv.Itoa(i) + `",{"note":null}]`
	}
	tests := map[string]struct {
		fixture string
		check   func(t *testing.T, res *Response, st Stats)
	}{
		"success: result.json in wire order": {
			fixture: "result.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				if diff := gocmp.Diff([]string{"spam", "tone", "quality"}, answerNames(res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
				q, _ := res.Answers.Get("quality")
				if diff := gocmp.Diff([]wire.LegendEntry{
					{Level: 0, Description: wire.Content{Text: "bad"}},
					{Level: 1, Description: wire.Content{Text: "ok"}},
					{Level: 2, Description: wire.Content{Text: "great"}},
				}, q.Score.Legend); diff != "" {
					t.Errorf("legend (-want +got):\n%s", diff)
				}
			},
		},
		"success: type-last.json equals result.json": {
			fixture: "type-last.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				var want Response
				if err := NewDecoder().DecodeInto(VariantA1, testsupport.Fixture(t, "result.json"), &want); err != nil {
					t.Fatal(err)
				}
				if diff := gocmp.Diff(want.Answers.Entries(), res.Answers.Entries()); diff != "" {
					t.Errorf("answers (-result.json +type-last.json):\n%s", diff)
				}
				if res.Model != want.Model || res.Usage != want.Usage {
					t.Errorf("model/usage = %q %+v, want %q %+v", res.Model, res.Usage, want.Model, want.Usage)
				}
			},
		},
		"success: escaped names decode": {
			fixture: "escaped-names.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				want := []string{"spécial", `quote"d`, `back\slash`, "new\nline", "globe 🌍", "sl/ash"}
				if diff := gocmp.Diff(want, answerNames(res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
			},
		},
		"success: escaped member names keep the level's escapes": {
			fixture: "escaped-member-names.json",
			check: func(t *testing.T, res *Response, st Stats) {
				if res.Model != "jev-latest" || res.Usage.InputTokens != 12 {
					t.Errorf("model/usage = %q %+v", res.Model, res.Usage)
				}
				r, _ := res.Answers.Get("risk")
				want := `{"summ\u0061ry":"duplicated","examples":["charged \"twice\""]}`
				if got := string(r.Score.Legend[0].Description.JSON); got != want {
					t.Errorf("level 0 = %s, want %s", got, want)
				}
				if st.LazyPasses != 1 || st.Structured != 1 {
					t.Errorf("stats = %+v, want one lazy pass filling one level", st)
				}
			},
		},
		"success: structured legend bytes": {
			fixture: "structured-legend.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				r, _ := res.Answers.Get("risk")
				if got, want := string(r.Score.Legend[0].Description.JSON), `{"summary":"duplicated","examples":["charged twice"]}`; got != want {
					t.Errorf("level 0 = %s, want %s", got, want)
				}
			},
		},
		"success: lone surrogate is U+FFFD in text and kept in Raw()": {
			fixture: "deviation-lone-surrogate.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				q, _ := res.Answers.Get("quality")
				if got := q.Score.Legend[0].Description.Text; got != "bad \uFFFD" {
					t.Errorf("text level = %q", got)
				}
				if got := string(q.Score.Legend[1].Description.JSON); got != `{"note":"\ud800"}` {
					t.Errorf("structured level = %s", got)
				}
			},
		},
		"success: unknown answer type dropped": {
			fixture: "unknown-answer-type.json",
			check: func(t *testing.T, res *Response, st Stats) {
				if diff := gocmp.Diff([]string{"spam"}, answerNames(res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
				if st.Warns != 1 {
					t.Errorf("warns = %d, want 1", st.Warns)
				}
			},
		},
		"success: no answers member": {
			fixture: "no-answers.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				if res.Answers.Len() != 0 || res.Model != "jev-latest" {
					t.Errorf("got %d answers, model %q", res.Answers.Len(), res.Model)
				}
			},
		},
		"success: 20 answers in wire order": {
			fixture: "result-20.json",
			check: func(t *testing.T, res *Response, _ Stats) {
				if res.Answers.Len() != 20 {
					t.Errorf("got %d answers, want 20", res.Answers.Len())
				}
			},
		},
		"success: 1k structured flood, exact bytes per level": {
			fixture: "structured-legend-flood-1k.json",
			check: func(t *testing.T, res *Response, st Stats) {
				f, _ := res.Answers.Get(testsupport.FloodAnswer)
				if len(f.Score.Legend) != 1000 || len(f.Score.Probabilities) != 1000 {
					t.Fatalf("legend %d, probabilities %d, want 1000 each", len(f.Score.Legend), len(f.Score.Probabilities))
				}
				for i, l := range f.Score.Legend {
					if l.Level != uint32(i) || string(l.Description.JSON) != floodLevel(i) {
						t.Fatalf("level %d = %d %s, want %s", i, l.Level, l.Description.JSON, floodLevel(i))
					}
				}
				if st.Structured != 1000 {
					t.Errorf("structured = %d, want 1000", st.Structured)
				}
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var first *Response
			for _, v := range Variants {
				d := NewDecoder()
				var res Response
				if err := d.DecodeInto(v, testsupport.Fixture(t, tt.fixture), &res); err != nil {
					t.Fatalf("%s: %v", v, err)
				}
				t.Run(v.String(), func(t *testing.T) { tt.check(t, &res, d.Stats()) })
				if first == nil {
					first = &res
					continue
				}
				if diff := gocmp.Diff(first.Answers.Entries(), res.Answers.Entries()); diff != "" {
					t.Errorf("%s differs from a1 (-a1 +%s):\n%s", v, v, diff)
				}
			}
		})
	}
}

func answerNames(res *Response) []string {
	var names []string
	for _, e := range res.Answers.Entries() {
		names = append(names, e.Name)
	}
	return names
}

// TestLastWins checks plan 6.2.4's one duplicate rule at every level: the
// last occurrence wins, a repeated answer name keeps its first position, and
// the lazy pass picks the same occurrence as the visitor.
func TestLastWins(t *testing.T) {
	const head = `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},`
	noul := func(n string) string { return `{"type":"noul","noul":` + n + `}` }
	score := func(legend string) string {
		return `{"type":"score","score":0,"confidence":1,"legend":` + legend + `,"probabilities":{"0":1}}`
	}
	type want struct {
		names  []string
		model  string
		input  uint64
		noul   map[string]float64
		legend map[string][]wire.LegendEntry
	}
	tests := map[string]struct {
		body string
		want want
	}{
		"success: repeated answers member resets every answer": {
			body: head + `"answers":{"x":` + noul("0.1") + `},"answers":{"y":` + noul("0.2") + `}}`,
			want: want{names: []string{"y"}, noul: map[string]float64{"y": 0.2}},
		},
		"success: repeated name keeps its first position and takes the last value": {
			body: head + `"answers":{"a":` + noul("0.1") + `,"b":` + noul("0.2") + `,"a":` + noul("0.3") + `}}`,
			want: want{names: []string{"a", "b"}, noul: map[string]float64{"a": 0.3, "b": 0.2}},
		},
		"success: repeated type member overwrites": {
			body: head + `"answers":{"a":{"type":"choice","noul":0.5,"type":"noul"}}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 0.5}},
		},
		"success: repeated model and usage overwrite": {
			body: `{"model":"m1","usage":{"input_tokens":1,"output_tokens":1},"model":"m2","usage":{"input_tokens":7,"output_tokens":1},"answers":{}}`,
			want: want{names: nil, model: "m2", input: 7},
		},
		"success: a duplicate repairs an invalid answer": {
			body: head + `"answers":{"a":{"type":"noul"},"a":` + noul("1") + `}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 1}},
		},
		"success: a duplicate repairs an invalid answers member": {
			body: head + `"answers":[],"answers":{"a":` + noul("1") + `}}`,
			want: want{names: []string{"a"}, noul: map[string]float64{"a": 1}},
		},
		"success: a duplicate unknown type drops the answer": {
			body: head + `"answers":{"a":` + noul("1") + `,"b":` + noul("1") + `,"a":{"type":"aurora"}}}`,
			want: want{names: []string{"b"}, noul: map[string]float64{"b": 1}},
		},
		"success: repeated text level keeps its first position": {
			body: head + `"answers":{"s":` + score(`{"0":"a","1":"b","0":"c"}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{Text: "c"}},
				{Level: 1, Description: wire.Content{Text: "b"}},
			}}},
		},
		"success: repeated structured level takes the last bytes": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1},"1":"t","0":{"v":2}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":2}`)}},
				{Level: 1, Description: wire.Content{Text: "t"}},
			}}},
		},
		"success: structured level superseded by text": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1},"0":"t"}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{Text: "t"}},
			}}},
		},
		"success: text level superseded by structured": {
			body: head + `"answers":{"s":` + score(`{"0":"t","0":[1]}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`[1]`)}},
			}}},
		},
		"success: level keys 1 and 01 are one level": {
			body: head + `"answers":{"s":` + score(`{"1":{"v":1},"01":{"v":2}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 1, Description: wire.Content{JSON: []byte(`{"v":2}`)}},
			}}},
		},
		"success: repeated legend member takes the last": {
			body: head + `"answers":{"s":{"type":"score","score":0,"confidence":1,"legend":{"0":{"v":1}},"probabilities":{"0":1},"legend":{"0":{"v":9}}}}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":9}`)}},
			}}},
		},
		"success: lazy pass reads the last occurrence of a repeated answer": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1}}`) + `,"t":` + noul("0") + `,"s":` + score(`{"0":{"v":3}}`) + `}}`,
			want: want{names: []string{"s", "t"}, noul: map[string]float64{"t": 0}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":3}`)}},
			}}},
		},
		"success: lazy pass reads the last answers member, escaped or not": {
			body: head + `"answers":{"s":` + score(`{"0":{"v":1}}`) + `},"\u0061nswers":{"s":` + score(`{"0":{"v":4}}`) + `}}`,
			want: want{names: []string{"s"}, legend: map[string][]wire.LegendEntry{"s": {
				{Level: 0, Description: wire.Content{JSON: []byte(`{"v":4}`)}},
			}}},
		},
	}
	for name, tt := range tests {
		for _, v := range Variants {
			t.Run(name+"/"+v.String(), func(t *testing.T) {
				var res Response
				if err := NewDecoder().DecodeInto(v, []byte(tt.body), &res); err != nil {
					t.Fatal(err)
				}
				if diff := gocmp.Diff(tt.want.names, answerNames(&res)); diff != "" {
					t.Errorf("names (-want +got):\n%s", diff)
				}
				if tt.want.model != "" && (res.Model != tt.want.model || res.Usage.InputTokens != tt.want.input) {
					t.Errorf("model %q input %d, want %q %d", res.Model, res.Usage.InputTokens, tt.want.model, tt.want.input)
				}
				for n, f := range tt.want.noul {
					if a, _ := res.Answers.Get(n); a.Noul.Noul != f {
						t.Errorf("%s.noul = %v, want %v", n, a.Noul.Noul, f)
					}
				}
				for n, l := range tt.want.legend {
					a, _ := res.Answers.Get(n)
					if diff := gocmp.Diff(l, a.Score.Legend); diff != "" {
						t.Errorf("%s.legend (-want +got):\n%s", n, diff)
					}
				}
			})
		}
	}
}

// TestControlRule checks the R14 rule the visitor implements: a raw control
// character anywhere inside a string is refused, one that came from an
// escape is accepted, and the whole-body scan runs only when a delivered
// string holds a control character.
func TestControlRule(t *testing.T) {
	const head = `{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"s":{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":`
	tests := map[string]struct {
		body      string
		accept    bool
		wantScans int // for a1 and a2
	}{
		"success: escaped newline in a text level":        {body: head + `"a\nb"}}}}`, accept: true, wantScans: 1},
		"success: escaped U+0001 in a text level":         {body: head + `"a\u0001b"}}}}`, accept: true, wantScans: 1},
		"success: raw LF between tokens, escape in value": {body: head + "\n\"a\\nb\"}}}}\n", accept: true, wantScans: 1},
		"success: plain text, no scan":                    {body: head + `"ab"}}}}`, accept: true, wantScans: 0},
		"error: raw U+0001 in a text level":               {body: head + "\"a\x01b\"}}}}"},
		"error: raw TAB in a text level":                  {body: head + "\"a\tb\"}}}}"},
		"error: escape and raw U+0001 in one string":      {body: head + "\"\\n\x01\"}}}}"},
		"error: raw U+001F in an unknown member's key":    {body: `{"model":"m","usage":{},"meta":{"k` + "\x1f" + `":1},"answers":{}}`},
		"error: raw U+0000 inside a structured level":     {body: head + "{\"x\":\"\x00\"}}}}}"},
	}
	for name, tt := range tests {
		for _, v := range append(slices.Clone(Variants), variantA1Fast) {
			t.Run(name+"/"+variantName(v), func(t *testing.T) {
				d := NewDecoder()
				if v == variantA1Fast {
					v, d.v.fastCheck = VariantA1, true
				}
				var res Response
				err := d.DecodeInto(v, []byte(tt.body), &res)
				if tt.accept != (err == nil) {
					t.Fatalf("accept = %v (err %v), want %v", err == nil, err, tt.accept)
				}
				var ve *ValidationError
				if err != nil && (!errors.As(err, &ve) || ve.Path != ".") {
					t.Errorf("error %v, want a *ValidationError at .", err)
				}
				if tt.accept && d.Stats().BodyScans != tt.wantScans {
					t.Errorf("body scans = %d, want %d", d.Stats().BodyScans, tt.wantScans)
				}
			})
		}
	}
}

// TestLinearity checks AC-P8's time bound on the structured-legend floods:
// the 10k decode takes less than 15 times the 1k decode (minimum of 21 runs
// each, which filters scheduler noise without a benchmark harness).
func TestLinearity(t *testing.T) {
	small := testsupport.Fixture(t, "structured-legend-flood-1k.json")
	large := testsupport.Fixture(t, "structured-legend-flood-10k.json")
	minTime := func(d *Decoder, v Variant, body []byte) time.Duration {
		runs := make([]time.Duration, 21)
		for i := range runs {
			var res Response
			start := time.Now()
			if err := d.DecodeInto(v, body, &res); err != nil {
				t.Fatal(err)
			}
			runs[i] = time.Since(start)
		}
		return slices.Min(runs)
	}
	for _, v := range Variants {
		d := NewDecoder()
		s, l := minTime(d, v, small), minTime(d, v, large)
		ratio := float64(l) / float64(s)
		t.Logf("%s: 1k %v, 10k %v, ratio %.2f (bound 15), lazy members visited 10k: %d", v, s, l, ratio, d.Stats().Members)
		if ratio >= 15 {
			t.Errorf("%s: 10k/1k time ratio %.2f, want < 15", v, ratio)
		}
	}
}

// TestFastCheckParity checks that the utf8.ValidString plus word-at-a-time
// per-string check gives variant A1 the same verdict and path as
// codec.ValidString on every fixture.
func TestFastCheckParity(t *testing.T) {
	for _, name := range testsupport.FixtureNames(t, "*.json") {
		slow := NewDecoder()
		fast := NewDecoder()
		fast.v.fastCheck = true
		_, a := decodeFixture(t, slow, VariantA1, name)
		_, b := decodeFixture(t, fast, VariantA1, name)
		if a.String() != b.String() {
			t.Errorf("%s: codec check %s, fast check %s", name, a, b)
		}
	}
}

// variantA1Fast is variant A1 with the fast per-string check, in tests only.
const variantA1Fast Variant = 255

func variantName(v Variant) string {
	if v == variantA1Fast {
		return "a1-fast"
	}
	return v.String()
}

// duplicatesLastWins is testdata/duplicates.json resolved as Python 0.7.1
// resolves it (testdata/README.md; internal/testsupport/fixtures_test.go).
const duplicatesLastWins = `{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},"spam":{"type":"noul","noul":0.98},"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"fine","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

// TestModuleDuplicates decodes testdata/duplicates.json and the same body
// without repeats: every variant must return the same answers, model and
// usage for both (last wins at every level, first position kept), and no
// WARN for the superseded unknown answer.
func TestModuleDuplicates(t *testing.T) {
	for _, v := range Variants {
		t.Run(v.String(), func(t *testing.T) {
			d := NewDecoder()
			var got, want Response
			if err := d.DecodeInto(v, testsupport.Fixture(t, "duplicates.json"), &got); err != nil {
				t.Fatal(err)
			}
			if warns := d.Stats().Warns; warns != 0 {
				t.Errorf("warns = %d, want 0 (mystery was superseded)", warns)
			}
			if err := NewDecoder().DecodeInto(v, []byte(duplicatesLastWins), &want); err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(want.Answers.Entries(), got.Answers.Entries()); diff != "" {
				t.Errorf("answers (-last-wins body +duplicates.json):\n%s", diff)
			}
			if got.Model != want.Model || got.Usage != want.Usage {
				t.Errorf("model/usage = %q %+v, want %q %+v", got.Model, got.Usage, want.Model, want.Usage)
			}
			if got.Usage.HasOutputTokens {
				t.Errorf("usage kept output_tokens from the superseded usage member")
			}
		})
	}
}
