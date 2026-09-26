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

// Package sd2 is throwaway code for spike S-D2 (port plan sections 6.6 and
// 7 W4.3): how the typed decode of [typesafe.DecodeAs] stores each answer
// into its struct field, measured three ways over the same lookups and
// checks.
//
//   - [DecodeAddr], variant 1, the store as built: the field's address as an
//     interface ([reflect.Value.Addr], [reflect.Value.Interface]),
//     type-asserted to the answer type's pointer.
//   - [DecodeOffset], variant 2: the field's [reflect.StructField.Offset],
//     taken once per type, and a store through [unsafe.Add] of the struct's
//     address.
//   - [DecodeSet], variant 3: [reflect.Value.Set] of the field with the
//     answer as a [reflect.Value].
//
// The three are replicas of the root package's typedPlan.decode: the same
// plan cache keyed by [reflect.Type], the same [wire.Answers.Get] lookup,
// kind check and option and level checks, on answer types laid out as the
// root package's ([NoulAnswer], [ChoiceAnswer], [ScoreAnswer]), which the
// replicas can build and the root's cannot be built outside it. The tests
// measure [typesafe.DecodeAs] itself beside them, over the same fixture and
// question set, as the anchor that shows the replica of variant 1 costs
// what the production code costs.
//
// The package also measures the first [typesafe.PreparedFor] call for a
// type (W4.3 deliverable B): see TestPreparedForFirstCall.
//
// The leading underscore of the directory keeps the package out of ./...,
// CI, golangci-lint and coverage, and out of the seam test's unsafe rule.
// Run it with an explicit path:
//
//	go test ./_spikes/w4.3/
//
// Nothing here is production code.
package sd2
