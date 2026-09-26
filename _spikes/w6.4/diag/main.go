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

// Command diag sends examples/options' first System One request once and
// prints the API's answer, with the key replaced by *** (ledger W6.4-07).
// It ran three times, 2026-09-27 03:28:00-03:28:19 JST: as the example
// stood (a raw question with "weight": 2 and ExtraBody("beam_width", 4):
// 400 api_usage_error), without both (200), and as here, with the extra
// body member alone (400). It reads TYPESAFE_API_KEY from the environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

func main() {
	key := strings.TrimSpace(os.Getenv(typesafe.APIKeyEnv))
	scrub := func(s string) string { return strings.ReplaceAll(s, key, "***") }
	client, err := typesafe.NewClient(
		typesafe.WithModel(typesafe.DefaultModel),
		typesafe.WithTimeout(30*time.Second),
		typesafe.WithRetry(typesafe.NoRetry()),
		typesafe.WithHeader("X-Team", "support"),
		typesafe.WithUserAgentProduct("options-example/1.0"),
	)
	if err != nil {
		fmt.Println(scrub(err.Error()))
		return
	}
	defer client.Close()
	qs, err := typesafe.NewQuestions().
		Choice("topic", typesafe.Choice{Instructions: typesafe.Text("What is the message about?"), Options: typesafe.Options{{Label: "billing", Description: typesafe.Text("payments or invoices")}, {Label: "other"}}}).
		Score("urgency", typesafe.Score{Levels: []typesafe.Content{typesafe.Text("low"), typesafe.Text("high")}}).
		Raw("refund", typesafe.RawQuestion{Type: "noul", Fields: map[string]any{"instructions": "Does the customer ask for a refund?"}}).
		Prepare()
	if err != nil {
		fmt.Println(err)
		return
	}
	_, err = client.SystemOne(context.Background(), "I was charged twice.", qs,
		typesafe.Timeout(20*time.Second), typesafe.Retry(typesafe.NoRetry()),
		typesafe.Header("X-Call", "options-example"), typesafe.ExtraBody("beam_width", 4))
	if apiErr, ok := errors.AsType[*typesafe.APIError](err); ok {
		fmt.Printf("status %d kind %v error_type %q\nbody: %s\n", apiErr.StatusCode, apiErr.Kind, apiErr.ErrorType, scrub(string(apiErr.Body)))
		return
	}
	fmt.Println("result:", err)
}
