package pool

import (
	"math/rand/v2"
	"sync"
	"time"

	"prequal/internal/observability"
	"prequal/internal/selector"
)

const (
	MaxSize      = 16
	ProbeTimeout = 1 * time.Second
	MinEntries   = 2 // below this fallback to random
)

// ProbeEntry one probe response from one server
// ProbeEntry holds a single probe response.
type ProbeEntry struct {
	URL        string
	RIF        int64
	LatencyMs  int64
	ErrorRate  float64
	ReceivedAt time.Time

	useCount int     // how many times used for routing
	breuse   float64 // reuse budget  computed at add time
}

// effectiveLatency applies an error rate penalty.
// Servers returning many errors appear slower to HCL.
/*
func (e *ProbeEntry) effectiveLatency() int64 {
	if e.ErrorRate > 0.05 {
		penalty := 1.0 + (e.ErrorRate * 9.0)
		return int64(float64(e.LatencyMs) * penalty)
	}
	return e.LatencyMs
}
*/

// shouldRemoveAfterUse returns true when the reuse budget is exhausted.
// Uses probabilistic rounding for fractional breuse values.
func (e *ProbeEntry) shouldRemoveAfterUse() bool {
	e.useCount++
	floor := int(e.breuse)
	frac := e.breuse - float64(floor)

	if e.useCount > floor {
		return true
	}
	if e.useCount == floor && frac > 0 {
		// remove with probability (1 - frac)
		return rand.Float64() > frac
	}
	return false
}

// Pool holds probe responses and manages their lifecycle.
// It is safe for concurrent use.
type Pool struct {
	mu       sync.Mutex
	entries  []ProbeEntry
	backends []string // all known backends  for random fallback

	// config
	qrif    float64 // 0.84  QRIF quantile
	rremove float64 // removals per query
	delta   float64 // pool drift factor for breuse formula
	n       int     // total number of backends

	// state
	removeCount int64   // drives A/B alternation
	removeAccum float64 // accumulator for fractional rremove
}

// Config holds all pool configuration.
type Config struct {
	Backends []string
	QRIF     float64 // recommended: 0.84
	Rprobe   float64 // probes per query used in breuse formula
	Rremove  float64 // removals per query  default 1.0
	Delta    float64 // drift factor  default 1.0
}

// New creates a Pool from config.
func New(cfg Config) *Pool {
	return &Pool{
		backends: cfg.Backends,
		entries:  make([]ProbeEntry, 0, MaxSize),
		qrif:     cfg.QRIF,
		rremove:  cfg.Rremove,
		delta:    cfg.Delta,
		n:        len(cfg.Backends),
	}
}

// breuse formula
// computeBreuse calculates the reuse budget for a new probe entry.
// Formula from the paper:
//
//	breuse = max(1, ceil((1+δ) / ((1 - m/n) × rprobe - rremove)))
//
// where m = current pool size, n = total replicas.
func (p *Pool) computeBreuse(rprobe float64) float64 {
	m := float64(len(p.entries))
	n := float64(p.n)

	if n == 0 {
		return 1.0
	}

	denominator := (1.0-m/n)*rprobe - p.rremove
	if denominator <= 0 {
		// drain rate exceeds arrival rate — use minimum reuse
		return 1.0
	}

	breuse := (1.0 + p.delta) / denominator
	if breuse < 1.0 {
		return 1.0
	}
	return breuse
}

// AddProbe called when a probe response arrives
// AddProbe adds a new probe response to the pool.
// If the pool is full the oldest entry is evicted first.
// rprobe is the current probing rate used to compute breuse.
func (p *Pool) AddProbe(url string, rif, latencyMs int64, errorRate float64, rprobe float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// evict oldest if at capacity
	if len(p.entries) >= MaxSize {
		p.evictOldest()
		observability.PoolRemovalsTotal.WithLabelValues("size").Inc()
	}

	entry := ProbeEntry{
		URL:        url,
		RIF:        rif,
		LatencyMs:  latencyMs,
		ErrorRate:  errorRate,
		ReceivedAt: time.Now(),
		breuse:     p.computeBreuse(rprobe),
	}

	p.entries = append(p.entries, entry)
	observability.PoolSize.Set(float64(len(p.entries)))
}

