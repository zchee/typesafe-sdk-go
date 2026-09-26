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

// Command k41 reproduces W5.3 fuzz finding 4 (c) (ruling K41): sonic's
// native scanner, reached through ast.Preorder, takes a string that runs to
// the end of the input without its closing quote as a complete string when
// its content is a multiple of 32 bytes long, and reports an error for every
// other length. The first part uses sonic alone, for the upstream issue; the
// second shows what this module's decoder does with the same shapes.
//
// Run from the module root: go run ./_spikes/w5.3/k41
package main

import (
	"encoding/json" // json.Number, the type sonic's ast.Visitor takes
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	sonicdecoder "github.com/bytedance/sonic/decoder"

	"github.com/zchee/typesafe-sdk-go/internal/codec"
	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// recorder records the strings, keys included, an ast.Preorder traversal
// reports; the other callbacks accept everything.
type recorder struct{ strs []string }

func (r *recorder) OnNull() error                        { return nil }
func (r *recorder) OnBool(bool) error                    { return nil }
func (r *recorder) OnString(s string) error              { r.strs = append(r.strs, s); return nil }
func (r *recorder) OnInt64(int64, json.Number) error     { return nil }
func (r *recorder) OnFloat64(float64, json.Number) error { return nil }
func (r *recorder) OnObjectBegin(int) error              { return nil }
func (r *recorder) OnObjectKey(k string) error           { r.strs = append(r.strs, k); return nil }
func (r *recorder) OnObjectEnd() error                   { return nil }
func (r *recorder) OnArrayBegin(int) error               { return nil }
func (r *recorder) OnArrayEnd() error                    { return nil }

func preorder(s string) string {
	var r recorder
	err := ast.Preorder(s, &r, &ast.VisitorOptions{OnlyNumber: true})
	got := "no string"
	for _, x := range r.strs {
		if strings.HasPrefix(x, "0") || x == "" && strings.HasPrefix(s, `""`) {
			got = fmt.Sprintf("string of %d bytes", len(x))
		}
	}
	return fmt.Sprintf("%s, err=%v", got, err)
}

func main() {
	fmt.Printf("sonic v1.15.4 (go.mod), %s %s/%s\n\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	fmt.Println("Part 1, sonic alone: an unterminated string at the end of the input, content n zeros.")
	fmt.Println("A correct scanner reports an error for every n; ERR_EOF-free success is the bug.")
	for _, shape := range []struct{ name, prefix string }{
		{"top-level", `"`},
		{"object member value", `{"":"`},
	} {
		fmt.Printf("\n%s, input %s + n zeros, end of input:\n", shape.name, shape.prefix)
		for _, n := range []int{0, 1, 31, 32, 33, 63, 64, 65, 96, 128} {
			in := shape.prefix + strings.Repeat("0", n)
			var v any
			uerr := sonic.UnmarshalString(in, &v)
			start, end := sonicdecoder.Skip([]byte(in))
			fmt.Printf("  n=%3d  ast.Preorder: %-60s  sonic.UnmarshalString err=%v  decoder.Skip=(%d,%d)\n", n, preorder(in), uerr, start, end)
		}
	}

	fmt.Println("\nPart 2, this module: codec.DecodeSystemOne, and wire.AppendJSON (the error-body path's")
	fmt.Println("validator), on the finding's body and its neighbours; each must be refused.")
	for _, in := range []string{
		`{"":"` + strings.Repeat("0", 64) + `}`, // W5.3-16's input
		`{"":"` + strings.Repeat("0", 63) + `}`, // the string with the brace: 64 bytes
		`{"":"` + strings.Repeat("0", 64),
		`"` + strings.Repeat("0", 32),
		`{"model":"m","answers":{},"usage":{},"x":"` + strings.Repeat("0", 32) + `}`,
	} {
		var dst wire.SystemOneResult
		_, err := codec.DecodeSystemOne([]byte(in), nil, "m", &dst)
		_, jerr := wire.AppendJSON(nil, []byte(in))
		verdict := "REFUSED"
		if err == nil {
			verdict = "ACCEPTED"
		}
		var se *wire.SyntaxError
		fmt.Printf("  len=%3d  DecodeSystemOne %s (%v)  AppendJSON syntax error: %t\n", len(in), verdict, err, errors.As(jerr, &se))
	}
}
