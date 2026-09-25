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

// Package sd1 is throwaway code for spike S-D1 (port plan section 6.2.9): three
// candidate response decoders over {model, usage, answers} bodies, measured
// against every fixture under testdata.
//
//   - [VariantA1]: an [ast.Preorder] visitor that skips nothing, then a
//     [decoder.Skip] trailing-data check, then one lazy second pass over the
//     root for structured legends.
//   - [VariantA2]: the same visitor and lazy pass, with a [sonic.ValidString]
//     pre-pass instead of the trailing-data check.
//   - [VariantB]: [sonic.UnmarshalString] into a map of
//     [sonic.NoCopyRawMessage], then the same visitor over each top-level
//     member, and the lazy pass over the answers member.
//
// The leading underscore of the directory keeps the package out of ./...,
// CI, golangci-lint and coverage. Run it with an explicit path:
//
//	go test ./_spikes/s-d1/
//
// Nothing here is production code; W2.0 writes the real decoder from the
// winner.
//
// [ast.Preorder]: https://pkg.go.dev/github.com/bytedance/sonic/ast#Preorder
// [decoder.Skip]: https://pkg.go.dev/github.com/bytedance/sonic/decoder#Skip
// [sonic.ValidString]: https://pkg.go.dev/github.com/bytedance/sonic#ValidString
// [sonic.UnmarshalString]: https://pkg.go.dev/github.com/bytedance/sonic#UnmarshalString
// [sonic.NoCopyRawMessage]: https://pkg.go.dev/github.com/bytedance/sonic#NoCopyRawMessage
package sd1
