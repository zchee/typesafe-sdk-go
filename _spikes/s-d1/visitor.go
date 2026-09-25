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
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/bytedance/sonic/ast"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// slot says what the next value means. Keys and array positions set it; a
// scalar consumes it.
type slot uint8

const (
	slotNone slot = iota
	slotIgnore
	slotRoot
	slotModel
	slotUsage
	slotAnswers
	slotInputTokens
	slotOutputTokens
	slotAnswer
	slotType
	slotNoul
	slotChoice
	slotConfidence
	slotScore
	slotProbs
	slotLegend
	slotProbValue
	slotLegendValue
	slotModels
	slotCard
	slotCardName
	slotCardDescription
	slotCardReleaseDate
)

// ctr is a known container on the visitor's stack.
type ctr uint8

const (
	ctrRoot ctr = iota
	ctrUsage
	ctrAnswers
	ctrAnswer
	ctrProbs
	ctrLegend
	ctrModels
	ctrCard
)

// mode is where a traversal starts: a whole body, or one member's value
// (variant B visits each top-level member on its own).
type mode uint8

const (
	modeAnswersBody mode = iota
	modeModelsBody
	modeModelValue
	modeUsageValue
	modeAnswersValue
	modeModelsValue
	modeCheckOnly
)

// member bits of an answer.
const (
	mType uint16 = 1 << iota
	mNoul
	mChoice
	mConfidence
	mScore
	mProbs
	mLegend
)

// memberOrder is the schema order in which member errors are reported.
var memberOrder = [...]struct {
	bit  uint16
	name string
}{
	{mType, "type"},
	{mNoul, "noul"},
	{mChoice, "choice"},
	{mConfidence, "confidence"},
	{mScore, "score"},
	{mProbs, "probabilities"},
	{mLegend, "legend"},
}

// required lists the members each known kind needs besides type.
var required = [...]uint16{
	wire.KindNoul:   mNoul,
	wire.KindChoice: mChoice | mConfidence | mProbs,
	wire.KindScore:  mScore | mConfidence | mLegend | mProbs,
}

// pend is a pending validation error. It holds the parts of the field path,
// so recording one allocates nothing; the path is built only when the
// decode fails.
type pend struct {
	set    bool
	name   string // answer name, when under answers
	member string // member of the answer or of usage
	key    string // probability or legend key
	root   string // root member ("model", "usage", "answers")
	msg    string
}

func (p pend) path() string {
	if p.root != "" {
		if p.member != "" {
			return p.root + "." + p.member
		}
		return p.root
	}
	path := "answers." + p.name
	if p.member != "" {
		path += "." + p.member
	}
	if p.key != "" {
		path += "." + p.key
	}
	return path
}

type probPair struct {
	key string
	p   float64
}

type legendPair struct {
	key        string
	text       string
	structured bool
}

// answerScratch collects one answer object's members until its end.
type answerScratch struct {
	name   string
	typ    string
	noul   float64
	conf   float64
	score  float64
	choice string
	has    uint16 // members present with the right kind (last occurrence)
	wrong  uint16 // members present with the wrong kind (last occurrence)
	subErr pend   // first bad probability or legend value of the last occurrence
	subBit uint16 // which member subErr belongs to
}

// entry is one answer name in the scratch answer set.
type entry struct {
	name       string
	ans        wire.Answer
	err        pend
	structured int // legend levels waiting for the lazy pass
}

// visitor is the ast.Visitor every variant runs. It skips nothing: sonic
// validates every token it hands over, and every string is checked here.
type visitor struct {
	body string
	mode mode

	slot    slot
	ign     int  // depth inside an ignored container
	ignSlot slot // the slot whose value the ignored container is
	stack   [8]ctr
	sp      int

	// fastCheck selects utf8.ValidString plus the word-at-a-time control
	// test for the per-string check instead of codec.ValidString's one loop
	// (W0.2 review input; both are measured).
	fastCheck bool

	scanned bool // the body's raw-control scan has run
	rawCtl  bool // its result
	scans   int  // number of body scans, for the report

	// root members
	model      string
	hasModel   bool
	modelErr   pend
	usage      wire.Usage
	hasUsage   bool
	usageErr   pend
	answersErr pend

	// answers
	cur    answerScratch
	curKey string
	set    []entry
	setIdx map[string]int32
	idxOn  bool
	probs  []probPair
	legend []legendPair
	strIdx map[string]int32
	lvlIdx map[uint32]int32
	warns  int

	// models
	cards    []wire.ModelCard
	card     wire.ModelCard
	cardHas  uint8
	cardsErr pend
	hasCards bool
}

