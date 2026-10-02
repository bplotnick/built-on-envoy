// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Configuration contract tests use the loader so schema and semantic validation
// remain inseparable for both filter-level and per-route callers.

package config

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_BasicEndpoint(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 90
        distribution:
          p0.0: "1ms"
          p50.0: "10ms"
          p99.0: "200ms"
      - status: 503
        resolution: 10
        distribution:
          p0.0: "50ms"
          p50.0: "100ms"
          p99.0: "500ms"
`

	cfg, err := loadFilterConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}

	ep := cfg.Endpoints[0]
	if ep.Match.Prefix != "/api/" {
		t.Errorf("expected prefix /api/, got %v", ep.Match.Prefix)
	}
	if len(ep.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(ep.Responses))
	}
	if ep.Responses[0].Status != 200 {
		t.Errorf("expected status 200, got %d", ep.Responses[0].Status)
	}
	if ep.Responses[0].Resolution != 90 {
		t.Errorf("expected resolution 90, got %d", ep.Responses[0].Resolution)
	}
	if ep.Responses[1].Status != 503 {
		t.Errorf("expected status 503, got %d", ep.Responses[1].Status)
	}
	if ep.Responses[1].Resolution != 10 {
		t.Errorf("expected resolution 10, got %d", ep.Responses[1].Resolution)
	}
	if cfg.ProbabilityDistribution != ProbabilityDistributionStateful {
		t.Errorf("expected default probability_distribution %q, got %q", ProbabilityDistributionStateful, cfg.ProbabilityDistribution)
	}
}

func TestLoadConfig_ProbabilityDistributionStateless(t *testing.T) {
	input := `
probability_distribution: stateless
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 100
        distribution:
          p0.0: "1ms"
          p100.0: "10ms"
`

	cfg, err := loadFilterConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ProbabilityDistribution != ProbabilityDistributionStateless {
		t.Fatalf("expected probability_distribution %q, got %q", ProbabilityDistributionStateless, cfg.ProbabilityDistribution)
	}
}

func TestLoadConfig_Diagnostic(t *testing.T) {
	input := `
diagnostic: true
responses:
  - status: 200
    resolution: 100
    distribution:
      p0.0: "1ms"
      p100.0: "10ms"
`

	cfg, err := loadFilterConfig([]byte(input))
	require.NoError(t, err)
	require.True(t, cfg.Diagnostic)
}

func TestLoadConfig_LocalResponse(t *testing.T) {
	valid := `responses:
  - status: 204
    resolution: 1
    distribution: {p0.0: "1ms"}
    local_response:
      body: ""
      headers:
        - {name: Set-Cookie, value: "a=1"}
        - {name: Set-Cookie, value: "b=2"}
        - {name: Content-Type, value: application/json}
`
	cfg, err := loadPerRouteConfig([]byte(valid))
	require.NoError(t, err)
	require.NotNil(t, cfg.Responses[0].LocalResponse)
	require.NotNil(t, cfg.Responses[0].LocalResponse.Body)
	require.Empty(t, *cfg.Responses[0].LocalResponse.Body)
	require.Len(t, cfg.Responses[0].LocalResponse.Headers, 3)

	for _, input := range []string{
		strings.Replace(valid, "local_response:\n", "local_response: null\n", 1),
		strings.Replace(valid, `body: ""`, "body: null", 1),
		strings.Replace(valid, `body: ""`, "body: 12", 1),
		strings.Replace(valid, "headers:\n", "headers: null\n", 1),
		strings.Replace(valid, "value: \"a=1\"", "value: 12", 1),
		strings.Replace(valid, "name: Set-Cookie", "name: null", 1),
		strings.Replace(valid, "name: Set-Cookie", "name: Content-Length", 1),
		strings.Replace(valid, "name: Set-Cookie", "name: x-fault-status", 1),
		strings.Replace(valid, "value: application/json", "value: \"bad\\nvalue\"", 1),
		strings.Replace(valid, "name: Content-Type, value: application/json", "name: Content-Type, value: ''", 1),
		strings.Replace(valid, "name: Content-Type, value: application/json", "name: Content-Type, value: ' \t'", 1),
		strings.Replace(valid, "name: Content-Type, value: application/json", "name: Content-Type, value: '\u00a0'", 1),
		strings.Replace(valid, "- {name: Content-Type, value: application/json}", "- {name: Content-Type, value: application/json}\n        - {name: content-type, value: text/plain}", 1),
		strings.Replace(valid, "- {name: Set-Cookie, value: \"a=1\"}", "- null", 1),
		strings.Replace(valid, `body: ""`, `body: content`, 1),
		strings.Replace(valid, "status: 204", "status: 199", 1),
	} {
		_, err := loadPerRouteConfig([]byte(input))
		require.Error(t, err, "input: %s", input)
	}
}

func TestLoadConfig_LocalResponseNestedBehavior(t *testing.T) {
	endpointConfig := `endpoints:
  - match: {prefix: /}
    responses:
      - status: 500
        resolution: 1
        distribution: {p0.0: 1ms}
        local_response: {body: custom}
`
	cfg, err := loadFilterConfig([]byte(endpointConfig))
	require.NoError(t, err)
	require.Equal(t, "custom", *cfg.Endpoints[0].Responses[0].LocalResponse.Body)

	loadConfig := `load_based:
  healthy:
    threshold_in_flight: 1
    responses:
      - status: 500
        resolution: 1
        distribution: {p0.0: 1ms}
        local_response: {body: healthy}
  tipping_point:
    threshold_in_flight: 2
    responses:
      - status: 503
        resolution: 1
        distribution: {p0.0: 2ms}
        local_response: {body: tipping}
`
	cfg, err = loadFilterConfig([]byte(loadConfig))
	require.NoError(t, err)
	require.Equal(t, "healthy", *cfg.LoadBased.Healthy.Responses[0].LocalResponse.Body)
	require.Equal(t, "tipping", *cfg.LoadBased.TippingPoint.Responses[0].LocalResponse.Body)
}

func TestLoadConfig_LocalResponseYAMLAliasesAndMerges(t *testing.T) {
	valid := `endpoints:
  - &endpoint
    match: {prefix: /one}
    responses: &responses
      - status: 500
        resolution: 1
        distribution: {p0.0: 1ms}
        local_response: &local
          body: &body '{"error":"down"}'
          headers:
            - {name: content-type, value: *body}
            - {name: x-extra, value: "ok"}
  - <<: *endpoint
    match: {prefix: /two}
    responses: *responses
`
	cfg, err := loadFilterConfig([]byte(valid))
	require.NoError(t, err)
	require.Len(t, cfg.Endpoints, 2)
	require.Equal(t, cfg.Endpoints[0].Responses[0].LocalResponse.Headers[0].Value, *cfg.Endpoints[0].Responses[0].LocalResponse.Body)

	validOverride := `responses:
  - status: 500
    resolution: 1
    distribution: {p0.0: 1ms}
    local_response:
      <<: &defaults {body: null, headers: null}
      body: ""
      headers: []
`
	_, err = loadPerRouteConfig([]byte(validOverride))
	require.NoError(t, err, "explicit fields must override invalid merged defaults")

	for _, invalid := range []string{
		`responses: &responses
  - &response {status: 500, resolution: 1, distribution: {p0.0: 1ms}, local_response: {body: null}}
`,
		`responses:
  - status: 500
    resolution: 1
    distribution: {p0.0: 1ms}
    local_response: {headers: [{name: x-test, value: 1}]}
`,
	} {
		_, err := loadPerRouteConfig([]byte(invalid))
		require.Error(t, err)
	}
}

func TestLoadConfig_InvalidProbabilityDistribution(t *testing.T) {
	input := `
probability_distribution: random
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 100
        distribution:
          p0.0: "1ms"
          p100.0: "10ms"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid probability_distribution")
	}
}