// getFreshEntries timeout filtering
// getFreshEntries returns all entries younger than ProbeTimeout.
// Must be called with lock held.
func (p *Pool) getFreshEntries() []ProbeEntry {
	now := time.Now()
	fresh := make([]ProbeEntry, 0, len(p.entries))
	for _, e := range p.entries {
		if now.Sub(e.ReceivedAt) <= ProbeTimeout {
			fresh = append(fresh, e)
		}
	}
	return fresh
}

// computeThreshold dynamic QRIF
// computeThreshold computes θRIF as the QRIF-th percentile
// of RIF values in the given entries.
// Must be called with sorted RIF slice — see selectBestLocked.
/*
func computeThreshold(rifs []int64, qrif float64) int64 {
	if len(rifs) == 0 {
		return 0
	}
	idx := int(float64(len(rifs)-1) * qrif)
	return rifs[idx]
}
*/

// SelectBest — the routing decision
// SelectBest applies HCL to fresh pool entries and returns the
// best backend URL. Falls back to random if pool has < MinEntries.
func (p *Pool) SelectBest() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	fresh := p.getFreshEntries()
	observability.PoolSize.Set(float64(len(fresh)))

	if len(fresh) < MinEntries {
		observability.LBFallbackTotal.Inc()
		return p.randomFallback()
	}

	return p.applyHCL(fresh)
}

// applyHCL implements the Hot-Cold Lexicographic rule.
// Must be called with lock held.
/*
func (p *Pool) applyHCL(entries []ProbeEntry) string {
	// step 1 — compute θRIF from pool
	rifs := make([]int64, len(entries))
	for i, e := range entries {
		rifs[i] = e.RIF
	}
	sort.Slice(rifs, func(i, j int) bool { return rifs[i] < rifs[j] })
	threshold := computeThreshold(rifs, p.qrif)

	// step 2 classify hot and cold
	var cold, hot []ProbeEntry
	for _, e := range entries {
		if e.RIF <= threshold {
			cold = append(cold, e)
		} else {
			hot = append(hot, e)
		}
	}

	// step 3 route to best cold by effective latency
	// or best hot by lowest RIF if all hot
	var chosen ProbeEntry
	if len(cold) > 0 {
		chosen = cold[0]
		for _, e := range cold[1:] {
			if e.effectiveLatency() < chosen.effectiveLatency() {
				chosen = e
			}
		}
	} else {
		chosen = hot[0]
		for _, e := range hot[1:] {
			if e.RIF < chosen.RIF {
				chosen = e
			}
		}
	}

	// step 4  mark entry as used  remove if budget exhausted
	p.markUsed(chosen.URL)

	return chosen.URL
}
*/

// applyHCL delegates to the selector package.
// Must be called with lock held.
func (p *Pool) applyHCL(entries []ProbeEntry) string {
	// convert ProbeEntry to selector.Entry
	sel := make([]selector.Entry, len(entries))
	for i, e := range entries {
		sel[i] = selector.Entry{
			URL:       e.URL,
			RIF:       e.RIF,
			LatencyMs: e.LatencyMs,
			ErrorRate: e.ErrorRate,
		}
	}

	chosen := selector.ApplyHCL(sel, p.qrif)

	// mark chosen entry as used remove if budget exhausted
	p.markUsed(chosen)

	return chosen
}

// randomFallback picks a uniformly random backend.
// Must be called with lock held.
func (p *Pool) randomFallback() string {
	return p.backends[rand.IntN(len(p.backends))]
}

// CompensateRIF staleness defense #2
// CompensateRIF increments the cached RIF for the chosen server.
// Called immediately after routing a request before it arrives
// at the server — so the pool reflects our own routing decisions.
func (p *Pool) CompensateRIF(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.entries {
		if p.entries[i].URL == url {
			p.entries[i].RIF++
			return
		}
	}
}

// markUsed reuse budget tracking
// markUsed decrements the reuse budget for the given URL.
// Removes the entry if budget is exhausted.
// Must be called with lock held.
func (p *Pool) MarkUsed(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markUsed(url)
}

