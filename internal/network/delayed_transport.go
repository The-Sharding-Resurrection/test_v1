package network

import (
	"math/rand"
	"net/http"
	"time"
)

// DelayConfig specifies latency simulation parameters
type DelayConfig struct {
	Enabled  bool          `json:"enabled"`
	MinDelay time.Duration `json:"min_delay"` // e.g., 10ms
	MaxDelay time.Duration `json:"max_delay"` // e.g., 100ms
}

// DelayedRoundTripper wraps http.RoundTripper with configurable delays
type DelayedRoundTripper struct {
	base   http.RoundTripper
	config DelayConfig
	rng    *rand.Rand
}

// NewDelayedRoundTripper creates a new DelayedRoundTripper.
// If base is nil, http.DefaultTransport is used.
func NewDelayedRoundTripper(base http.RoundTripper, config DelayConfig) *DelayedRoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &DelayedRoundTripper{
		base:   base,
		config: config,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// RoundTrip implements http.RoundTripper by adding a delay before the actual request
func (d *DelayedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if d.config.Enabled {
		delay := d.calculateDelay()
		time.Sleep(delay)
	}
	return d.base.RoundTrip(req)
}

// calculateDelay returns a random delay within the configured range
func (d *DelayedRoundTripper) calculateDelay() time.Duration {
	min := d.config.MinDelay
	max := d.config.MaxDelay

	// Random delay within range
	if max > min {
		delta := max - min
		return min + time.Duration(d.rng.Int63n(int64(delta)))
	}
	return min
}
