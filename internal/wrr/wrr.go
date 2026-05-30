package wrr

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madhavrajverma/Prequel-Load-balancer/internal/observability"
)

const (
	// how often WRR recomputes weights from server stats
	weightUpdateInterval = 2 * time.Second

	// probe timeout for WRR stat collection
	statTimeout = 300 * time.Millisecond

	// default weight when a server has no data yet
	defaultWeight = 1.0

	// smoothing factor for EWMA weight updates
	// alpha=0.3 means new observations count for 30%
	alpha = 0.3
)

// serverStat — tracks one backend's current stats
type serverStat struct {
	latencyMs float64 // smoothed latency estimate
	errorRate float64 // recent error rate
	weight    float64 // computed weight
	healthy   bool
}

// WRR implements Weighted Round Robin load balancing.
// Weights are derived from server latency — faster servers
// receive proportionally more traffic.
// This approximates the paper's CPU-based WRR while using
// the same /status endpoint our servers already expose.
type WRR struct {
	backends []string
	client   *http.Client

	mu    sync.RWMutex
	stats map[string]*serverStat

	// weighted round robin state
	current int     // current position in the backend list
	counter float64 // fractional counter for weighted distribution
}

// New creates a WRR instance and starts the background
// weight updater.
func New(backends []string) *WRR {
	w := &WRR{
		backends: backends,
		client: &http.Client{
			Timeout: 500 * time.Millisecond,
		},
		stats: make(map[string]*serverStat),
	}

	// initialise all backends with default stats
	for _, b := range backends {
		w.stats[b] = &serverStat{
			latencyMs: 100, // assume 100ms until we have data
			weight:    defaultWeight,
			healthy:   true,
		}
		observability.BackendHealthy.WithLabelValues(b).Set(1)
	}

	// start background weight updater
	go w.runWeightUpdater()

	return w
}

// SelectBest — weighted round robin selection
// SelectBest returns the next backend according to
// weighted round robin ordering.
// Backends with higher weight receive proportionally
// more requests.
func (w *WRR) SelectBest() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	type candidate struct {
		url    string
		weight float64
	}

	var candidates []candidate
	totalWeight := 0.0

	for _, b := range w.backends {
		stat, ok := w.stats[b]
		if !ok || !stat.healthy {
			continue
		}
		candidates = append(candidates, candidate{
			url:    b,
			weight: stat.weight,
		})
		totalWeight += stat.weight
	}

	if len(candidates) == 0 {
		observability.LBFallbackTotal.Inc()
		return w.backends[0]
	}

	if len(candidates) == 1 {
		return candidates[0].url
	}

	// advance counter
	w.counter += 1.0
	if w.counter >= totalWeight {
		w.counter = 0
	}

	// find backend whose cumulative weight covers the counter
	cumulative := 0.0
	for _, c := range candidates {
		cumulative += c.weight
		if w.counter <= cumulative {
			return c.url
		}
	}

	// fallback  return last candidate
	return candidates[len(candidates)-1].url
}

// CompensateRIF is a no-op for WRR.
// WRR does not use a probe pool so there is nothing to compensate.
func (w *WRR) CompensateRIF(_ string) {}

// MarkUsed is a no-op for WRR.
// WRR does not have a reuse budget.
func (w *WRR) MarkUsed(_ string) {}

// AddProbe is a no-op for WRR.
// WRR collects stats on its own schedule not per-request.
func (w *WRR) AddProbe(_ string, _, _ int64, _ float64, _ float64) {}

// MaybeRemoveWorst is a no-op for WRR.
// WRR has no probe pool to maintain.
func (w *WRR) MaybeRemoveWorst() {}

// Weight computation
// computeWeight converts a latency estimate into a routing weight.
// Lower latency → higher weight → more traffic.
// Uses inverse latency so a 50ms server gets 2x the weight of a 100ms server.
func computeWeight(latencyMs float64, errorRate float64) float64 {
	if latencyMs <= 0 {
		latencyMs = 1
	}

	// base weight is inverse of latency
	// 50ms → weight 20, 100ms → weight 10, 200ms → weight 5
	weight := 1000.0 / latencyMs

	// penalise servers with high error rates
	// mirrors the paper's WRR error rate handling
	if errorRate > 0.01 {
		weight *= (1.0 - errorRate)
	}

	if weight < 0.01 {
		weight = 0.01 // minimum weight server still gets tiny fraction
	}

	return weight
}

// Background weight updater
// runWeightUpdater polls all backends on a fixed interval
// and recomputes routing weights from their current stats.
// This is WRR's fundamental characteristic — it uses smoothed
// historical stats, not real-time probe data.
func (w *WRR) runWeightUpdater() {
	ticker := time.NewTicker(weightUpdateInterval)
	defer ticker.Stop()

	for range ticker.C {
		w.updateAllWeights()
	}
}

// updateAllWeights polls every backend and updates weights.
func (w *WRR) updateAllWeights() {
	for _, backend := range w.backends {
		go w.updateWeight(backend)
	}
}

// updateWeight polls one backend's /status and updates its weight.
func (w *WRR) updateWeight(backend string) {
	url := strings.TrimRight(backend, "/") + "/status"

	ctx, cancel := context.WithTimeout(context.Background(), statTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		w.markUnhealthy(backend)
		return
	}

	resp, err := w.client.Do(req)
	if err != nil {
		w.markUnhealthy(backend)
		return
	}
	defer resp.Body.Close()

	var result struct {
		LatencyMs int64   `json:"latency_ms"`
		ErrorRate float64 `json:"error_rate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		w.markUnhealthy(backend)
		return
	}

	// update stats with EWMA smoothing
	// this is WRR's trailing signal property
	// it averages over time rather than reacting instantly
	w.mu.Lock()
	stat := w.stats[backend]

	if stat.latencyMs == 0 {
		stat.latencyMs = float64(result.LatencyMs)
	} else {
		// EWMA: new = alpha*sample + (1-alpha)*old
		stat.latencyMs = alpha*float64(result.LatencyMs) +
			(1-alpha)*stat.latencyMs
	}

	stat.errorRate = alpha*result.ErrorRate + (1-alpha)*stat.errorRate
	stat.weight = computeWeight(stat.latencyMs, stat.errorRate)
	stat.healthy = true

	w.mu.Unlock()

	// update observability
	observability.BackendLatencyEst.WithLabelValues(backend).Set(stat.latencyMs)
	observability.BackendHealthy.WithLabelValues(backend).Set(1)

	log.Printf("wrr: %s latency=%.0fms weight=%.2f",
		backend, stat.latencyMs, stat.weight)
}

// markUnhealthy marks a backend as unavailable.
func (w *WRR) markUnhealthy(backend string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if stat, ok := w.stats[backend]; ok {
		stat.healthy = false
		log.Printf("wrr: backend %s marked unhealthy", backend)
	}
	observability.BackendHealthy.WithLabelValues(backend).Set(0)
}
