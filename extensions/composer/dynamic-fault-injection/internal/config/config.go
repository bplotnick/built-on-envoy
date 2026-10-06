// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Configuration types and semantic checks live together because schema rules
// cannot express all relationships between load thresholds and latency values.

package config

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
	"gopkg.in/yaml.v3"
)

// FilterConfig is the top-level configuration for the latency/fault filter.
type FilterConfig struct {
	Endpoints               []EndpointConfig     `yaml:"endpoints"`
	ProbabilityDistribution string               `yaml:"probability_distribution,omitempty"`
	Diagnostic              bool                 `yaml:"diagnostic,omitempty"`
	Responses               []StatusDistribution `yaml:"responses,omitempty"`
	LoadBased               *LoadBasedConfig     `yaml:"load_based,omitempty"`
}

// Source identifies where a configuration was supplied.
type Source int

const (
	// FilterSource identifies filter-level configuration.
	FilterSource Source = iota
	// PerRouteSource identifies configuration attached to an Envoy route.
	PerRouteSource
)

const (
	// ProbabilityDistributionStateful selects precomputed latency samples.
	ProbabilityDistributionStateful = "stateful"
	// ProbabilityDistributionStateless selects independent latency samples.
	ProbabilityDistributionStateless = "stateless"
)

// EndpointConfig defines fault behavior for a matched endpoint.
type EndpointConfig struct {
	Match     MatchConfig          `yaml:"match"`
	Responses []StatusDistribution `yaml:"responses"`
	LoadBased *LoadBasedConfig     `yaml:"load_based,omitempty"`
}

// StatusDistribution defines a latency distribution for a specific HTTP status code.
// Resolution acts as both the relative weight for selecting this status code
// and the number of pre-computed samples in the stateful distribution.
type StatusDistribution struct {
	Status        int                  `yaml:"status"`
	Resolution    int                  `yaml:"resolution"`
	Distribution  map[string]string    `yaml:"distribution"`
	LocalResponse *LocalResponseConfig `yaml:"local_response,omitempty"`
}

// LocalResponseConfig customizes the body and headers of a forced response.
type LocalResponseConfig struct {
	Body    *string               `yaml:"body,omitempty"`
	Headers []LocalResponseHeader `yaml:"headers,omitempty"`
}

// LocalResponseHeader is one header pair on a forced response.
type LocalResponseHeader struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// LoadBasedConfig enables different response distributions based on in-flight request count.
type LoadBasedConfig struct {
	Healthy      *LoadTier       `yaml:"healthy"`
	TippingPoint *LoadTier       `yaml:"tipping_point"`
	GreyZone     *GreyZoneConfig `yaml:"grey_zone,omitempty"`
}

// LoadTier defines the response behavior at a specific load level.
type LoadTier struct {
	ThresholdInFlight float64              `yaml:"threshold_in_flight"`
	Responses         []StatusDistribution `yaml:"responses"`
}

// GreyZoneConfig controls behavior in the transition zone between healthy and tipping point.
type GreyZoneConfig struct {
	PenaltyBase            string  `yaml:"penalty_base"`
	SpikeThreshold         float64 `yaml:"spike_threshold"`
	SpikePenaltyDuration   string  `yaml:"spike_penalty_duration"`
	SpikePenaltyMultiplier float64 `yaml:"spike_penalty_multiplier"`
	RecoveryRate           float64 `yaml:"recovery_rate"`
}