var _ ast.Visitor = (*visitor)(nil)

var (
	errRootNotObject = errors.New("the response is not a JSON object")
	errInvalidUTF8   = errors.New("invalid UTF-8 in a string")
	errRawControl    = errors.New("raw control character in a string")
)

// reset prepares the visitor for a new body, keeping its scratch capacity.
func (v *visitor) reset(body string, m mode) {
	v.body = body
	v.scanned, v.rawCtl, v.scans = false, false, 0
	v.model, v.hasModel, v.modelErr = "", false, pend{}
	v.usage, v.hasUsage, v.usageErr = wire.Usage{}, false, pend{}
	v.answersErr = pend{}
	v.resetAnswers()
	v.warns = 0
	v.cards = v.cards[:0]
	v.cardsErr, v.hasCards = pend{}, false
	v.start(m)
}

// start positions the visitor at the first value of a traversal in mode m.
func (v *visitor) start(m mode) {
	v.mode = m
	v.ign, v.sp = 0, 0
	switch m {
	case modeAnswersBody, modeModelsBody:
		v.slot = slotRoot
	case modeModelValue:
		v.slot = slotModel
	case modeUsageValue:
		v.slot = slotUsage
	case modeAnswersValue:
		v.slot = slotAnswers
	case modeModelsValue:
		v.slot = slotModels
	case modeCheckOnly:
		v.slot = slotIgnore
	}
}

func (v *visitor) resetAnswers() {
	clear(v.set)
	v.set = v.set[:0]
	if v.idxOn {
		clear(v.setIdx)
		v.idxOn = false
	}
}

// checkString is the per-string check: UTF-8 and no raw control character.
// sonic hands a string without escapes over as the raw text between its
// quotes and a string with escapes decoded, so a control character in s is
// legal when it came from an escape such as \n. When s holds one, the body
// is scanned once for a raw control character inside a string, which
// decides for every string of the body.
func (v *visitor) checkString(s string) error {
	if v.fastCheck {
		if utf8.ValidString(s) && !HasControlByte(s) {
			return nil
		}
	} else if codec.ValidString(s) {
		return nil
	}
	if !utf8.ValidString(s) {
		return errInvalidUTF8
	}
	if !v.scanned {
		v.scanned = true
		v.scans++
		v.rawCtl = controlInString(v.body)
	}
	if v.rawCtl {
		return errRawControl
	}
	return nil
}

func (v *visitor) push(c ctr) { v.stack[v.sp] = c; v.sp++ }

func (v *visitor) top() ctr { return v.stack[v.sp-1] }

// begin handles an object (isObj) or array starting in the current slot.
func (v *visitor) begin(isObj bool) error {
	if v.ign > 0 {
		v.ign++
		return nil
	}
	s := v.slot
	v.slot = slotNone
	switch {
	case s == slotRoot:
		if !isObj {
			return errRootNotObject
		}
		v.push(ctrRoot)
		return nil
	case s == slotUsage && isObj:
		v.usage, v.hasUsage, v.usageErr = wire.Usage{}, true, pend{}
		v.push(ctrUsage)
		return nil
	case s == slotAnswers && isObj:
		v.resetAnswers()
		v.answersErr = pend{}
		v.push(ctrAnswers)
		return nil
	case s == slotAnswer && isObj:
		v.cur = answerScratch{name: v.curKey}
		v.push(ctrAnswer)
		return nil
	case s == slotProbs && isObj:
		v.memberOK(mProbs)
		v.probs = v.probs[:0]
		v.push(ctrProbs)
		return nil
	case s == slotLegend && isObj:
		v.memberOK(mLegend)
		v.legend = v.legend[:0]
		v.push(ctrLegend)
		return nil
	case s == slotModels && !isObj:
		v.cards, v.hasCards, v.cardsErr = v.cards[:0], true, pend{}
		v.push(ctrModels)
		v.slot = slotCard
		return nil
	case s == slotCard && isObj:
		v.card, v.cardHas = wire.ModelCard{}, 0
		v.push(ctrCard)
		return nil
	}
	// Any other container is a value nobody reads, or one of the wrong kind:
	// traverse it, check its strings, and decide at its end.
	v.ign, v.ignSlot = 1, s
	return nil
}

