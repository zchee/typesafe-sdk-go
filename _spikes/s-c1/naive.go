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

package sc1

import (
	"bytes"
	"context"
	// The naive comparator uses encoding/json on purpose: it is what a
	// straightforward client would write. The seam rule (NF6: JSON only
	// through internal/codec, sonic only there and in
	// internal/testsupport/naive) does not apply under _spikes/, which is
	// outside ./... and the seam test.
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"time"
)

// naiveBody is the request body as a straightforward client declares it.
type naiveBody struct {
	State     any             `json:"state"`
	Model     string          `json:"model"`
	Questions json.RawMessage `json:"questions"`
}

// NaiveClient is the AC-P6 comparator: the same call written the obvious
// way. It sends the same headers and has the same per-attempt deadline as
// [Client], and calls the same RoundTripper directly (no http.Client), so
// the two differ only in how they encode, build, read and decode.
type NaiveClient struct {
	rt        http.RoundTripper
	url       string
	auth      string
	model     string
	questions json.RawMessage
	timeout   time.Duration
}

// NewNaiveClient returns a NaiveClient posting questions, the compact JSON
// of a question set, through rt.
func NewNaiveClient(rt http.RoundTripper, questions []byte) *NaiveClient {
	return &NaiveClient{
		rt:        rt,
		url:       "https://api.typesafe.ai/v1/systemone",
		auth:      "Bearer " + placeholderKey,
		model:     "jev-latest",
		questions: questions,
		timeout:   10 * time.Second,
	}
}

// SystemOne makes one call: encoding/json Marshal of the body, a request
// over a bytes.Reader, io.ReadAll of the response and encoding/json
// Unmarshal into a map[string]any.
func (c *NaiveClient) SystemOne(ctx context.Context, state any) (map[string]any, error) {
	b, err := json.Marshal(naiveBody{State: state, Model: c.model, Questions: c.questions})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-TypeSafe-SDK", userAgent)
	req.Header.Set("X-TypeSafe-Runtime", "go/"+runtime.Version())
	req.Header.Set("X-TypeSafe-Retry-Count", "0")
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &StatusError{Status: resp.StatusCode}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
