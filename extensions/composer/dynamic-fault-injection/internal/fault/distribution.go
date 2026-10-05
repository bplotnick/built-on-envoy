// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package fault provides the basic building blocks for simulating faults and latencies,
// to be used by an envoy filter, including probability distributions for response times
// and status codes, as well as load-based behavior with grey zone handling.
package fault

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/tetratelabs/built-on-envoy/extensions/composer/dynamic-fault-injection/internal/config"
)

type durationDistribution interface {
	Sample() time.Duration
}

// ProbabilityDistribution samples from a distribution using linear interpolation
// between percentile boundaries. Stateless — each sample is independent.
type ProbabilityDistribution struct {
	percentiles []config.Percentile
}

// NewProbabilityDistribution creates a new stateless probability distribution.
func NewProbabilityDistribution(percentiles []config.Percentile) *ProbabilityDistribution {
	return &ProbabilityDistribution{
		percentiles: percentiles,
	}
}

// Sample returns a random duration from the distribution.
func (pd *ProbabilityDistribution) Sample() time.Duration {
	r := cryptoFloat64()
	return pd.SampleWithValue(r)
}

// SampleWithValue returns a duration for a specific quantile value [0, 1).
func (pd *ProbabilityDistribution) SampleWithValue(r float64) time.Duration {
	if len(pd.percentiles) == 0 {
		return 0
	}

	// Before the first percentile: interpolate from 0 to first value.
	if r <= pd.percentiles[0].Quantile {
		if pd.percentiles[0].Quantile == 0 {
			return pd.percentiles[0].Duration
		}
		fraction := r / pd.percentiles[0].Quantile
		return time.Duration(float64(pd.percentiles[0].Duration) * fraction)
	}

	// Between two percentiles: linear interpolation.
	for i := 1; i < len(pd.percentiles); i++ {
		if r <= pd.percentiles[i].Quantile {
			lower := pd.percentiles[i-1]
			upper := pd.percentiles[i]
			fraction := (r - lower.Quantile) / (upper.Quantile - lower.Quantile)
			dur := lower.Duration + time.Duration(fraction*float64(upper.Duration-lower.Duration))
			return dur
		}
	}

	// Beyond the last percentile: extrapolate.
	last := pd.percentiles[len(pd.percentiles)-1]
	if last.Quantile >= 1.0 {
		return last.Duration
	}
	fraction := (r - last.Quantile) / (1.0 - last.Quantile)
	return last.Duration + time.Duration(fraction*float64(last.Duration))
}

// StatefulProbabilityDistribution pre-computes exactly `resolution` samples
// and cycles through them in shuffled order. Over N samples, this guarantees
// an exact match to the configured percentile distribution.
type StatefulProbabilityDistribution struct {
	values    []time.Duration
	index     int
	indexLock sync.Mutex
}

// NewStatefulProbabilityDistribution creates a new stateful distribution with
// the given resolution (number of pre-computed samples).
func NewStatefulProbabilityDistribution(percentiles []config.Percentile, resolution int) *StatefulProbabilityDistribution {
	values := make([]time.Duration, resolution)
	idx := 0
	prevQuantile := 0.0
	prevDuration := time.Duration(0)

	for _, p := range percentiles {
		count := int(float64(resolution) * (p.Quantile - prevQuantile))
		for i := 0; i < count && idx < resolution; i++ {
			fraction := float64(i) / float64(count)
			dur := prevDuration + time.Duration(fraction*float64(p.Duration-prevDuration))
			values[idx] = dur
			idx++
		}
		prevQuantile = p.Quantile
		prevDuration = p.Duration
	}

	// Fill remaining slots (tail beyond last percentile).
	lastDuration := prevDuration
	remaining := resolution - idx
	for i := 0; i < remaining; i++ {
		fraction := float64(i) / float64(remaining)
		values[idx] = lastDuration + time.Duration(fraction*float64(lastDuration))
		idx++
	}

	cryptoShuffle(len(values), func(i, j int) {
		values[i], values[j] = values[j], values[i]
	})

	return &StatefulProbabilityDistribution{
		values: values,
		index:  0,
	}
}