// end handles the end of an object or array.
func (v *visitor) end() error {
	if v.ign > 0 {
		v.ign--
		if v.ign == 0 {
			v.wrongKind(v.ignSlot, true)
			if v.sp > 0 && v.top() == ctrModels {
				v.slot = slotCard
			}
		}
		return nil
	}
	v.sp--
	switch v.stack[v.sp] {
	case ctrAnswer:
		v.commitAnswer()
	case ctrCard:
		v.commitCard()
	case ctrModels:
		return nil // the array's end: no slot follows
	}
	if v.sp > 0 && v.top() == ctrModels {
		v.slot = slotCard
	}
	return nil
}

// wrongKind records a value of the wrong kind in slot s. container says the
// value was an object or array; for a legend level that is the structured
// form, which is right.
func (v *visitor) wrongKind(s slot, container bool) {
	switch s {
	case slotModel:
		v.hasModel, v.modelErr = true, pend{set: true, root: "model", msg: "not a string"}
	case slotUsage:
		v.hasUsage, v.usageErr = true, pend{set: true, root: "usage", msg: "not an object"}
	case slotAnswers:
		v.resetAnswers()
		v.answersErr = pend{set: true, root: "answers", msg: "not an object"}
	case slotInputTokens:
		v.usageErr = pend{set: true, root: "usage", member: "input_tokens", msg: "not an unsigned integer"}
	case slotOutputTokens:
		v.usageErr = pend{set: true, root: "usage", member: "output_tokens", msg: "not an unsigned integer"}
	case slotAnswer:
		v.put(entry{name: v.curKey, err: pend{set: true, name: v.curKey, msg: "not an object"}})
	case slotType:
		v.memberWrong(mType)
	case slotNoul:
		v.memberWrong(mNoul)
	case slotChoice:
		v.memberWrong(mChoice)
	case slotConfidence:
		v.memberWrong(mConfidence)
	case slotScore:
		v.memberWrong(mScore)
	case slotProbs:
		v.memberWrong(mProbs)
	case slotLegend:
		v.memberWrong(mLegend)
	case slotProbValue:
		v.subError(mProbs, "probabilities", "not a number")
	case slotLegendValue:
		if container {
			v.legend = append(v.legend, legendPair{key: v.curKey, structured: true})
			return
		}
		v.subError(mLegend, "legend", "not a string, object or array")
	case slotModels:
		v.hasCards, v.cardsErr = true, pend{set: true, root: "models", msg: "not an array"}
	case slotCard:
		v.cardsErr = pend{set: true, root: "models", msg: "not an object"}
	case slotCardName, slotCardDescription, slotCardReleaseDate:
		v.cardsErr = pend{set: true, root: "models", msg: "card member not a string"}
	}
}

func (v *visitor) memberOK(bit uint16) {
	v.cur.has |= bit
	v.cur.wrong &^= bit
	if v.cur.subBit == bit {
		v.cur.subErr, v.cur.subBit = pend{}, 0
	}
}

func (v *visitor) memberWrong(bit uint16) {
	v.cur.wrong |= bit
	v.cur.has &^= bit
}

func (v *visitor) subError(bit uint16, member, msg string) {
	if v.cur.subBit == 0 {
		v.cur.subErr = pend{set: true, name: v.cur.name, member: member, key: v.curKey, msg: msg}
		v.cur.subBit = bit
	}
}

// scalar handles a string (isStr, s) or another scalar in the current slot.
// num is the number's text for a number, "" otherwise.
func (v *visitor) scalar(isStr bool, s string, num json.Number) error {
	if v.ign > 0 {
		return nil
	}
	if v.slot == slotRoot {
		return errRootNotObject
	}
	v.assign(isStr, s, num)
	return nil
}

