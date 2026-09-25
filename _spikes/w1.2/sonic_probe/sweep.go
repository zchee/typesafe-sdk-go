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

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
)

// class accumulates one value class of the sweep: a digest of its inputs, a
// digest of sonic's output for every input, the same digest without the
// negative zeros, and the inputs whose sonic spelling differs from a
// reference. Two hosts whose input digests agree encoded the same values, so
// equal output digests mean equal bytes for every value of the class.
type class struct {
	name                      string
	n                         int
	in, out, outExceptNegZero hash.Hash
	differ                    int
	shown                     []string          // up to 20 distinct differing inputs, in order
	lines                     map[string]string // its line, by differing input
	times                     map[string]int    // how often each differing input was encoded
}

func newClass(name string) *class {
	return &class{
		name: name, in: sha256.New(), out: sha256.New(), outExceptNegZero: sha256.New(),
		lines: map[string]string{}, times: map[string]int{},
	}
}

// add records one input: key identifies it the same way on every host (its
// bits, or its bytes in hex), got is sonic's output and want the
// reference's ("" for no reference); negZero marks a negative zero.
func (c *class) add(key, got, want string, negZero bool) {
	c.n++
	fmt.Fprintf(c.in, "%d:%s", len(key), key)
	fmt.Fprintf(c.out, "%d:%s", len(got), got)
	if !negZero {
		fmt.Fprintf(c.outExceptNegZero, "%d:%s", len(got), got)
	}
	if want != "" && got != want {
		c.differ++
		if _, ok := c.lines[key]; !ok && len(c.shown) < 20 {
			c.shown = append(c.shown, key)
			c.lines[key] = fmt.Sprintf("sonic %s, reference %s", got, want)
		}
		c.times[key]++
	}
}

// print writes the class's line and up to 20 distinct inputs whose spelling
// differs from ref, the reference's name ("" for none).
func (c *class) print(ref string) {
	fmt.Printf("%s n=%d inputs=%x sonic=%x sonic-except-negative-zero=%x", c.name, c.n, c.in.Sum(nil), c.out.Sum(nil), c.outExceptNegZero.Sum(nil))
	if ref != "" {
		fmt.Printf(" differs-from-%s=%d", ref, c.differ)
	}
	fmt.Println()
	for _, key := range c.shown {
		fmt.Printf("  %s %s (%d of the inputs): %s\n", c.name, key, c.times[key], c.lines[key])
	}
}

// marshal is encoding/json's spelling of v, the reference for floats.
func marshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "ERR " + err.Error()
	}
	return string(b)
}

// parse returns the float that strconv reads from m×10^k at bitSize: correctly
// rounded on every architecture, where math.Pow may round differently.
func parse(m int64, k, bitSize int) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatInt(m, 10)+"e"+strconv.Itoa(k), bitSize)
	return v
}

// sweep encodes deterministic value sets (a PCG with a fixed seed) with
// sonic and prints one line per class.
func sweep() {
	r := rand.New(rand.NewPCG(0x5ca1ab1e, 27))
	sweepFloat64(r)
	sweepFloat32(r)
	sweepIntegers(r)
	sweepStrings(r)
}

func sweepFloat64(r *rand.Rand) {
	c := newClass("float64")
	add := func(v float64) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		c.add(fmt.Sprintf("%016x", math.Float64bits(v)), enc(v), marshal(v), v == 0 && math.Signbit(v))
	}
	addBoth := func(v float64) {
		add(v)
		add(-v)
	}
	addBoth(0)
	for e := -325; e <= 309; e++ { // powers of ten and their neighbours
		p := parse(1, e, 64)
		addBoth(p)
		addBoth(math.Nextafter(p, 0))
		addBoth(math.Nextafter(p, math.Inf(1)))
	}
	for range 1 << 18 { // short decimals across the fixed/exponent switches
		addBoth(parse(r.Int64N(10_000_000)+1, r.IntN(61)-30, 64))
	}
	for range 1 << 16 { // integral floats
		addBoth(float64(r.Int64N(1 << 62)))
	}
	for range 1 << 20 { // random bit patterns
		add(math.Float64frombits(r.Uint64()))
	}
	c.print("encoding/json")
}

func sweepFloat32(r *rand.Rand) {
	c := newClass("float32")
	add := func(v float32) {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return
		}
		c.add(fmt.Sprintf("%08x", math.Float32bits(v)), enc(v), marshal(v), v == 0 && math.Signbit(f))
	}
	addBoth := func(v float32) {
		add(v)
		add(-v)
	}
	addBoth(0)
	for e := -46; e <= 39; e++ {
		p := float32(parse(1, e, 32))
		addBoth(p)
		addBoth(math.Nextafter32(p, 0))
		addBoth(math.Nextafter32(p, float32(math.Inf(1))))
	}
	for range 1 << 16 {
		addBoth(float32(parse(r.Int64N(10_000_000)+1, r.IntN(61)-30, 32)))
	}
	for range 1 << 20 {
		add(math.Float32frombits(r.Uint32()))
	}
	c.print("encoding/json")
}

func sweepIntegers(r *rand.Rand) {
	c := newClass("int64")
	add := func(v int64) {
		c.add(fmt.Sprintf("%016x", uint64(v)), enc(v), strconv.FormatInt(v, 10), false)
	}
	u := newClass("uint64")
	addU := func(v uint64) {
		u.add(fmt.Sprintf("%016x", v), enc(v), strconv.FormatUint(v, 10), false)
	}
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, math.MinInt64 + 1} {
		add(v)
	}
	for p := int64(1); p <= math.MaxInt64/10; p *= 10 {
		for _, v := range []int64{p - 1, p, p + 1} {
			add(v)
			add(-v)
			addU(uint64(v))
		}
	}
	addU(math.MaxUint64)
	for range 1 << 18 {
		add(int64(r.Uint64()))
		add(r.Int64N(1<<20) - 1<<19)
		addU(r.Uint64())
	}
	c.print("strconv")
	u.print("strconv")
}

func sweepStrings(r *rand.Rand) {
	c := newClass("string")
	frags := []string{"\"", "\\", "/", "<", ">", "&", "\x7f", "é", " ", " ", "\U0001F600", "\xff", "\xed\xa0\x80", "\xc3", "\xe2\x80"}
	for b := range 0x20 {
		frags = append(frags, string(rune(b)))
	}
	var sb strings.Builder
	for range 1 << 17 {
		n := r.IntN(130) // up to two 64-byte blocks
		if r.IntN(64) == 0 {
			n = r.IntN(1100)
		}
		sb.Reset()
		for sb.Len() < n {
			if r.IntN(4) == 0 {
				sb.WriteString(frags[r.IntN(len(frags))])
			} else {
				sb.WriteByte(byte(0x20 + r.IntN(0x5f)))
			}
		}
		s := sb.String()
		c.add(fmt.Sprintf("%x", s), enc(s), "", false)
	}
	c.print("")
}
