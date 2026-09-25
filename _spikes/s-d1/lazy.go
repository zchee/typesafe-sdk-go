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
	"slices"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

var errLazyMissed = errors.New("lazy pass: a structured legend level was not found")

// lazy is the one second pass that reads structured legend levels as exact
// bytes (plan 6.2.4): from the root (or, for variant B, the answers member),
// it takes the last answers member, the last member of each flagged name,
// its last legend member, and each level's Raw(), last wins throughout.
// Every lookup iterates members; Node.Get would return the first match.
//
// Raw() returns substrings of the body. Turning them into []byte needs a
// copy (the module has one unsafe bridge, and it goes the other way), and
// the copy is the interning miss path (plan 6.2.5: a level equal to the
// question's compact JSON reuses the request's bytes). By default every
// level is copied into one arena sized to the sum of their lengths, so the
// copies cost one allocation per response; perLevelCopy copies each level
// on its own, for the comparison in the ledger.
func (d *Decoder) lazy(src string, fromRoot bool) error {
	if err := d.lazyScan(src, fromRoot); err != nil {
		return err
	}
	v := &d.v
	var arena []byte
	if !d.perLevelCopy {
		n := 0
		for _, r := range d.raws {
			n += len(r)
		}
		arena = make([]byte, 0, n)
	}
	for i := range v.set {
		e := &v.set[i]
		if e.structured == 0 || e.ans.Kind == 0 {
			continue
		}
		raws := d.raws[d.rawBase[i]:]
		for j := range e.ans.Score.Legend {
			desc := &e.ans.Score.Legend[j].Description
			if desc.JSON == nil {
				continue
			}
			raw := raws[j]
			if raw == "" {
				return errLazyMissed
			}
			if d.perLevelCopy {
				desc.JSON = []byte(raw)
			} else {
				start := len(arena)
				arena = append(arena, raw...)
				desc.JSON = arena[start:len(arena):len(arena)]
			}
			d.stats.Structured++
		}
	}
	d.stats.LazyPasses = 1
	return nil
}

// lazyScan runs the iteration and leaves each flagged answer's level bytes in
// d.raws, from d.rawBase[i] on for answer i and indexed like its legend. It
// allocates what the lazy pass costs before any copy, so an allocation test
// can run it on its own.
func (d *Decoder) lazyScan(src string, fromRoot bool) error {
	v := &d.v
	root, err := sonic.GetFromString(src)
	if err != nil {
		return jsonErr(err)
	}
	answers := root
	var p ast.Pair
	if fromRoot {
		it, err := root.Properties()
		if err != nil {
			return jsonErr(err)
		}
		for it.Next(&p) {
			d.stats.Members++
			if p.Key == "answers" {
				answers = p.Value
			}
		}
	}
	d.nodes = growNodes(d.nodes, len(v.set))
	d.raws = d.raws[:0]
	d.rawBase = d.rawBase[:0]
	it, err := answers.Properties()
	if err != nil {
		return jsonErr(err)
	}
	for it.Next(&p) {
		d.stats.Members++
		if i := v.find(p.Key); i >= 0 && v.set[i].structured > 0 {
			d.nodes[i] = p.Value
		}
	}
	for i := range v.set {
		e := &v.set[i]
		if e.structured == 0 || e.ans.Kind == 0 {
			continue
		}
		it, err := d.nodes[i].Properties()
		if err != nil {
			return jsonErr(err)
		}
		var legend ast.Node
		for it.Next(&p) {
			d.stats.Members++
			if p.Key == "legend" {
				legend = p.Value
			}
		}
		entries := e.ans.Score.Legend
		for len(d.rawBase) <= i {
			d.rawBase = append(d.rawBase, 0)
		}
		base := len(d.raws)
		d.rawBase[i] = base
		d.raws = growRaws(d.raws, base+len(entries))
		useMap := len(entries) > 16
		if useMap {
			v.lvlMap(len(entries))
			for j := range entries {
				v.lvlIdx[entries[j].Level] = int32(j)
			}
		}
		lit, err := legend.Properties()
		if err != nil {
			return jsonErr(err)
		}
		for lit.Next(&p) {
			d.stats.Members++
			lvl, ok := parseLevel(p.Key)
			if !ok {
				continue // the visitor already refused such a key
			}
			j := findLevel(entries, lvl, useMap, v.lvlIdx)
			if j < 0 || entries[j].Description.JSON == nil {
				continue // a text level, or a level superseded by text
			}
			raw, err := p.Value.Raw()
			if err != nil {
				return jsonErr(err)
			}
			d.raws[base+j] = raw
		}
	}
	return nil
}

func growNodes(s []ast.Node, n int) []ast.Node {
	if cap(s) < n {
		return make([]ast.Node, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// growRaws extends s to length n with empty strings, keeping its prefix.
func growRaws(s []string, n int) []string {
	old := len(s)
	s = slices.Grow(s, n-old)[:n]
	clear(s[old:])
	return s
}
