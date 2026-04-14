package main

import "testing"

func TestPercentile(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50}
	if got := percentile(values, 0.50); got != 30 {
		t.Fatalf("p50 = %.1f, want 30", got)
	}
	if got := percentile(values, 0.95); got != 50 {
		t.Fatalf("p95 = %.1f, want 50", got)
	}
}

func TestEvaluateThresholds(t *testing.T) {
	err := evaluateThresholds(thresholds{
		P95MS:         100,
		P99MS:         120,
		MinThroughput: 5,
		MaxErrorRate:  0.01,
	}, summary{
		P95MS:               99,
		P99MS:               110,
		ActiveThroughputRPS: 6,
		ErrorRate:           0,
	})
	if err != nil {
		t.Fatalf("expected thresholds to pass: %v", err)
	}

	err = evaluateThresholds(thresholds{
		P95MS:         100,
		P99MS:         120,
		MinThroughput: 5,
		MaxErrorRate:  0.01,
	}, summary{
		P95MS:               140,
		P99MS:               150,
		ActiveThroughputRPS: 3,
		ErrorRate:           0.02,
	})
	if err == nil {
		t.Fatal("expected thresholds to fail")
	}
}
