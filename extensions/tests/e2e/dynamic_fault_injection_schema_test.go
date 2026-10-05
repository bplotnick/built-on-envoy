// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package integration verifies that schema-validated YAML and its load thresholds
// reach the running Envoy module and select the configured response.
package integration

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/tetratelabs/built-on-envoy/internal/testing"
)

func TestDynamicFaultInjectionLoadBasedSchemaConfiguration(t *testing.T) {
	config := `load_based:
  healthy:
    threshold_in_flight: 1
    responses:
      - status: 503
        resolution: 1
        distribution: {p0.0: 10ms, p100.0: 10ms}
        local_response:
          body: "loaded via threshold_in_flight"
          headers: [{name: x-schema-input, value: yaml}]
  tipping_point:
    threshold_in_flight: 2
    responses:
      - status: 504
        resolution: 1
        distribution: {p0.0: 10ms, p100.0: 10ms}
`
	proxyPort := startDynamicFaultInjectionEnvoy(t, config)
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/status/200", proxyPort), nil)
	require.NoError(t, err)

	internaltesting.RequireEventuallyRequestWithTiming(t, req, func(c *assert.CollectT, resp *http.Response, _ time.Duration) bool {
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		require.Equal(c, http.StatusServiceUnavailable, resp.StatusCode)
		require.Equal(c, "503", resp.Header.Get("x-fault-status"))
		require.Equal(c, "yaml", resp.Header.Get("x-schema-input"))
		require.Equal(c, "loaded via threshold_in_flight", string(body))
		return true
	})
}