func parseConfig(data []byte, source Source) (*FilterConfig, error) {
	var cfg FilterConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse filter config: %w", err)
	}

	// Default to stateful sampling when not explicitly configured.
	if cfg.ProbabilityDistribution == "" {
		cfg.ProbabilityDistribution = ProbabilityDistributionStateful
	}

	// Validate top-level options.
	validationErrors := []error{}
	if cfg.ProbabilityDistribution != ProbabilityDistributionStateful && cfg.ProbabilityDistribution != ProbabilityDistributionStateless {
		validationErrors = append(validationErrors, fmt.Errorf("probability_distribution must be one of %q or %q, got %q", ProbabilityDistributionStateful, ProbabilityDistributionStateless, cfg.ProbabilityDistribution))
	}

	if source == PerRouteSource || len(cfg.Responses) > 0 || cfg.LoadBased != nil {
		if err := validateBehavior(cfg.Responses, cfg.LoadBased, "configuration"); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	// Validate endpoints.
	for i, ep := range cfg.Endpoints {
		if err := validateBehavior(ep.Responses, ep.LoadBased, fmt.Sprintf("endpoint %d", i)); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if len(validationErrors) > 0 {
		return nil, errors.Join(validationErrors...)
	}

	return &cfg, nil
}

func validateBehavior(responses []StatusDistribution, loadBased *LoadBasedConfig, context string) error {
	validationErrors := []error{}
	if len(responses) == 0 && loadBased == nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: must have at least 'responses' or 'load_based' configured", context))
	}
	if len(responses) > 0 && loadBased != nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: must have *either one* 'responses' or 'load_based' configured", context))
	}
	for j, resp := range responses {
		if err := validateStatusDistribution(resp, fmt.Sprintf("%s response %d", context, j)); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	if loadBased != nil {
		if err := validateLoadBased(loadBased, context); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return errors.Join(validationErrors...)
}

func validateStatusDistribution(sd StatusDistribution, context string) error {
	validationErrors := []error{}
	if sd.Status < 200 || sd.Status > 599 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: invalid HTTP status code %d; supported status codes are 200-599", context, sd.Status))
	}
	if sd.Resolution <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: resolution must be positive, got %d", context, sd.Resolution))
	}
	if len(sd.Distribution) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: distribution must have at least one entry", context))
	}
	if _, err := ParsePercentileDistribution(sd.Distribution); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: %w", context, err))
	}
	if sd.LocalResponse != nil {
		if sd.Status == 204 || sd.Status == 205 || sd.Status == 304 {
			if sd.LocalResponse.Body != nil && *sd.LocalResponse.Body != "" {
				validationErrors = append(validationErrors, fmt.Errorf("%s: local_response.body must be empty for status %d", context, sd.Status))
			}
		}
		contentTypes := 0
		for i, header := range sd.LocalResponse.Headers {
			if !httpguts.ValidHeaderFieldName(header.Name) || strings.HasPrefix(header.Name, ":") {
				validationErrors = append(validationErrors, fmt.Errorf("%s local_response header %d: invalid header name %q", context, i, header.Name))
				continue
			}
			name := strings.ToLower(header.Name)
			if name == "content-length" || name == "transfer-encoding" || name == "connection" || name == "keep-alive" || name == "proxy-connection" || name == "upgrade" || name == "trailer" || name == "te" || strings.HasPrefix(name, "x-fault-") {
				validationErrors = append(validationErrors, fmt.Errorf("%s local_response header %d: header %q is reserved", context, i, header.Name))
			}
			if !httpguts.ValidHeaderFieldValue(header.Value) {
				validationErrors = append(validationErrors, fmt.Errorf("%s local_response header %d: invalid value", context, i))
			}
			if name == "content-type" {
				contentTypes++
				if strings.TrimSpace(header.Value) == "" {
					validationErrors = append(validationErrors, fmt.Errorf("%s local_response content-type must not be empty", context))
				}
			}
		}
		if contentTypes > 1 {
			validationErrors = append(validationErrors, fmt.Errorf("%s local_response may configure content-type only once", context))
		}
	}
	return errors.Join(validationErrors...)
}

func validateLoadBased(lb *LoadBasedConfig, context string) error {
	validationErrors := []error{}
	if lb.Healthy == nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.healthy is required", context))
	} else if lb.Healthy.ThresholdInFlight <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.healthy.threshold_in_flight must be positive", context))
	}

	if lb.TippingPoint == nil {
		validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.tipping_point is required", context))
	}

	if lb.TippingPoint != nil && lb.Healthy != nil {

		if lb.TippingPoint.ThresholdInFlight <= lb.Healthy.ThresholdInFlight {
			validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.tipping_point.threshold_in_flight must be greater than healthy.threshold_in_flight", context))
		}
		if len(lb.Healthy.Responses) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.healthy.responses must have at least one entry", context))
		}
		if len(lb.TippingPoint.Responses) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: load_based.tipping_point.responses must have at least one entry", context))
		}
		for j, resp := range lb.Healthy.Responses {
			if err := validateStatusDistribution(resp, fmt.Sprintf("%s healthy response %d", context, j)); err != nil {
				validationErrors = append(validationErrors, err)
			}
		}
		for j, resp := range lb.TippingPoint.Responses {
			if err := validateStatusDistribution(resp, fmt.Sprintf("%s tipping_point response %d", context, j)); err != nil {
				validationErrors = append(validationErrors, err)
			}
		}
	}

	if lb.GreyZone != nil {
		if _, err := time.ParseDuration(lb.GreyZone.PenaltyBase); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: grey_zone.penalty_base: %w", context, err))
		}
		if _, err := time.ParseDuration(lb.GreyZone.SpikePenaltyDuration); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: grey_zone.spike_penalty_duration: %w", context, err))
		}
		if lb.GreyZone.SpikeThreshold <= 0 || lb.GreyZone.SpikeThreshold >= 1 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: grey_zone.spike_threshold must be between 0 and 1 exclusive", context))
		}
		if lb.GreyZone.SpikePenaltyMultiplier <= 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: grey_zone.spike_penalty_multiplier must be positive", context))
		}
		if lb.GreyZone.RecoveryRate <= 0 || lb.GreyZone.RecoveryRate > 1 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: grey_zone.recovery_rate must be between 0 exclusive and 1 inclusive", context))
		}
	}
	return errors.Join(validationErrors...)
}

// MatchConfig defines how a request is matched to an endpoint.
type MatchConfig struct {
	Prefix  string              `yaml:"prefix,omitempty"`
	Exact   string              `yaml:"exact,omitempty"`
	Headers []HeaderMatchConfig `yaml:"headers,omitempty"`
}

// HeaderMatchConfig defines a header-based match condition.
type HeaderMatchConfig struct {
	Name         string `yaml:"name"`
	ExactMatch   string `yaml:"exact_match,omitempty"`
	PresentMatch bool   `yaml:"present_match,omitempty"`
}
