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

// Command sonic_probe prints what sonic's encoder.EncodeInto writes with the
// options internal/codec uses (0) for the values behind rulings R46 to R49:
// the float set of R42 ("floats"), and control characters, invalid UTF-8,
// NaN and the infinities, unsupported kinds, nil containers, []byte and
// json.RawMessage ("misc"), and cyclic values ("cycmap", "cycptr",
// "cycslice", which sonic stops at its nesting limit). The leading
// underscore of _spikes keeps it out of ./...; run it with an explicit path:
//
//	GOEXPERIMENT=nosimd,noruntimesecret go run ./_spikes/w1.2/sonic_probe floats
//
// results/sonic-*-M.txt are its runs on (M).
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/bytedance/sonic/encoder"
)

func enc(v any) string {
	var buf []byte
	if err := encoder.EncodeInto(&buf, v, 0); err != nil {
		return fmt.Sprintf("ERR %T: %v", err, err)
	}
	return string(buf)
}

type node struct {
	Next *node `json:"next"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sonic_probe floats|misc|cycmap|cycptr|cycslice")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "floats":
		vals := []float64{1e-5, 9.99e-6, 1e-4, 0.1, 1.5, 3.0, -7.0, 9999999999999998, 1e16, 1e20, 1e21, 1e22, 5e-324, 1.7976931348623157e308, math.Copysign(0, -1), 0.0, float64(1<<53 + 1), 12345678901234567168.0}
		for _, v := range vals {
			fmt.Printf("%s\t%s\n", strconv.FormatFloat(v, 'g', -1, 64), enc(v))
		}
	case "misc":
		ctl := make([]byte, 0, 64)
		for c := range 0x20 {
			ctl = append(ctl, byte(c))
		}
		s := string(ctl) + "\x7f<>&\"\\/" + "é  \U0001F600"
		fmt.Println("ctl:", enc(s))
		fmt.Printf("invalid utf8: %q\n", enc("a\xffb"))
		fmt.Printf("lone surrogate bytes: %q\n", enc("a\xed\xa0\x80b"))
		fmt.Println("nan:", enc(math.NaN()))
		fmt.Println("inf:", enc(math.Inf(1)))
		fmt.Println("f32 nan:", enc(float32(math.NaN())))
		fmt.Println("nan nested:", enc(map[string]any{"a": []any{math.Inf(-1)}}))
		fmt.Println("chan:", enc(make(chan int)))
		fmt.Println("func:", enc(func() {}))
		fmt.Println("complex:", enc(complex(1, 2)))
		fmt.Println("map[bool]int:", enc(map[bool]int{true: 1}))
		fmt.Println("map[int]string:", enc(map[int]string{3: "x"}))
		fmt.Println("nil map:", enc(map[string]any(nil)))
		fmt.Println("nil slice:", enc([]any(nil)))
		fmt.Println("nil ptr:", enc((*node)(nil)))
		fmt.Println("nil any:", enc(nil))
		fmt.Println("bytes:", enc([]byte(`{"a":1}`)))
		fmt.Printf("rawmsg ws: %q\n", enc(json.RawMessage(" {\"a\" : 1} ")))
		fmt.Println("rawmsg bad:", enc(json.RawMessage(`{"a":`)))
		fmt.Println("rawmsg scalar:", enc(json.RawMessage(`3`)))
		fmt.Println("json.Number:", enc(json.Number("12")))
		fmt.Println("uint64 max:", enc(uint64(math.MaxUint64)))
		fmt.Println("int8:", enc(int8(-3)))
		fmt.Println("f32 0.1:", enc(float32(0.1)))
	case "cycmap":
		m := map[string]any{}
		m["self"] = m
		fmt.Println("cyclic map:", enc(m))
	case "cycptr":
		n := &node{}
		n.Next = n
		fmt.Println("cyclic ptr:", enc(n))
	case "cycslice":
		s := []any{nil}
		s[0] = s
		fmt.Println("cyclic slice:", enc(s))
	default:
		fmt.Fprintln(os.Stderr, "unknown probe", os.Args[1])
		os.Exit(2)
	}
}
