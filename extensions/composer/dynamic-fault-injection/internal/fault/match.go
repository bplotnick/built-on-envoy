// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fault

import (
	"strings"

	"github.com/tetratelabs/built-on-envoy/extensions/composer/dynamic-fault-injection/internal/config"
)

// HeaderGetter provides read access to headers.
type HeaderGetter interface {
	GetOne(name string) string
}

// MatchRoute checks if a request matches the route's match configuration.
func MatchRoute(match config.MatchConfig, path string, headers HeaderGetter) bool {
	// Check path matching.
	if match.Prefix != "" {
		if !strings.HasPrefix(path, match.Prefix) {
			return false
		}
	}
	if match.Exact != "" {
		// Strip query string for exact matching.
		requestPath := path
		if idx := strings.Index(requestPath, "?"); idx != -1 {
			requestPath = requestPath[:idx]
		}
		if requestPath != match.Exact {
			return false
		}
	}

	// Check header matching.
	for _, hm := range match.Headers {
		value := headers.GetOne(hm.Name)
		if hm.PresentMatch {
			if value == "" {
				return false
			}
		} else if hm.ExactMatch != "" {
			if value != hm.ExactMatch {
				return false
			}
		}
	}

	return true
}
