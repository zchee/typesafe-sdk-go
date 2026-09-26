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

// Copies of the root package's test helpers that the allocation tests use.
// A test of the root package cannot import this package (it imports the
// root package), so the helpers the root package's own tests share stay
// there and are copied here; each copy names its original.

import (
	"maps"
	"net/http"
	"os"
	"testing"

	. "github.com/zchee/typesafe-sdk-go"
)

// testKey is the API key of every test client (transport_test.go).
const testKey = "test-key"

// clearEnv clears the SDK's environment variables for the test
// (config_test.go).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{APIKeyEnv, BaseURLEnv, DefaultModelEnv} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// newTestClient builds a client over rt, the test's transport
// (WithRoundTripper), with testKey (the upstream tests' key, 8 bytes long,
// so the checks that look for the key inside other text apply to it, ruling
// R68) and opts, after clearing the variables a client reads, so a
// developer's TYPESAFE_API_KEY never reaches a test. The client is closed
// when the test ends.
//
// Its calls make one attempt each (NoRetry), as the upstream tests' clients
// do unless a test asks for retries (tests/conftest.py:34-35, ruling R88b);
// a test that wants the production policy passes WithRetry(DefaultRetry())
// in opts, which comes later and wins.
func newTestClient(t *testing.T, rt http.RoundTripper, opts ...ClientOption) *Client {
	t.Helper()
	clearEnv(t)
	c, err := NewClient(append([]ClientOption{WithAPIKey(testKey), WithRoundTripper(rt), WithRetry(NoRetry())}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mustPrepared prepares qs, failing the test when Prepare fails.
func mustPrepared(t testing.TB, qs *Questions) *Prepared {
	t.Helper()
	p, err := qs.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

// q3Questions is the NF3 question set: the three questions of the upstream
// round-trip test (tests/test_clients.py:60-80), which result.json answers.
func q3Questions(t testing.TB) *Prepared {
	t.Helper()
	return mustPrepared(t, NewQuestions().
		Noul("spam", Noul{Instructions: Text("Spam?")}).
		Choice("tone", Choice{Instructions: Text("Tone?"), Options: Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", Score{Instructions: Text("Quality?"), Levels: []Content{Text("bad"), Text("ok"), Text("great")}}))
}

// answerView is what a test compares of an answer: its kind and every value
// its getters return.
type answerView struct {
	Kind          AnswerKind
	Noul          float64
	Choice        string
	Confidence    float64
	Score         float64
	Probabilities map[string]float64
	Levels        map[uint32]float64
	Legend        map[uint32]string
}

// viewOf returns the view of a.
func viewOf(a Answer) answerView {
	v := answerView{Kind: a.Kind()}
	if n, ok := a.Noul(); ok {
		v.Noul = n.Noul()
	}
	if c, ok := a.Choice(); ok {
		v.Choice, v.Confidence = c.Choice(), c.Confidence()
		v.Probabilities = maps.Collect(c.Probabilities())
	}
	if s, ok := a.Score(); ok {
		v.Score, v.Confidence = s.Score(), s.Confidence()
		v.Levels = maps.Collect(s.Probabilities())
		v.Legend = map[uint32]string{}
		for level, d := range s.Legend() {
			if d.IsJSON() {
				v.Legend[level] = string(d.JSON())
			} else {
				v.Legend[level] = d.Text()
			}
		}
	}
	return v
}

// usageView is what a test compares of a usage.
type usageView struct {
	In, Out       uint64
	HasIn, HasOut bool
}

// namedAnswer is one answer's view with its question's name.
type namedAnswer struct {
	Name   string
	Answer answerView
}

// payloadView is what a test compares of a System One response's payload:
// every value its getters return, the answers in the order of All.
type payloadView struct {
	Model   string
	Usage   usageView
	Answers []namedAnswer
}

// payloadOf returns the view of r's payload.
func payloadOf(r *SystemOneResponse) payloadView {
	v := payloadView{Model: r.Model()}
	v.Usage.In, v.Usage.HasIn = r.Usage().InputTokens()
	v.Usage.Out, v.Usage.HasOut = r.Usage().OutputTokens()
	for name, a := range r.Answers().All() {
		v.Answers = append(v.Answers, namedAnswer{name, viewOf(a)})
	}
	return v
}

// reviewAnswers types every answer of RESULT, with the question set
// q3Questions builds by hand (tests/test_clients.py:60-80).
type reviewAnswers struct {
	Spam    NoulAnswer   `typesafe:"kind=noul;name=spam;instructions=Spam?"`
	Tone    ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=Tone?;options=friendly|hostile"`
	Quality ScoreAnswer  `typesafe:"kind=score;name=quality;instructions=Quality?;levels=bad|ok|great"`
}