// Sample returns the next pre-computed duration, reshuffling when the cycle completes.
func (spd *StatefulProbabilityDistribution) Sample() time.Duration {
	spd.indexLock.Lock()
	defer spd.indexLock.Unlock()

	if spd.index >= len(spd.values) {
		cryptoShuffle(len(spd.values), func(i, j int) {
			spd.values[i], spd.values[j] = spd.values[j], spd.values[i]
		})
		spd.index = 0
	}
	val := spd.values[spd.index]
	spd.index++
	return val
}

// ResponseSample represents a sampled response: status code + latency.
type ResponseSample struct {
	Status        int
	Duration      time.Duration
	LocalResponse *config.LocalResponseConfig
}

// ResponseDistribution selects a status code based on resolution weights,
// then samples a latency from that status code's distribution.
type ResponseDistribution struct {
	entries     []responseEntry
	totalWeight int
}

type responseEntry struct {
	status        int
	weight        int
	distribution  durationDistribution
	localResponse *config.LocalResponseConfig
}

// NewResponseDistributionWithMode creates a ResponseDistribution with the requested
// sampling mode: "stateful" or "stateless".
func NewResponseDistributionWithMode(statusDists []config.StatusDistribution, distributionMode string) (*ResponseDistribution, error) {
	if len(statusDists) == 0 {
		return nil, fmt.Errorf("response distribution must have at least one status entry")
	}
	entries := make([]responseEntry, 0, len(statusDists))
	totalWeight := 0

	for _, sd := range statusDists {
		percentiles, err := config.ParsePercentileDistribution(sd.Distribution)
		if err != nil {
			return nil, err
		}
		dist, err := newDurationDistribution(percentiles, sd.Resolution, distributionMode)
		if err != nil {
			return nil, err
		}
		entries = append(entries, responseEntry{
			status:        sd.Status,
			weight:        sd.Resolution,
			distribution:  dist,
			localResponse: sd.LocalResponse,
		})
		totalWeight += sd.Resolution
	}

	return &ResponseDistribution{
		entries:     entries,
		totalWeight: totalWeight,
	}, nil
}

// Sample selects a status code by weight and returns a sampled response.
func (rd *ResponseDistribution) Sample() ResponseSample {
	// Select status by weighted random choice.
	r := cryptoIntn(rd.totalWeight)
	cumulative := 0
	for _, entry := range rd.entries {
		cumulative += entry.weight
		if r < cumulative {
			return ResponseSample{
				Status:        entry.status,
				Duration:      entry.distribution.Sample(),
				LocalResponse: entry.localResponse,
			}
		}
	}
	// Fallback (should not happen).
	last := &rd.entries[len(rd.entries)-1]
	return ResponseSample{
		Status:        last.status,
		Duration:      last.distribution.Sample(),
		LocalResponse: last.localResponse,
	}
}

// LoadBasedResponseDistribution switches between healthy and tipping-point
// distributions based on observed load, with grey zone transition behavior.
type LoadBasedResponseDistribution struct {
	healthy          *ResponseDistribution
	tippingPoint     *ResponseDistribution
	healthyThreshold float64
	tippingThreshold float64
	greyZone         *greyZoneState
	now              func() time.Time
	mu               sync.Mutex
}

type greyZoneState struct {
	penaltyBase            time.Duration
	spikePenaltyDuration   time.Duration
	spikeThreshold         float64
	spikePenaltyMultiplier float64
	recoveryRate           float64
	recoveryStart          time.Time
	inSpike                bool
}

// NewLoadBasedResponseDistributionWithMode creates a load-based distribution
// using either stateful or stateless sampling.
func NewLoadBasedResponseDistributionWithMode(
	healthyDists []config.StatusDistribution,
	healthyThreshold float64,
	tippingDists []config.StatusDistribution,
	tippingThreshold float64,
	gz *config.GreyZoneConfig,
	distributionMode string,
) (*LoadBasedResponseDistribution, error) {
	healthy, err := NewResponseDistributionWithMode(healthyDists, distributionMode)
	if err != nil {
		return nil, err
	}
	tipping, err := NewResponseDistributionWithMode(tippingDists, distributionMode)
	if err != nil {
		return nil, err
	}

	lb := &LoadBasedResponseDistribution{
		healthy:          healthy,
		tippingPoint:     tipping,
		healthyThreshold: healthyThreshold,
		tippingThreshold: tippingThreshold,
		now:              time.Now,
	}

	if gz != nil {
		penaltyBase, _ := time.ParseDuration(gz.PenaltyBase)
		spikeDur, _ := time.ParseDuration(gz.SpikePenaltyDuration)
		lb.greyZone = &greyZoneState{
			penaltyBase:            penaltyBase,
			spikePenaltyDuration:   spikeDur,
			spikeThreshold:         gz.SpikeThreshold,
			spikePenaltyMultiplier: gz.SpikePenaltyMultiplier,
			recoveryRate:           gz.RecoveryRate,
		}
	}

	return lb, nil
}

