// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package impl

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	internaltesting "github.com/tetratelabs/built-on-envoy/extensions/composer/internal/testing"
)

func TestConfigSchema(t *testing.T) {
	t.Run("valid full config", func(t *testing.T) {
		internaltesting.AssertSchemaValid(t, "config.schema.json", `
			{
  "endpoints": [
				    {
				      "match": {
				        "prefix": "/api/"
				      },
				      "responses": [
				        {
				          "status": 200,
				          "resolution": 90,
				          "distribution": {
				            "p0.0": "1ms",
				            "p50.0": "10ms",
				            "p99.0": "200ms"
				          }
				        },
				        {
				          "status": 503,
				          "resolution": 10,
				          "distribution": {
				            "p0.0": "50ms",
				            "p50.0": "100ms",
				            "p99.0": "500ms"
				          }
				        }
				      ]
					}],
					"diagnostic": true
			}`)
	})

	t.Run("valid direct per-route config", func(t *testing.T) {
		internaltesting.AssertSchemaValid(t, "config.schema.json", `
{
  "responses": [
    {
      "status": 200,
      "resolution": 100,
      "distribution": {
        "p0.0": "1ms",
        "p100.0": "10ms"
      }
    }
  ]
}`)
	})
}

func TestConfigSchema_ExclusiveBehavior(t *testing.T) {
	responses := []any{map[string]any{"status": 200, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}}}
	loadBased := map[string]any{
		"healthy":       map[string]any{"threshold_in_flight": 10, "responses": responses},
		"tipping_point": map[string]any{"threshold_in_flight": 100, "responses": responses},
	}
	for _, endpoint := range []bool{true, false} {
		for _, behavior := range []map[string]any{
			{"responses": responses}, {"load_based": loadBased}, {"responses": responses, "load_based": loadBased}, {},
		} {
			t.Run(stringMustMarshalSchemaTest(t, behavior), func(t *testing.T) {
				valid := len(behavior) == 1
				if endpoint {
					behavior["match"] = map[string]string{"prefix": "/"}
					behavior = map[string]any{"endpoints": []any{behavior}}
				}
				data := stringMustMarshalSchemaTest(t, behavior)
				if valid {
					internaltesting.AssertSchemaValid(t, "config.schema.json", data)
				} else {
					internaltesting.AssertSchemaInvalid(t, "config.schema.json", data)
				}
			})
		}
	}
}

func TestConfigSchema_OptionalMatchAndRootBehavior(t *testing.T) {
	internaltesting.AssertSchemaValid(t, "config.schema.json", `{"endpoints":[{"responses":[{"status":200,"resolution":1,"distribution":{"p0.0":"1ms"}}]}]}`)
	internaltesting.AssertSchemaValid(t, "config.schema.json", `{"endpoints":[]}`)
	internaltesting.AssertSchemaInvalid(t, "config.schema.json", `{}`)
}

func TestConfigSchema_LocalResponse(t *testing.T) {
	response := map[string]any{
		"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"},
		"local_response": map[string]any{
			"body": `{"error":"down"}`,
			"headers": []any{
				map[string]string{"name": "content-type", "value": "application/problem+json"},
				map[string]string{"name": "set-cookie", "value": "a=1"},
				map[string]string{"name": "set-cookie", "value": "b=2"},
			},
		},
	}
	validConfigs := []map[string]any{
		{"responses": []any{response}},
		{"endpoints": []any{map[string]any{"match": map[string]string{"prefix": "/"}, "responses": []any{response}}}},
		{"load_based": map[string]any{
			"healthy":       map[string]any{"threshold_in_flight": 1, "responses": []any{response}},
			"tipping_point": map[string]any{"threshold_in_flight": 2, "responses": []any{response}},
		}},
	}
	for _, config := range validConfigs {
		internaltesting.AssertSchemaValid(t, "config.schema.json", stringMustMarshalSchemaTest(t, config))
	}

	for _, badResponse := range []map[string]any{
		{"status": 199, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}},
		{"status": 204, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"body": "not empty"}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": nil},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"body": 1}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "bad name", "value": "x"}}}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "x-test", "value": "line\nbreak"}}}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "content-type", "value": "application/json"}, map[string]string{"name": "Content-Type", "value": "text/plain"}}}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "content-type", "value": ""}}}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "content-type", "value": " \t"}}}},
		{"status": 503, "resolution": 1, "distribution": map[string]string{"p0.0": "1ms"}, "local_response": map[string]any{"headers": []any{map[string]string{"name": "content-type", "value": "\u00a0"}}}},
	} {
		internaltesting.AssertSchemaInvalid(t, "config.schema.json", stringMustMarshalSchemaTest(t, map[string]any{"responses": []any{badResponse}}))
	}
}

func stringMustMarshalSchemaTest(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
