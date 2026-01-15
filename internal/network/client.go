package network

import (
	"net/http"
	"time"
)

// NetworkConfig holds network-level configuration for HTTP clients
type NetworkConfig struct {
	DelayEnabled bool `json:"delay_enabled"`
	MinDelayMs   int  `json:"min_delay_ms"` // Minimum delay in milliseconds
	MaxDelayMs   int  `json:"max_delay_ms"` // Maximum delay in milliseconds
}

// NewHTTPClient creates an HTTP client with optional latency simulation.
// If config.DelayEnabled is true, the client will add random delays to simulate network latency.
func NewHTTPClient(config NetworkConfig, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport

	if config.DelayEnabled {
		transport = NewDelayedRoundTripper(transport, DelayConfig{
			Enabled:  true,
			MinDelay: time.Duration(config.MinDelayMs) * time.Millisecond,
			MaxDelay: time.Duration(config.MaxDelayMs) * time.Millisecond,
		})
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