func TestLoadConfig_LoadBased(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    load_based:
      healthy:
        threshold_in_flight: 100
        responses:
          - status: 200
            resolution: 100
            distribution:
              p0.0: "1ms"
              p50.0: "5ms"
              p99.0: "50ms"
      tipping_point:
        threshold_in_flight: 500
        responses:
          - status: 200
            resolution: 50
            distribution:
              p0.0: "50ms"
              p50.0: "200ms"
              p99.0: "2s"
          - status: 503
            resolution: 50
            distribution:
              p0.0: "10ms"
              p50.0: "50ms"
              p99.0: "100ms"
      grey_zone:
        penalty_base: "10ms"
        spike_threshold: 0.8
        spike_penalty_duration: "5s"
        spike_penalty_multiplier: 2.0
        recovery_rate: 0.1
`

	cfg, err := loadFilterConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}

	ep := cfg.Endpoints[0]
	if ep.LoadBased == nil {
		t.Fatal("expected load_based config")
	}
	if ep.LoadBased.Healthy.ThresholdInFlight != 100 {
		t.Errorf("expected healthy threshold 100, got %v", ep.LoadBased.Healthy.ThresholdInFlight)
	}
	if ep.LoadBased.TippingPoint.ThresholdInFlight != 500 {
		t.Errorf("expected tipping_point threshold 500, got %v", ep.LoadBased.TippingPoint.ThresholdInFlight)
	}
	if ep.LoadBased.GreyZone == nil {
		t.Fatal("expected grey_zone config")
	}
	if ep.LoadBased.GreyZone.SpikeThreshold != 0.8 {
		t.Errorf("expected spike_threshold 0.8, got %v", ep.LoadBased.GreyZone.SpikeThreshold)
	}
}

func TestLoadConfig_ExactMatch(t *testing.T) {
	input := `
endpoints:
  - match:
      exact: "/health"
    responses:
      - status: 200
        resolution: 100
        distribution:
          p0.0: "0ms"
          p50.0: "1ms"
          p99.0: "5ms"
`

	cfg, err := loadFilterConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Endpoints[0].Match.Exact != "/health" {
		t.Errorf("expected exact /health, got %v", cfg.Endpoints[0].Match.Exact)
	}
}

func TestLoadConfig_HeaderMatch(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
      headers:
        - name: "x-env"
          exact_match: "staging"
    responses:
      - status: 200
        resolution: 100
        distribution:
          p0.0: "1ms"
          p50.0: "10ms"
          p99.0: "100ms"
`

	cfg, err := loadFilterConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := cfg.Endpoints[0]
	if len(ep.Match.Headers) != 1 {
		t.Fatalf("expected 1 header matcher, got %d", len(ep.Match.Headers))
	}
	if ep.Match.Headers[0].Name != "x-env" {
		t.Errorf("expected header name x-env, got %v", ep.Match.Headers[0].Name)
	}
	if ep.Match.Headers[0].ExactMatch != "staging" {
		t.Errorf("expected exact_match staging, got %v", ep.Match.Headers[0].ExactMatch)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	_, err := loadFilterConfig([]byte("{{not yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadConfig_NoResponsesOrLoadBased(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error when neither responses nor load_based is configured")
	}
}

func TestLoadConfig_InvalidStatusCode(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 999
        resolution: 100
        distribution:
          p0.0: "1ms"
          p50.0: "10ms"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid status code")
	}
}