func newDurationDistribution(percentiles []config.Percentile, resolution int, distributionMode string) (durationDistribution, error) {
	switch distributionMode {
	case config.ProbabilityDistributionStateful:
		return NewStatefulProbabilityDistribution(percentiles, resolution), nil
	case config.ProbabilityDistributionStateless:
		return NewProbabilityDistribution(percentiles), nil
	default:
		return nil, fmt.Errorf("unsupported probability distribution mode: %q", distributionMode)
	}
}

// Sample returns a response based on the current load.
// In the grey zone (between healthyThreshold and tippingThreshold), it interpolates
// between healthy and tipping behavior with optional spike penalties.
func (lb *LoadBasedResponseDistribution) Sample(currentInFlight float64) ResponseSample {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	greyPosition := (currentInFlight - lb.healthyThreshold) / (lb.tippingThreshold - lb.healthyThreshold)
	var penalty time.Duration
	// Tier transitions update spike state even though penalties apply only in the grey zone.
	if lb.greyZone != nil {
		penalty = lb.calculateGreyZonePenalty(min(max(greyPosition, 0), 1), lb.now())
	}

	if currentInFlight <= lb.healthyThreshold {
		return lb.healthy.Sample()
	}
	if currentInFlight >= lb.tippingThreshold {
		return lb.tippingPoint.Sample()
	}

	// Decide whether to use healthy or tipping distribution based on position.
	var sample ResponseSample
	if cryptoFloat64() > greyPosition {
		sample = lb.healthy.Sample()
	} else {
		sample = lb.tippingPoint.Sample()
	}

	sample.Duration += penalty

	return sample
}

// calculateGreyZonePenalty computes additional latency penalty in the grey zone.
func (lb *LoadBasedResponseDistribution) calculateGreyZonePenalty(greyPosition float64, now time.Time) time.Duration {
	gz := lb.greyZone
	basePenalty := time.Duration(float64(gz.penaltyBase) * greyPosition)

	if greyPosition >= gz.spikeThreshold {
		gz.inSpike = true
		gz.recoveryStart = time.Time{}
		return time.Duration(float64(basePenalty) * gz.spikePenaltyMultiplier)
	}
	if gz.inSpike {
		if gz.recoveryStart.IsZero() {
			gz.recoveryStart = now
		}
		elapsed := now.Sub(gz.recoveryStart)
		if elapsed >= gz.spikePenaltyDuration {
			gz.inSpike = false
			gz.recoveryStart = time.Time{}
		} else {
			remaining := 1.0 - float64(elapsed)/float64(gz.spikePenaltyDuration)*gz.recoveryRate
			return time.Duration(float64(basePenalty) * gz.spikePenaltyMultiplier * remaining)
		}
	}
	return basePenalty
}

// cryptoFloat64 returns a cryptographically random float64 in [0, 1).
func cryptoFloat64() float64 {
	maximum := new(big.Int).SetUint64(1 << 53)
	n, _ := rand.Int(rand.Reader, maximum)
	return float64(n.Uint64()) / float64(1<<53)
}

// cryptoIntn returns a cryptographically random int in [0, n).
func cryptoIntn(n int) int {
	maximum := big.NewInt(int64(n))
	val, _ := rand.Int(rand.Reader, maximum)
	return int(val.Int64())
}

// cryptoShuffle performs a Fisher-Yates shuffle using crypto/rand.
func cryptoShuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		j := cryptoIntn(i + 1)
		swap(i, j)
	}
}
