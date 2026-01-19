package network

import (
	"log"
	"net/http"
	"time"

	"github.com/sharding-experiment/sharding/config"
)

// NewHTTPClient creates an HTTP client with optional latency simulation.
// If config.DelayEnabled is true, the client will add random delays to simulate network latency.
func NewHTTPClient(cfg config.NetworkConfig, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport

	if cfg.DelayEnabled {
		// Validate delay values
		if cfg.MinDelayMs < 0 || cfg.MaxDelayMs < 0 {
			log.Printf("Warning: negative delay values (min=%d, max=%d), disabling delay simulation",
				cfg.MinDelayMs, cfg.MaxDelayMs)
			cfg.DelayEnabled = false
		} else if cfg.MaxDelayMs < cfg.MinDelayMs {
			log.Printf("Warning: max_delay (%d) < min_delay (%d), swapping values",
				cfg.MaxDelayMs, cfg.MinDelayMs)
			cfg.MinDelayMs, cfg.MaxDelayMs = cfg.MaxDelayMs, cfg.MinDelayMs
		}

		if cfg.DelayEnabled {
			transport = NewDelayedRoundTripper(transport, DelayConfig{
				Enabled:  true,
				MinDelay: time.Duration(cfg.MinDelayMs) * time.Millisecond,
				MaxDelay: time.Duration(cfg.MaxDelayMs) * time.Millisecond,
			})
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
