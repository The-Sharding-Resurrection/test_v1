package main

import (
	"math"
	"sync"
	"testing"
)

func TestNewZipfianGenerator_UniformWhenThetaZero(t *testing.T) {
	gen := NewZipfianGenerator(100, 0.0)
	if gen.theta != 0 {
		t.Errorf("expected theta=0, got %f", gen.theta)
	}
	if gen.cdf != nil {
		t.Error("expected nil CDF for uniform distribution")
	}
	// Should not panic
	for i := 0; i < 1000; i++ {
		idx := gen.Next()
		if idx < 0 || idx >= 100 {
			t.Fatalf("index out of range: %d", idx)
		}
	}
}

func TestNewZipfianGenerator_NumItemsZero(t *testing.T) {
	gen := NewZipfianGenerator(0, 0.9)
	if gen.numItems != 1 {
		t.Errorf("expected numItems=1 for zero input, got %d", gen.numItems)
	}
	// Should not panic
	idx := gen.Next()
	if idx != 0 {
		t.Errorf("expected index 0, got %d", idx)
	}
}

func TestNewZipfianGenerator_NumItemsNegative(t *testing.T) {
	gen := NewZipfianGenerator(-5, 0.9)
	if gen.numItems != 1 {
		t.Errorf("expected numItems=1 for negative input, got %d", gen.numItems)
	}
}

func TestNewZipfianGenerator_SingleItem(t *testing.T) {
	gen := NewZipfianGenerator(1, 0.9)
	for i := 0; i < 100; i++ {
		if gen.Next() != 0 {
			t.Fatal("single-item generator should always return 0")
		}
	}
}

func TestZipfianGenerator_CDFSumsToOne(t *testing.T) {
	for _, theta := range []float64{0.1, 0.5, 0.9, 0.99} {
		gen := NewZipfianGenerator(1000, theta)
		if gen.cdf == nil {
			t.Fatalf("theta=%f: CDF should not be nil", theta)
		}
		last := gen.cdf[len(gen.cdf)-1]
		if math.Abs(last-1.0) > 1e-10 {
			t.Errorf("theta=%f: CDF last entry=%f, want 1.0", theta, last)
		}
	}
}

func TestZipfianGenerator_SkewedDistribution(t *testing.T) {
	gen := NewZipfianGenerator(100, 0.9)
	counts := make([]int, 100)
	n := 100000

	for i := 0; i < n; i++ {
		counts[gen.Next()]++
	}

	// With theta=0.9, index 0 should get significantly more hits than the average
	avg := float64(n) / 100.0
	if float64(counts[0]) < avg*3 {
		t.Errorf("index 0 got %d hits (avg=%.0f), expected much higher for theta=0.9", counts[0], avg)
	}

	// Verify monotonically decreasing (approximately)
	// First 10 should have more hits than last 10
	firstSum, lastSum := 0, 0
	for i := 0; i < 10; i++ {
		firstSum += counts[i]
		lastSum += counts[90+i]
	}
	if firstSum <= lastSum {
		t.Errorf("first 10 items (%d) should have more hits than last 10 (%d)", firstSum, lastSum)
	}
}

func TestZipfianGenerator_UniformDistribution(t *testing.T) {
	gen := NewZipfianGenerator(10, 0.0)
	counts := make([]int, 10)
	n := 100000

	for i := 0; i < n; i++ {
		counts[gen.Next()]++
	}

	// Each bucket should have roughly n/10 = 10000 hits (allow 30% deviation)
	expected := float64(n) / 10.0
	for i, c := range counts {
		ratio := float64(c) / expected
		if ratio < 0.7 || ratio > 1.3 {
			t.Errorf("index %d: got %d hits (expected ~%.0f, ratio=%.2f)", i, c, expected, ratio)
		}
	}
}

func TestZipfianGenerator_ConcurrentAccess(t *testing.T) {
	gen := NewZipfianGenerator(100, 0.9)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				idx := gen.Next()
				if idx < 0 || idx >= 100 {
					t.Errorf("index out of range: %d", idx)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestAddressToShard(t *testing.T) {
	tests := []struct {
		addr     string
		shards   int
		expected int
	}{
		{"0x0000000000000000000000000000000000000001", 8, 0},
		{"0x1000000000000000000000000000000000000001", 8, 1},
		{"0x7000000000000000000000000000000000000001", 8, 7},
		{"0xa000000000000000000000000000000000000001", 8, 2}, // a=10, 10%8=2
		{"0xf000000000000000000000000000000000000001", 8, 7}, // f=15, 15%8=7
		{"0x0000000000000000000000000000000000000001", 4, 0},
		{"0x5000000000000000000000000000000000000001", 4, 1}, // 5%4=1
	}

	for _, tt := range tests {
		got := addressToShard(tt.addr, tt.shards)
		if got != tt.expected {
			t.Errorf("addressToShard(%s, %d) = %d, want %d", tt.addr, tt.shards, got, tt.expected)
		}
	}
}
