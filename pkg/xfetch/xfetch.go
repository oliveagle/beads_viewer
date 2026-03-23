package xfetch

import (
	"math"
	"math/rand"
	"time"
)

// XFetch implements probabilistic early refresh to prevent cache stampedes.
// Based on the "XFetch" algorithm from "Optimal Probabilistic Cache Stampede Prevention"
// (Vattani, Chierichetti, Lowenstein 2015).
//
// The algorithm probabilistically decides whether to refresh a cached value
// before its expiry, with refresh probability increasing as expiry approaches.
// This spreads refresh load over time instead of all clients refreshing simultaneously.

// ShouldRefresh returns true if a cached value should be refreshed early.
//
// Parameters:
//   - lastCompute: when the cached value was last computed
//   - computeDuration: how long the last computation took (used for gap estimation)
//   - beta: scaling factor (1.0 is standard; higher = more aggressive early refresh)
//   - now: current time (allows deterministic testing)
//
// The probability of returning true increases as (now - lastCompute) approaches computeDuration.
// This ensures some clients start refreshing before all cached copies expire.
func ShouldRefresh(lastCompute time.Time, computeDuration time.Duration, beta float64, now time.Time) bool {
	if computeDuration <= 0 || beta <= 0 || math.IsNaN(beta) || math.IsInf(beta, 0) {
		return false
	}

	// XFetch formula: refresh if now > lastCompute + duration * beta * -ln(rand)
	// Since ln(rand) is negative for rand in (0,1), we use -ln(rand) which is positive
	//
	// Intuition: as time passes, (now - lastCompute) grows. The threshold
	// computeDuration * beta * -ln(rand) is a random positive value. Higher beta
	// or longer computeDuration means higher threshold, so less likely to refresh early.
	// As cache ages, eventually now exceeds the threshold.

	r := rand.Float64()
	if r <= 0 {
		r = 1e-10 // Avoid log(0)
	}

	product := float64(computeDuration) * beta * -math.Log(r)
	if product > float64(math.MaxInt64) || math.IsInf(product, 0) || math.IsNaN(product) {
		// Duration would overflow int64 — definitely should refresh
		return true
	}
	threshold := lastCompute.Add(time.Duration(product))
	return now.After(threshold)
}

// ShouldRefreshWithDefault is a convenience wrapper using time.Now() and beta=1.0.
func ShouldRefreshWithDefault(lastCompute time.Time, computeDuration time.Duration) bool {
	return ShouldRefresh(lastCompute, computeDuration, 1.0, time.Now())
}