func (p *Pool) markUsed(url string) {
	for i := range p.entries {
		if p.entries[i].URL == url {
			if p.entries[i].shouldRemoveAfterUse() {
				p.removeAt(i)
				observability.PoolRemovalsTotal.WithLabelValues("budget").Inc()
			}
			return
		}
	}
}

// MaybeRemoveWorst degradation defense
// MaybeRemoveWorst runs the remove-worst process at rate rremove per query.
// Alternates between Strategy A (remove oldest) and Strategy B (remove worst by HCL).
func (p *Pool) MaybeRemoveWorst() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeAccum += p.rremove

	// remove floor(accumulator) times this query
	times := int(p.removeAccum)
	p.removeAccum -= float64(times)

	for i := 0; i < times; i++ {
		p.doRemoveWorst()
	}
}

// doRemoveWorst performs one alternating removal.
// Must be called with lock held.
func (p *Pool) doRemoveWorst() {
	if len(p.entries) == 0 {
		p.removeCount++ // preserve alternation state
		return
	}

	p.removeCount++

	if p.removeCount%2 == 0 {
		// Strategy A remove oldest (fights staleness)
		p.evictOldest()
		observability.PoolRemovalsTotal.WithLabelValues("oldest").Inc()
	} else {
		// Strategy B — remove worst by HCL (fights degradation)
		p.evictWorstByHCL()
		observability.PoolRemovalsTotal.WithLabelValues("worst").Inc()
	}
}

// Eviction helpers
// evictOldest removes the entry with the earliest ReceivedAt.
// Must be called with lock held.
func (p *Pool) evictOldest() {
	if len(p.entries) == 0 {
		return
	}
	oldest := 0
	for i, e := range p.entries {
		if e.ReceivedAt.Before(p.entries[oldest].ReceivedAt) {
			oldest = i
		}
	}
	p.removeAt(oldest)
}

// evictWorstByHCL removes the entry HCL would choose last.
// Hot entry with highest RIF, or if all cold the one with highest latency.
// Must be called with lock held.
/*
func (p *Pool) evictWorstByHCL() {
	if len(p.entries) == 0 {
		return
	}

	// compute threshold from current pool
	rifs := make([]int64, len(p.entries))
	for i, e := range p.entries {
		rifs[i] = e.RIF
	}
	sort.Slice(rifs, func(i, j int) bool { return rifs[i] < rifs[j] })
	threshold := computeThreshold(rifs, p.qrif)

	// find worst hot highest RIF among hot entries
	worstHot := -1
	for i, e := range p.entries {
		if e.RIF > threshold {
			if worstHot == -1 || e.RIF > p.entries[worstHot].RIF {
				worstHot = i
			}
		}
	}

	if worstHot >= 0 {
		p.removeAt(worstHot)
		return
	}

	// all cold  remove highest effective latency
	worstCold := 0
	for i, e := range p.entries {
		if e.effectiveLatency() > p.entries[worstCold].effectiveLatency() {
			worstCold = i
		}
	}
	p.removeAt(worstCold)
}
*/

// evictWorstByHCL removes the entry the selector considers worst.
// Must be called with lock held.
func (p *Pool) evictWorstByHCL() {
	if len(p.entries) == 0 {
		return
	}

	sel := make([]selector.Entry, len(p.entries))
	for i, e := range p.entries {
		sel[i] = selector.Entry{
			URL:       e.URL,
			RIF:       e.RIF,
			LatencyMs: e.LatencyMs,
			ErrorRate: e.ErrorRate,
		}
	}

	worstURL := selector.WorstByHCL(sel, p.qrif)

	for i, e := range p.entries {
		if e.URL == worstURL {
			p.removeAt(i)
			return
		}
	}
}

// removeAt removes the entry at index using O(1) swap-and-shrink.
// Must be called with lock held.
func (p *Pool) removeAt(idx int) {
	last := len(p.entries) - 1
	p.entries[idx] = p.entries[last]
	p.entries = p.entries[:last]
	observability.PoolSize.Set(float64(len(p.entries)))
}

// Diagnostics
// Size returns the current number of entries in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// FreshSize returns the number of non-expired entries.
func (p *Pool) FreshSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.getFreshEntries())
}
