// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// These tests prove the shared HTTP timing helper retries assertions from its collector.
package internaltesting

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireEventuallyRequestWithTiming_RetriesAssertions(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	RequireEventuallyRequestWithTiming(t, req, func(c *assert.CollectT, response *http.Response, duration time.Duration) bool {
		require.Equal(c, http.StatusOK, response.StatusCode)
		require.Positive(c, duration)
		return true
	})
	require.EqualValues(t, 2, attempts.Load())
}
