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

package sd2

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// The struct types whose first PreparedFor call W4.3 measures (deliverable
// B), each beside the question set a caller would build by hand for the
// same questions.

// one asks one noul, the set of W1.3's c2-noul-short
// (prepare_cases_test.go).
type one struct {
	Spam typesafe.NoulAnswer `typesafe:"kind=noul;name=spam;instructions=Spam?"`
}

// ticket is the port plan's section 5 Ticket, as the root tests' Ticket
// (typed_test.go) spells it.
type ticket struct {
	Billing typesafe.NoulAnswer   `typesafe:"kind=noul;instructions=Is this about billing, invoices or refunds?;yes=payments or invoices"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;instructions=What is the tone?;options=calm=neutral or polite|angry"`
	Urgency typesafe.ScoreAnswer  `typesafe:"kind=score;instructions=How urgent?;levels=can wait|this week|today"`
	Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;optional;instructions=Spam?"`
}

// twenty asks result-20.json's questions: 7 noul, 7 choice and 6 score, the
// root tests' result20Answers.
type twenty struct {
	Spam          typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Tone          typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;options=calm|angry|confused"`
	Urgency       typesafe.ScoreAnswer  `typesafe:"kind=score;name=urgency;levels=can wait|this week|today"`
	Billing       typesafe.NoulAnswer   `typesafe:"kind=noul;name=billing"`
	Language      typesafe.ChoiceAnswer `typesafe:"kind=choice;name=language;options=en|ja|de|fr"`
	Sentiment     typesafe.ScoreAnswer  `typesafe:"kind=score;name=sentiment;levels=very negative|negative|neutral|positive|very positive"`
	RefundRequest typesafe.NoulAnswer   `typesafe:"kind=noul;name=refund_request"`
	Team          typesafe.ChoiceAnswer `typesafe:"kind=choice;name=team;options=billing|support|sales"`
	Priority      typesafe.ScoreAnswer  `typesafe:"kind=score;name=priority;levels=low|normal|high|critical"`
	PIIPresent    typesafe.NoulAnswer   `typesafe:"kind=noul;name=pii_present"`
	Product       typesafe.ChoiceAnswer `typesafe:"kind=choice;name=product;options=sdk|api|dashboard|other"`
	Clarity       typesafe.ScoreAnswer  `typesafe:"kind=score;name=clarity;levels=unclear|partly clear|clear"`
	NeedsHuman    typesafe.NoulAnswer   `typesafe:"kind=noul;name=needs_human"`
	Channel       typesafe.ChoiceAnswer `typesafe:"kind=choice;name=channel;options=email|chat|phone"`
	Complexity    typesafe.ScoreAnswer  `typesafe:"kind=score;name=complexity;levels=simple|moderate|complex"`
	Duplicate     typesafe.NoulAnswer   `typesafe:"kind=noul;name=duplicate"`
	Intent        typesafe.ChoiceAnswer `typesafe:"kind=choice;name=intent;options=question|complaint|request|feedback"`
	Effort        typesafe.ScoreAnswer  `typesafe:"kind=score;name=effort;levels=minutes|hours|days"`
	Escalate      typesafe.NoulAnswer   `typesafe:"kind=noul;name=escalate"`
	Resolution    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=resolution;options=resolved|pending|wontfix"`
}

// ten asks the first ten of twenty's questions: 4 noul, 3 choice and 3
// score.
type ten struct {
	Spam          typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam"`
	Tone          typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;options=calm|angry|confused"`
	Urgency       typesafe.ScoreAnswer  `typesafe:"kind=score;name=urgency;levels=can wait|this week|today"`
	Billing       typesafe.NoulAnswer   `typesafe:"kind=noul;name=billing"`
	Language      typesafe.ChoiceAnswer `typesafe:"kind=choice;name=language;options=en|ja|de|fr"`
	Sentiment     typesafe.ScoreAnswer  `typesafe:"kind=score;name=sentiment;levels=very negative|negative|neutral|positive|very positive"`
	RefundRequest typesafe.NoulAnswer   `typesafe:"kind=noul;name=refund_request"`
	Team          typesafe.ChoiceAnswer `typesafe:"kind=choice;name=team;options=billing|support|sales"`
	Priority      typesafe.ScoreAnswer  `typesafe:"kind=score;name=priority;levels=low|normal|high|critical"`
	PIIPresent    typesafe.NoulAnswer   `typesafe:"kind=noul;name=pii_present"`
}

// warmUp is the type of each child process's first PreparedFor call, made
// before the measured one so that the measured call does not pay the
// cache's own first use.
type warmUp struct {
	W typesafe.NoulAnswer `typesafe:"kind=noul;name=w"`
}

