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

package engine

import "github.com/zchee/typesafe-sdk-go/internal/wire"

// Accessors for internal/alloctest, which holds the root package's public
// values (aliases of this package's types) and measures the stages that
// build them. They are functions, not methods: a method of an aliased type
// would join the root package's public API.

// ConfigOf returns c's configuration.
func ConfigOf(c *Client) *config { return c.cfg.config }

// SystemOneEndpointOf returns how c's errors name the System One endpoint.
func SystemOneEndpointOf(c *Client) string { return c.systemOneEndpoint }

// ResponseMetaOf returns r's HTTP metadata.
func ResponseMetaOf(r *SystemOneResponse) *wire.ResponseMeta { return &r.meta }

// ResponseResultOf returns r's decoded result.
func ResponseResultOf(r *SystemOneResponse) *wire.SystemOneResult { return &r.res }

// WireOf returns the bytes and tables of p, or nil for a nil set.
func WireOf(p *Prepared) *wire.Prepared {
	if p == nil {
		return nil
	}
	return &p.w
}