func TestLoadConfig_MultipleValidationErrors(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 100
        distribution:
          pbad: "1ms"
          p50.0: "10ms"
  - match:
      prefix: "/otherAPI/"
    responses:
     - status: 200
       resolution: 100
       distribution:
         pbad: "1ms"
         p50.0: "10ms"
`

	_, err := loadFilterConfig([]byte(input))
	require.ErrorIs(t, err, strconv.ErrSyntax)
	var numberErr *strconv.NumError
	require.ErrorAs(t, err, &numberErr)
	var schemaErr *jsonschema.ValidationError
	require.NotErrorAs(t, err, &schemaErr)
	require.ErrorContains(t, err, "endpoint 0")
	require.ErrorContains(t, err, "endpoint 1")
}

func TestLoadConfig_ZeroResolution(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 0
        distribution:
          p0.0: "1ms"
          p50.0: "10ms"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for zero resolution")
	}
}

func TestLoadConfig_LoadBased_MissingHealthy(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    load_based:
      tipping_point:
        threshold_in_flight: 500
        responses:
          - status: 200
            resolution: 100
            distribution:
              p0.0: "1ms"
              p50.0: "10ms"
              p99.0: "100ms"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error when load_based.healthy is missing")
	}
}

func TestLoadConfig_LoadBased_InvalidThresholds(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    load_based:
      healthy:
        threshold_in_flight: 500
        responses:
          - status: 200
            resolution: 100
            distribution:
              p0.0: "1ms"
              p50.0: "10ms"
              p99.0: "100ms"
      tipping_point:
        threshold_in_flight: 100
        responses:
          - status: 200
            resolution: 100
            distribution:
              p0.0: "1ms"
              p50.0: "10ms"
              p99.0: "100ms"
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error when tipping_point threshold is less than healthy threshold")
	}
}

func TestParsePercentileDistribution(t *testing.T) {
	dist := map[string]string{
		"p0.0":  "1ms",
		"p50.0": "10ms",
		"p90.0": "50ms",
		"p99.0": "200ms",
	}

	percentiles, err := ParsePercentileDistribution(dist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(percentiles) != 4 {
		t.Fatalf("expected 4 percentiles, got %d", len(percentiles))
	}

	// Verify they're sorted by quantile.
	for i := 1; i < len(percentiles); i++ {
		if percentiles[i].Quantile <= percentiles[i-1].Quantile {
			t.Errorf("percentiles not sorted: %v", percentiles)
		}
	}

	// Verify values.
	expected := []struct {
		quantile float64
		duration time.Duration
	}{
		{0.00, 1 * time.Millisecond},
		{0.50, 10 * time.Millisecond},
		{0.90, 50 * time.Millisecond},
		{0.99, 200 * time.Millisecond},
	}

	for i, e := range expected {
		if percentiles[i].Quantile != e.quantile {
			t.Errorf("percentile %d: expected quantile %v, got %v", i, e.quantile, percentiles[i].Quantile)
		}
		if percentiles[i].Duration != e.duration {
			t.Errorf("percentile %d: expected duration %v, got %v", i, e.duration, percentiles[i].Duration)
		}
	}
}

func TestParsePercentileDistribution_InvalidKey(t *testing.T) {
	dist := map[string]string{
		"p50.0":   "10ms",
		"invalid": "100ms",
	}

	_, err := ParsePercentileDistribution(dist)
	if err == nil {
		t.Fatal("expected error for invalid percentile key")
	}
}

func TestParsePercentileDistribution_InvalidDuration(t *testing.T) {
	dist := map[string]string{
		"p50.0": "not-a-duration",
	}

	_, err := ParsePercentileDistribution(dist)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestParsePercentileDistribution_Empty(t *testing.T) {
	_, err := ParsePercentileDistribution(map[string]string{})
	if err == nil {
		t.Fatal("expected error for empty distribution")
	}
}

func TestParsePercentileDistribution_NegativeDuration(t *testing.T) {
	dist := map[string]string{
		"p50.0": "-10ms",
	}

	_, err := ParsePercentileDistribution(dist)
	if err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestParsePercentileDistribution_NonDecreasing(t *testing.T) {
	dist := map[string]string{
		"p0.0":  "100ms",
		"p50.0": "50ms",
		"p99.0": "200ms",
	}

	_, err := ParsePercentileDistribution(dist)
	if err == nil {
		t.Fatal("expected error for non-monotonic distribution")
	}
}

func TestMutuallyExclusiveConfigurationOfStandardAndLoadBasedConfig(t *testing.T) {
	input := `
endpoints:
  - match:
      prefix: "/api/"
    responses:
      - status: 200
        resolution: 90
        distribution:
          p0.0: "1ms"
          p50.0: "10ms"
          p99.0: "200ms"
      - status: 503
        resolution: 10
        distribution:
          p0.0: "50ms"
          p50.0: "100ms"
          p99.0: "500ms"
    load_based:
      healthy:
        threshold_in_flight: 100
        responses:
        - status: 200
          resolution: 100
          distribution:
            p0.0: "1ms"
            p50.0: "5ms"
            p99.0: "50ms"
      tipping_point:
          threshold_in_flight: 500
          responses:
          - status: 200
            resolution: 50
            distribution:
              p0.0: "50ms"
              p50.0: "200ms"
              p99.0: "2s"
          - status: 503
            resolution: 50
            distribution:
              p0.0: "10ms"
              p50.0: "50ms"
              p99.0: "100ms"
      grey_zone:
        penalty_base: "10ms"
        spike_threshold: 0.8
        spike_penalty_duration: "5s"
        spike_penalty_multiplier: 2.0
        recovery_rate: 0.1
`

	_, err := loadFilterConfig([]byte(input))
	if err == nil {
		t.Fatalf("unexpected successful load of config: %v", err)
	}

	var schemaErr *jsonschema.ValidationError
	require.ErrorAs(t, err, &schemaErr)
}

func TestLoadConfig_JSONFromStruct(t *testing.T) {
	// When using google.protobuf.Struct, Envoy serializes the config as JSON
	// before passing it to the module. Verify that our YAML parser handles
	// this correctly.
	jsonInput := `{"endpoints":[{"match":{"prefix":"/api/"},"responses":[{"status":200,"resolution":900,"distribution":{"p0.0":"1ms","p50.0":"10ms","p100.0":"100ms"}},{"status":503,"resolution":100,"distribution":{"p0.0":"50ms","p100.0":"500ms"}}]}]}`

	cfg, err := loadFilterConfig([]byte(jsonInput))
	if err != nil {
		t.Fatalf("unexpected error parsing JSON input: %v", err)
	}

	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}

	ep := cfg.Endpoints[0]
	if ep.Match.Prefix != "/api/" {
		t.Errorf("expected prefix /api/, got %q", ep.Match.Prefix)
	}
	if len(ep.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(ep.Responses))
	}
	if ep.Responses[0].Status != 200 {
		t.Errorf("expected status 200, got %d", ep.Responses[0].Status)
	}
	if ep.Responses[0].Resolution != 900 {
		t.Errorf("expected resolution 900, got %d", ep.Responses[0].Resolution)
	}
	if ep.Responses[0].Distribution["p50.0"] != "10ms" {
		t.Errorf("expected p50.0=10ms, got %q", ep.Responses[0].Distribution["p50.0"])
	}
	if ep.Responses[1].Status != 503 {
		t.Errorf("expected status 503, got %d", ep.Responses[1].Status)
	}
}

func TestLoadConfig_RejectsUnknownFields(t *testing.T) {
	for _, input := range []string{
		`diagnostics: true`,
		`endpoints: [{mach: {prefix: /api}, responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]}]`,
		`endpoints: [{match: {prefixx: /api}, responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]}]`,
		`responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}, extra: true}]`,
	} {
		t.Run(input, func(t *testing.T) {
			for _, parse := range []func([]byte) (*FilterConfig, error){loadFilterConfig, loadPerRouteConfig} {
				cfg, err := parse([]byte(input))
				var schemaErr *jsonschema.ValidationError
				require.ErrorAs(t, err, &schemaErr)
				require.Nil(t, cfg)
			}
		})
	}
}

func TestLoadConfig_LoadBasedRequiresResponses(t *testing.T) {
	for _, tier := range []string{"healthy", "tipping_point"} {
		for _, empty := range []string{"", "responses: null", "responses: []"} {
			t.Run(tier+"/"+empty, func(t *testing.T) {
				response := `responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]`
				healthy, tipping := response, response
				if tier == "healthy" {
					healthy = empty
				} else {
					tipping = empty
				}
				input := "load_based:\n  healthy:\n    threshold_in_flight: 10\n    " + healthy + "\n  tipping_point:\n    threshold_in_flight: 100\n    " + tipping + "\n"
				for _, parse := range []func([]byte) (*FilterConfig, error){loadFilterConfig, loadPerRouteConfig} {
					cfg, err := parse([]byte(input))
					var schemaErr *jsonschema.ValidationError
					require.ErrorAs(t, err, &schemaErr)
					require.Nil(t, cfg)
				}
			})
		}
	}
}

func TestLoadConfig_EmptyMatchIsPreserved(t *testing.T) {
	cfg, err := loadFilterConfig([]byte(`endpoints: [{match: {}, responses: [{status: 200, resolution: 1, distribution: {p0.0: 1ms}}]}]`))
	require.NoError(t, err)
	require.Equal(t, MatchConfig{}, cfg.Endpoints[0].Match)
}