// assign stores a scalar in the current slot, or records it as a value of the
// wrong kind.
func (v *visitor) assign(isStr bool, s string, num json.Number) {
	sl := v.slot
	v.slot = slotNone
	if v.sp > 0 && v.top() == ctrModels {
		v.slot = slotCard
	}
	switch sl {
	case slotNone, slotIgnore:
		return
	case slotModel:
		if isStr {
			v.model, v.hasModel, v.modelErr = s, true, pend{}
			return
		}
	case slotInputTokens, slotOutputTokens:
		if num != "" {
			if n, err := strconv.ParseUint(string(num), 10, 64); err == nil {
				if sl == slotInputTokens {
					v.usage.InputTokens, v.usage.HasInputTokens = n, true
				} else {
					v.usage.OutputTokens, v.usage.HasOutputTokens = n, true
				}
				if v.usageErr.member == memberName(sl) {
					v.usageErr = pend{}
				}
				return
			}
		}
	case slotType:
		if isStr {
			v.cur.typ = s
			v.memberOK(mType)
			return
		}
	case slotChoice:
		if isStr {
			v.cur.choice = s
			v.memberOK(mChoice)
			return
		}
	case slotNoul, slotConfidence, slotScore:
		if f, ok := parseFloat(num); ok {
			switch sl {
			case slotNoul:
				v.cur.noul = f
				v.memberOK(mNoul)
			case slotConfidence:
				v.cur.conf = f
				v.memberOK(mConfidence)
			default:
				v.cur.score = f
				v.memberOK(mScore)
			}
			return
		}
	case slotProbValue:
		if f, ok := parseFloat(num); ok {
			v.probs = append(v.probs, probPair{key: v.curKey, p: f})
			return
		}
	case slotLegendValue:
		if isStr {
			v.legend = append(v.legend, legendPair{key: v.curKey, text: s})
			return
		}
	case slotCardName, slotCardDescription, slotCardReleaseDate:
		if isStr {
			switch sl {
			case slotCardName:
				v.card.Name = s
				v.cardHas |= 1
			case slotCardDescription:
				v.card.Description = s
				v.cardHas |= 2
			default:
				v.card.ReleaseDate = s
				v.cardHas |= 4
			}
			return
		}
	}
	v.wrongKind(sl, false)
}

func memberName(s slot) string {
	if s == slotInputTokens {
		return "input_tokens"
	}
	return "output_tokens"
}

// parseFloat parses a JSON number the traversal delivered with OnlyNumber.
// An out-of-range value (1e400) fails: the port rejects it (deviation).
func parseFloat(num json.Number) (float64, bool) {
	if num == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(num), 64)
	return f, err == nil
}

func (v *visitor) OnNull() error           { return v.scalar(false, "", "") }
func (v *visitor) OnBool(bool) error       { return v.scalar(false, "", "") }
func (v *visitor) OnObjectBegin(int) error { return v.begin(true) }
func (v *visitor) OnArrayBegin(int) error  { return v.begin(false) }
func (v *visitor) OnObjectEnd() error      { return v.end() }
func (v *visitor) OnArrayEnd() error       { return v.end() }

func (v *visitor) OnString(s string) error {
	if err := v.checkString(s); err != nil {
		return err
	}
	return v.scalar(true, s, "")
}

func (v *visitor) OnInt64(_ int64, n json.Number) error     { return v.scalar(false, "", n) }
func (v *visitor) OnFloat64(_ float64, n json.Number) error { return v.scalar(false, "", n) }

