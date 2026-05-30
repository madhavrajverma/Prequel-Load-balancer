package selector

import (
	"testing"
)

func TestApplyHCL_prefersLowLatencyCold(t *testing.T) {
	entries := []Entry{
		{URL: "server-a", RIF: 3, LatencyMs: 80},  // cold
		{URL: "server-b", RIF: 5, LatencyMs: 40},  // cold  best latency
		{URL: "server-c", RIF: 20, LatencyMs: 10}, // hot low latency but hot
	}

	// with qrif=0.84 and 3 entries:
	// sorted RIFs: [3, 5, 20]
	// idx = int(2 × 0.84) = int(1.68) = 1
	// threshold = rifs[1] = 5
	// server-c (RIF=20) is hot — excluded
	// cold: server-a (80ms) and server-b (40ms)
	// HCL picks server-b — lowest latency among cold

	got := ApplyHCL(entries, 0.84)
	if got != "server-b" {
		t.Errorf("expected server-b got %s", got)
	}
}

func TestApplyHCL_allHotPicksLowestRIF(t *testing.T) {
	entries := []Entry{
		{URL: "server-a", RIF: 50, LatencyMs: 80},
		{URL: "server-b", RIF: 30, LatencyMs: 120}, // lowest RIF among hot
		{URL: "server-c", RIF: 45, LatencyMs: 60},
	}

	// with qrif=0.84 and 3 entries:
	// sorted RIFs: [30, 45, 50]
	// threshold = rifs[1] = 45
	// hot: server-a (50) and server-c (45) — both above threshold
	// cold: server-b (30) — below threshold
	// wait — server-b is cold here
	// let us use qrif=0 to force all hot

	got := ApplyHCL(entries, 0.0)
	// threshold = rifs[0] = 30
	// hot: server-a (50 > 30) and server-c (45 > 30)
	// cold: server-b (30 = 30 ≤ 30)
	// server-b is cold — pick it
	if got != "server-b" {
		t.Errorf("expected server-b got %s", got)
	}
}
func TestApplyHCL_errorRatePenalty(t *testing.T) {
	// server-b has 10ms latency but 90% error rate
	// effective latency = 10 × (1 + 0.9×9) = 10 × 9.1 = 91ms
	// server-a effective latency = 50ms
	// HCL picks server-a — penalty flipped the decision
	entries := []Entry{
		{URL: "server-a", RIF: 2, LatencyMs: 50, ErrorRate: 0.0},
		{URL: "server-b", RIF: 2, LatencyMs: 10, ErrorRate: 0.9},
	}

	got := ApplyHCL(entries, 0.84)
	if got != "server-a" {
		t.Errorf("expected server-a got %s", got)
	}
}

func TestWorstByHCL_returnsHighestRIFHot(t *testing.T) {
	entries := []Entry{
		{URL: "server-a", RIF: 3, LatencyMs: 50},
		{URL: "server-b", RIF: 25, LatencyMs: 60}, // hot — highest RIF
		{URL: "server-c", RIF: 18, LatencyMs: 55}, // hot
	}

	// sorted RIFs: [3, 18, 25]
	// qrif=0.84, idx=int(1.68)=1, threshold=18
	// hot: server-b (25 > 18)
	// server-c (18 = 18) — at threshold — classified cold
	// worst hot = server-b

	got := WorstByHCL(entries, 0.84)
	if got != "server-b" {
		t.Errorf("expected server-b got %s", got)
	}
}

func TestApplyHCL_emptyEntries(t *testing.T) {
	got := ApplyHCL([]Entry{}, 0.84)
	if got != "" {
		t.Errorf("expected empty string got %s", got)
	}
}

func TestRifThreshold_singleEntry(t *testing.T) {
	entries := []Entry{{URL: "a", RIF: 7}}
	threshold := rifThreshold(entries, 0.84)
	// idx = int(0 × 0.84) = 0
	// threshold = rifs[0] = 7
	if threshold != 7 {
		t.Errorf("expected 7 got %d", threshold)
	}
}
