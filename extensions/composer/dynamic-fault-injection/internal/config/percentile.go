// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Percentile parsing is shared by configuration checks and runtime distributions
// so both interpret configured latency values consistently.

package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Percentile represents a quantile-duration pair in a distribution.
type Percentile struct {
	Quantile float64
	Duration time.Duration
}

// ParsePercentileDistribution parses a map of percentile keys (e.g., "p0.0", "p50.0", "p99.9")
// to duration strings into a sorted slice of Percentiles.
func ParsePercentileDistribution(dist map[string]string) ([]Percentile, error) {
	if len(dist) == 0 {
		return nil, fmt.Errorf("distribution must have at least one entry")
	}

	var result []Percentile
	for key, durStr := range dist {
		quantile, err := parsePercentileKey(key)
		if err != nil {
			return nil, err
		}
		dur, err := time.ParseDuration(durStr)
		if err != nil {
			return nil, fmt.Errorf("invalid duration for %q: %w", key, err)
		}
		if dur < 0 {
			return nil, fmt.Errorf("negative duration for %q: %v", key, dur)
		}
		result = append(result, Percentile{Quantile: quantile, Duration: dur})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Quantile < result[j].Quantile
	})

	// Validate that durations are non-decreasing.
	for i := 1; i < len(result); i++ {
		if result[i].Duration < result[i-1].Duration {
			return nil, fmt.Errorf("distribution values must be non-decreasing: p%.1f (%v) < p%.1f (%v)",
				result[i].Quantile*100, result[i].Duration,
				result[i-1].Quantile*100, result[i-1].Duration)
		}
	}

	return result, nil
}

// parsePercentileKey parses keys like "p0.0", "p50.0", "p99.9", "p100.0"
// into a quantile value between 0.0 and 1.0.
func parsePercentileKey(key string) (float64, error) {
	if !strings.HasPrefix(key, "p") {
		return 0, fmt.Errorf("percentile key must start with 'p', got %q", key)
	}
	numStr := key[1:]
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid percentile key %q: %w", key, err)
	}
	if val < 0 || val > 100 {
		return 0, fmt.Errorf("percentile key %q: value must be between 0 and 100", key)
	}
	return val / 100.0, nil
}