func (v *visitor) OnObjectKey(key string) error {
	if err := v.checkString(key); err != nil {
		return err
	}
	if v.ign > 0 {
		return nil
	}
	v.curKey = key
	switch v.top() {
	case ctrRoot:
		switch {
		case v.mode == modeModelsBody && key == "models":
			v.slot = slotModels
		case v.mode == modeAnswersBody && key == "model":
			v.slot = slotModel
		case v.mode == modeAnswersBody && key == "usage":
			v.slot = slotUsage
		case v.mode == modeAnswersBody && key == "answers":
			v.slot = slotAnswers
		default:
			v.slot = slotIgnore
		}
	case ctrUsage:
		switch key {
		case "input_tokens":
			v.slot = slotInputTokens
		case "output_tokens":
			v.slot = slotOutputTokens
		default:
			v.slot = slotIgnore
		}
	case ctrAnswers:
		v.slot = slotAnswer
	case ctrAnswer:
		switch key {
		case "type":
			v.slot = slotType
		case "noul":
			v.slot = slotNoul
		case "choice":
			v.slot = slotChoice
		case "confidence":
			v.slot = slotConfidence
		case "score":
			v.slot = slotScore
		case "probabilities":
			v.slot = slotProbs
		case "legend":
			v.slot = slotLegend
		default:
			v.slot = slotIgnore
		}
	case ctrProbs:
		v.slot = slotProbValue
	case ctrLegend:
		v.slot = slotLegendValue
	case ctrCard:
		switch key {
		case "name":
			v.slot = slotCardName
		case "description":
			v.slot = slotCardDescription
		case "release_date":
			v.slot = slotCardReleaseDate
		default:
			v.slot = slotIgnore
		}
	default:
		v.slot = slotIgnore
	}
	return nil
}

// structuredMark marks a legend level whose bytes the lazy pass fills in.
// It is non-nil and empty, which no decoded level can be.
var structuredMark = make([]byte, 0)

// commitAnswer ends an answer object: its type decides which members it
// needs, and its value replaces any earlier answer of the same name.
func (v *visitor) commitAnswer() {
	a := &v.cur
	e := entry{name: a.name}
	switch {
	case a.has&mType == 0:
		e.err = pend{set: true, name: a.name, member: "type", msg: "missing or not a string"}
	default:
		e.ans.Kind = wire.ParseKind(a.typ)
		if e.ans.Kind == wire.KindUnknown {
			v.warns++ // WARN line, buffered: a later duplicate discards it
			break
		}
		if e.err = v.memberErrors(e.ans.Kind); e.err.set {
			break
		}
		switch e.ans.Kind {
		case wire.KindNoul:
			e.ans.Noul.Noul = a.noul
		case wire.KindChoice:
			e.ans.Choice = wire.ChoiceAnswer{Choice: a.choice, Confidence: a.conf, Probabilities: v.labelProbs()}
		case wire.KindScore:
			e.ans.Score.Score, e.ans.Score.Confidence = a.score, a.conf
			e.err = v.scoreLists(&e)
		}
	}
	v.put(e)
}

// memberErrors returns the first wrong-kind member, then the first missing
// one, in schema order.
func (v *visitor) memberErrors(k wire.Kind) pend {
	a := &v.cur
	need := required[k]
	for _, m := range memberOrder {
		if a.wrong&m.bit != 0 && (need|mType)&m.bit != 0 {
			return pend{set: true, name: a.name, member: m.name, msg: "wrong kind"}
		}
	}
	if a.subBit&need != 0 {
		return a.subErr
	}
	for _, m := range memberOrder {
		if need&m.bit != 0 && a.has&m.bit == 0 {
			return pend{set: true, name: a.name, member: m.name, msg: "missing"}
		}
	}
	return pend{}
}

// labelProbs returns the choice probabilities, one per label, each label at
// its first position with its last value.
func (v *visitor) labelProbs() []wire.LabelProbability {
	out := make([]wire.LabelProbability, 0, len(v.probs))
	useMap := len(v.probs) > 16
	if useMap {
		if v.strIdx == nil {
			v.strIdx = make(map[string]int32, len(v.probs))
		}
		clear(v.strIdx)
	}
	for _, p := range v.probs {
		i := -1
		if useMap {
			if j, ok := v.strIdx[p.key]; ok {
				i = int(j)
			}
		} else {
			for j := range out {
				if out[j].Label == p.key {
					i = j
					break
				}
			}
		}
		if i >= 0 {
			out[i].Probability = p.p
			continue
		}
		if useMap {
			v.strIdx[p.key] = int32(len(out))
		}
		out = append(out, wire.LabelProbability{Label: p.key, Probability: p.p})
	}
	return out
}