// spec is one question of a set built by hand: its kind, its name, and a
// choice's option labels or a score's levels.
type spec struct {
	kind, name string
	items      []string
}

// twentySpec is twenty's questions, in its field order.
var twentySpec = []spec{
	{"noul", "spam", nil},
	{"choice", "tone", []string{"calm", "angry", "confused"}},
	{"score", "urgency", []string{"can wait", "this week", "today"}},
	{"noul", "billing", nil},
	{"choice", "language", []string{"en", "ja", "de", "fr"}},
	{"score", "sentiment", []string{"very negative", "negative", "neutral", "positive", "very positive"}},
	{"noul", "refund_request", nil},
	{"choice", "team", []string{"billing", "support", "sales"}},
	{"score", "priority", []string{"low", "normal", "high", "critical"}},
	{"noul", "pii_present", nil},
	{"choice", "product", []string{"sdk", "api", "dashboard", "other"}},
	{"score", "clarity", []string{"unclear", "partly clear", "clear"}},
	{"noul", "needs_human", nil},
	{"choice", "channel", []string{"email", "chat", "phone"}},
	{"score", "complexity", []string{"simple", "moderate", "complex"}},
	{"noul", "duplicate", nil},
	{"choice", "intent", []string{"question", "complaint", "request", "feedback"}},
	{"score", "effort", []string{"minutes", "hours", "days"}},
	{"noul", "escalate", nil},
	{"choice", "resolution", []string{"resolved", "pending", "wontfix"}},
}

// build adds specs to a new question set as a caller writes them: a noul
// with no texts, a choice with undescribed options, a score with text
// levels.
func build(specs []spec) *typesafe.Questions {
	qs := typesafe.NewQuestions()
	for _, s := range specs {
		switch s.kind {
		case "noul":
			qs.Noul(s.name, typesafe.Noul{})
		case "choice":
			options := make(typesafe.Options, len(s.items))
			for i, label := range s.items {
				options[i] = typesafe.Option{Label: label}
			}
			qs.Choice(s.name, typesafe.Choice{Options: options})
		default:
			levels := make([]typesafe.Content, len(s.items))
			for i, level := range s.items {
				levels[i] = typesafe.Text(level)
			}
			qs.Score(s.name, typesafe.Score{Levels: levels})
		}
	}
	return qs
}

// shape is one measured type: its PreparedFor, and the same question set
// built by hand.
type shape struct {
	name        string
	fields      int
	preparedFor func() (*typesafe.Prepared, error)
	byHand      func() *typesafe.Questions
}

// shapes are the measured types, smallest first.
var shapes = []shape{
	{
		name: "one", fields: 1, preparedFor: typesafe.PreparedFor[one],
		byHand: func() *typesafe.Questions {
			return typesafe.NewQuestions().Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")})
		},
	},
	{
		name: "ticket", fields: 4, preparedFor: typesafe.PreparedFor[ticket],
		byHand: func() *typesafe.Questions {
			return typesafe.NewQuestions().
				Noul("Billing", typesafe.Noul{Instructions: typesafe.Text("Is this about billing, invoices or refunds?"), Yes: typesafe.Text("payments or invoices")}).
				Choice("Tone", typesafe.Choice{Instructions: typesafe.Text("What is the tone?"), Options: typesafe.Options{{Label: "calm", Description: typesafe.Text("neutral or polite")}, {Label: "angry"}}}).
				Score("Urgency", typesafe.Score{Instructions: typesafe.Text("How urgent?"), Levels: []typesafe.Content{typesafe.Text("can wait"), typesafe.Text("this week"), typesafe.Text("today")}}).
				Noul("Spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")})
		},
	},
	{name: "ten", fields: 10, preparedFor: typesafe.PreparedFor[ten], byHand: func() *typesafe.Questions { return build(twentySpec[:10]) }},
	{name: "twenty", fields: 20, preparedFor: typesafe.PreparedFor[twenty], byHand: func() *typesafe.Questions { return build(twentySpec) }},
}

// TestShapesMatchByHand checks that each measured type declares, byte for
// byte, the question set its hand-built twin prepares, so that the first
// call's cost beyond the twin's is what the typed path adds.
func TestShapesMatchByHand(t *testing.T) {
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			typed, err := s.preparedFor()
			if err != nil {
				t.Fatal(err)
			}
			hand, err := s.byHand().Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if typed.Len() != s.fields {
				t.Errorf("PreparedFor: %d questions, want %d", typed.Len(), s.fields)
			}
			if diff := gocmp.Diff(string(wirePrepared(t, hand).Questions), string(wirePrepared(t, typed).Questions)); diff != "" {
				t.Errorf("PreparedFor's set differs from the hand-built one (-hand +typed):\n%s", diff)
			}
		})
	}
}
