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

// Package sc1 is throwaway code for spike S-C1 (port plan sections 1.2 NF3
// and NF5, 6.1 to 6.3, 8 AC-P5 and AC-P6): a prototype of one whole System
// One call, made the way the future Client.SystemOne will make it with what
// exists today, and the AC-P5 memory probe of its body read.
//
// One call ([Client.SystemOne]) runs these stages, each a method so that a
// test can measure it alone:
//
//  1. encode: the body {"state":…,"model":…,"questions":…} into a pooled
//     codec.Body (plan 6.1.2), the state through sonic's encoder.EncodeInto;
//  2. request: a reader over the body, GetBody, ContentLength, the header
//     template, a per-attempt context.WithTimeout (plan 6.3);
//  3. round trip: the http.RoundTripper (testsupport.Recorder in the tests);
//  4. read: the body under the NF5 rules (initial buffer
//     min(Content-Length, 256 KiB), doubling under a 16 MiB cap, a declared
//     length above the cap refused before any read, an undeclared body
//     stopped at cap + 1);
//  5. decode: the S-D1 winner a1 (the Preorder visitor, the decoder.Skip
//     trailing check, the lazy pass) from _spikes/s-d1 into wire.Answers;
//  6. finish: cancel the attempt's context, close the response body, drop
//     the call's reference to the request body.
//
// [NaiveClient] is the comparator of AC-P6: encoding/json, http.NewRequest
// over a bytes.Reader, io.ReadAll and a map[string]any.
//
// The leading underscore of the directory keeps the package out of ./...,
// CI, golangci-lint, coverage and the seam test. Run it with an explicit
// path:
//
//	go test ./_spikes/s-c1/
//
// Nothing here is production code; W2.3 writes the real call.
package sc1
