package network

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDelayedRoundTripper_Disabled verifies that no delay is added when disabled
func TestDelayedRoundTripper_Disabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := DelayConfig{
		Enabled:  false,
		MinDelay: 100 * time.Millisecond,
		MaxDelay: 200 * time.Millisecond,
	}

	transport := NewDelayedRoundTripper(nil, config)
	client := &http.Client{Transport: transport}

	start := time.Now()
	resp, err := client.Get(server.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Request should complete quickly (< 50ms) when disabled
	if elapsed > 50*time.Millisecond {
		t.Errorf("Request took too long with disabled delay: %v", elapsed)
	}
}

// TestDelayedRoundTripper_Enabled verifies that delays are added when enabled
func TestDelayedRoundTripper_Enabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	minDelay := 50 * time.Millisecond
	maxDelay := 100 * time.Millisecond

	config := DelayConfig{
		Enabled:  true,
		MinDelay: minDelay,
		MaxDelay: maxDelay,
	}

	transport := NewDelayedRoundTripper(nil, config)
	client := &http.Client{Transport: transport}

	start := time.Now()
	resp, err := client.Get(server.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Request should take at least minDelay
	if elapsed < minDelay {
		t.Errorf("Request completed too quickly: %v (expected >= %v)", elapsed, minDelay)
	}

	// Request should not exceed maxDelay + reasonable margin (50ms for processing)
	maxAllowed := maxDelay + 50*time.Millisecond
	if elapsed > maxAllowed {
		t.Errorf("Request took too long: %v (expected <= %v)", elapsed, maxAllowed)
	}
}

// TestDelayedRoundTripper_DelayRange verifies delays fall within configured range
func TestDelayedRoundTripper_DelayRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	minDelay := 20 * time.Millisecond
	maxDelay := 50 * time.Millisecond

	config := DelayConfig{
		Enabled:  true,
		MinDelay: minDelay,
		MaxDelay: maxDelay,
	}

	transport := NewDelayedRoundTripper(nil, config)
	client := &http.Client{Transport: transport}

	// Run multiple requests to verify range consistency
	iterations := 10
	for i := 0; i < iterations; i++ {
		start := time.Now()
		resp, err := client.Get(server.URL)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()

		// Each request should respect the delay range
		if elapsed < minDelay {
			t.Errorf("Request %d too fast: %v (expected >= %v)", i, elapsed, minDelay)
		}
		maxAllowed := maxDelay + 50*time.Millisecond
		if elapsed > maxAllowed {
			t.Errorf("Request %d too slow: %v (expected <= %v)", i, elapsed, maxAllowed)
		}
	}
}

// TestDelayedRoundTripper_ZeroMaxDelay verifies behavior when max == min
func TestDelayedRoundTripper_ZeroMaxDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fixedDelay := 30 * time.Millisecond

	config := DelayConfig{
		Enabled:  true,
		MinDelay: fixedDelay,
		MaxDelay: fixedDelay, // Same as min - should use fixed delay
	}

	transport := NewDelayedRoundTripper(nil, config)
	client := &http.Client{Transport: transport}

	start := time.Now()
	resp, err := client.Get(server.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should use the fixed delay
	if elapsed < fixedDelay {
		t.Errorf("Request too fast: %v (expected >= %v)", elapsed, fixedDelay)
	}

	maxAllowed := fixedDelay + 50*time.Millisecond
	if elapsed > maxAllowed {
		t.Errorf("Request too slow: %v (expected ~%v)", elapsed, fixedDelay)
	}
}

// TestNewHTTPClient_NoDelay verifies default behavior without delays
func TestNewHTTPClient_NoDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := NetworkConfig{
		DelayEnabled: false,
		MinDelayMs:   100,
		MaxDelayMs:   200,
	}

	client := NewHTTPClient(config, 5*time.Second)

	start := time.Now()
	resp, err := client.Get(server.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should complete quickly without delay
	if elapsed > 50*time.Millisecond {
		t.Errorf("Request took too long without delay: %v", elapsed)
	}
}

// TestNewHTTPClient_WithDelay verifies factory creates delayed client
func TestNewHTTPClient_WithDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := NetworkConfig{
		DelayEnabled: true,
		MinDelayMs:   30,
		MaxDelayMs:   60,
	}

	client := NewHTTPClient(config, 5*time.Second)

	start := time.Now()
	resp, err := client.Get(server.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	minExpected := 30 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("Request too fast: %v (expected >= %v)", elapsed, minExpected)
	}
}
