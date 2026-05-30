package selector

import (
	"sort"
)

// Entry  what the selector needs to know
// about each candidate server
// Entry is the minimal view of a probe result
// that the selector needs. The pool passes these in.
type Entry struct {
	URL       string
	RIF       int64
	LatencyMs int64
	ErrorRate float64
}

// effectiveLatency applies error rate penalty.
// Servers with high error rates appear slower to HCL.
// At 100% error rate the server appears 10x slower.
func (e Entry) effectiveLatency() int64 {
	if e.ErrorRate > 0.05 {
		penalty := 1.0 + (e.ErrorRate * 9.0)
		return int64(float64(e.LatencyMs) * penalty)
	}
	return e.LatencyMs
}

// ApplyHCL the core routing algorithm
// ApplyHCL implements the Hot-Cold Lexicographic rule.
//
// Algorithm:
//  1. Compute θRIF as the qrif-th percentile of entry RIF values
//  2. Classify each entry as hot (RIF > θRIF) or cold (RIF ≤ θRIF)
//  3. If any cold entries exist — pick cold with lowest effective latency
//  4. If all entries are hot — pick hot with lowest RIF
//
// Returns empty string if entries is empty.
func ApplyHCL(entries []Entry, qrif float64) string {
	if len(entries) == 0 {
		return ""
	}

	//step 1: compute θRIF
	threshold := rifThreshold(entries, qrif)

	// step 2: classify
	var cold, hot []Entry
	for _, e := range entries {
		if e.RIF <= threshold {
			cold = append(cold, e)
		} else {
			hot = append(hot, e)
		}
	}

	// step 3: route to best cold
	if len(cold) > 0 {
		best := cold[0]
		for _, e := range cold[1:] {
			if e.effectiveLatency() < best.effectiveLatency() {
				best = e
			}
		}
		return best.URL
	}

	//step 4: all hot — pick lowest RIF
	best := hot[0]
	for _, e := range hot[1:] {
		if e.RIF < best.RIF {
			best = e
		}
	}
	return best.URL
}

// WorstByHCL : used by pool remove-worst

// WorstByHCL returns the URL of the entry HCL would choose last.
// This is the mirror image of ApplyHCL used for eviction.
//
// If any hot entries exist — return hot with highest RIF.
// If all cold return cold with highest effective latency.
func WorstByHCL(entries []Entry, qrif float64) string {
	if len(entries) == 0 {
		return ""
	}

	threshold := rifThreshold(entries, qrif)

	var cold, hot []Entry
	for _, e := range entries {
		if e.RIF <= threshold {
			cold = append(cold, e)
		} else {
			hot = append(hot, e)
		}
	}

	// worst hot highest RIF
	if len(hot) > 0 {
		worst := hot[0]
		for _, e := range hot[1:] {
			if e.RIF > worst.RIF {
				worst = e
			}
		}
		return worst.URL
	}

	// all cold highest effective latency
	worst := cold[0]
	for _, e := range cold[1:] {
		if e.effectiveLatency() > worst.effectiveLatency() {
			worst = e
		}
	}
	return worst.URL
}

// rifThreshold — dynamic QRIF computation
// rifThreshold computes θRIF as the qrif-th percentile
// of RIF values across the given entries.
//
// With qrif=0.84 and 16 entries:
//
//	idx = int(15 × 0.84) = 12
//	threshold = sorted_rifs[12]  (13th value out of 16)
//
// This means roughly the top 16% of entries are classified hot.
func rifThreshold(entries []Entry, qrif float64) int64 {
	rifs := make([]int64, len(entries))
	for i, e := range entries {
		rifs[i] = e.RIF
	}
	sort.Slice(rifs, func(i, j int) bool { return rifs[i] < rifs[j] })

	idx := int(float64(len(rifs)-1) * qrif)
	return rifs[idx]
}