// scoreLists parses the level keys and builds the legend and probabilities,
// one entry per level, each at its first position with its last value.
func (v *visitor) scoreLists(e *entry) pend {
	legend := make([]wire.LegendEntry, 0, len(v.legend))
	useMap := len(v.legend) > 16
	if useMap {
		v.lvlMap(len(v.legend))
	}
	for _, l := range v.legend {
		lvl, ok := parseLevel(l.key)
		if !ok {
			return pend{set: true, name: e.name, member: "legend", key: l.key, msg: "not a level"}
		}
		desc := wire.Content{Text: l.text}
		if l.structured {
			desc = wire.Content{JSON: structuredMark}
		}
		if i := findLevel(legend, lvl, useMap, v.lvlIdx); i >= 0 {
			legend[i].Description = desc
			continue
		}
		if useMap {
			v.lvlIdx[lvl] = int32(len(legend))
		}
		legend = append(legend, wire.LegendEntry{Level: lvl, Description: desc})
	}
	for i := range legend {
		if legend[i].Description.JSON != nil {
			e.structured++
		}
	}
	probs := make([]wire.LevelProbability, 0, len(v.probs))
	useMap = len(v.probs) > 16
	if useMap {
		v.lvlMap(len(v.probs))
	}
	for _, p := range v.probs {
		lvl, ok := parseLevel(p.key)
		if !ok {
			return pend{set: true, name: e.name, member: "probabilities", key: p.key, msg: "not a level"}
		}
		i := -1
		if useMap {
			if j, ok := v.lvlIdx[lvl]; ok {
				i = int(j)
			}
		} else {
			for j := range probs {
				if probs[j].Level == lvl {
					i = j
					break
				}
			}
		}
		if i >= 0 {
			probs[i].Probability = p.p
			continue
		}
		if useMap {
			v.lvlIdx[lvl] = int32(len(probs))
		}
		probs = append(probs, wire.LevelProbability{Level: lvl, Probability: p.p})
	}
	e.ans.Score.Legend, e.ans.Score.Probabilities = legend, probs
	return pend{}
}

func (v *visitor) lvlMap(n int) {
	if v.lvlIdx == nil {
		v.lvlIdx = make(map[uint32]int32, n)
	}
	clear(v.lvlIdx)
}

func findLevel(legend []wire.LegendEntry, lvl uint32, useMap bool, idx map[uint32]int32) int {
	if useMap {
		if j, ok := idx[lvl]; ok {
			return int(j)
		}
		return -1
	}
	for j := range legend {
		if legend[j].Level == lvl {
			return j
		}
	}
	return -1
}

// parseLevel parses a level key with the grammar [+]?[0-9]+ into a uint32
// (plan 6.2.6).
func parseLevel(key string) (uint32, bool) {
	s := key
	if len(s) > 0 && s[0] == '+' {
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	var n uint64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
		if n > 1<<32-1 {
			return 0, false
		}
	}
	return uint32(n), true
}

// put stores e under its name: a repeated name keeps its first position and
// takes the last value (Python's dict).
func (v *visitor) put(e entry) {
	if i := v.find(e.name); i >= 0 {
		v.set[i] = e
		return
	}
	v.set = append(v.set, e)
	switch {
	case v.idxOn:
		v.setIdx[e.name] = int32(len(v.set) - 1)
	case len(v.set) > 8:
		if v.setIdx == nil {
			v.setIdx = make(map[string]int32, 2*len(v.set))
		}
		for i := range v.set {
			v.setIdx[v.set[i].name] = int32(i)
		}
		v.idxOn = true
	}
}

func (v *visitor) find(name string) int {
	if v.idxOn {
		if i, ok := v.setIdx[name]; ok {
			return int(i)
		}
		return -1
	}
	for i := range v.set {
		if v.set[i].name == name {
			return i
		}
	}
	return -1
}

func (v *visitor) commitCard() {
	if v.cardHas != 7 {
		if !v.cardsErr.set {
			v.cardsErr = pend{set: true, root: "models", msg: "card member missing"}
		}
		return
	}
	v.cards = append(v.cards, v.card)
}
