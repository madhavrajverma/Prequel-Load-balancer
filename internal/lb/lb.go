package lb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"prequal/internal/observability"
	"prequal/internal/pool"
	"prequal/internal/wrr"
)

type Router interface {
	SelectBest() string
	CompensateRIF(url string)
	MarkUsed(url string)
	AddProbe(url string, rif int64, latencyMs int64, errorRate float64, rprobe float64)
	MaybeRemoveWorst()
}

// Config holds all load balancer configuration.
type Config struct {
	Backends  []string
	Algorithm string  // "prequal" or "wrr"
	QRIF      float64 // 0.84 recommended
	Rprobe    float64 // probes per query  2 or 3
	Rremove   float64 // removals per query  1
	Delta     float64 // pool drift factor  1.0
}

// Health state

type healthState struct {
	healthy  bool
	lastFail time.Time
}

// LoadBalancer
// LoadBalancer receives requests and forwards them
// to the best available backend using async probing
// and HCL routing via the probe pool.
type LoadBalancer struct {
	cfg Config
	//pool   *pool.Pool
	router Router
	client *http.Client // shared client for probing and forwarding

	// health tracking
	healthy   map[string]*healthState
	healthyMu sync.RWMutex

	// probe firing rate fractional accumulator
	probeAccum float64
	probeMu    sync.Mutex
}

// New constructs a LoadBalancer from config.
func New(cfg Config) *LoadBalancer {
	var router Router

	switch cfg.Algorithm {
	case "wrr":
		router = wrr.New(cfg.Backends)
		log.Printf("lb: using WRR algorithm")
	default:
		router = pool.New(pool.Config{
			Backends: cfg.Backends,
			QRIF:     cfg.QRIF,
			Rprobe:   cfg.Rprobe,
			Rremove:  cfg.Rremove,
			Delta:    cfg.Delta,
		})
		log.Printf("lb: using Prequal algorithm")
	}

	lb := &LoadBalancer{
		cfg:     cfg,
		router:  router,
		client:  &http.Client{Timeout: 5 * time.Second},
		healthy: make(map[string]*healthState),
	}

	for _, b := range cfg.Backends {
		lb.healthy[b] = &healthState{healthy: true}
		observability.BackendHealthy.WithLabelValues(b).Set(1)
	}

	go lb.runHealthChecker()
	go lb.runIdleProber()

	return lb
}

// Forward: the main request handler

// Forward handles an incoming request end-to-end:
//  1. Run remove-worst pool maintenance
//  2. Select best backend via HCL
//  3. Compensate RIF for chosen backend
//  4. Fire async probes for future requests
//  5. Forward request to chosen backend
//  6. Return response to client
func (lb *LoadBalancer) Forward(w http.ResponseWriter, r *http.Request) {
	observability.LBRequestsTotal.Inc()
	start := time.Now()

	// step 1 :pool maintenance
	lb.router.MaybeRemoveWorst()

	// step 2:select best backend
	chosen := lb.router.SelectBest()

	// step 3 :compensate RIF immediately
	lb.router.CompensateRIF(chosen)

	// step 4 :fire async probes in background
	go lb.fireProbes()

	// step 5 :forward to backend
	lb.forwardToBackend(chosen, w, r)

	// record end to end latency
	latencyMs := float64(time.Since(start).Milliseconds())
	observability.LBRequestLatency.Observe(latencyMs)
}

// forwardToBackend sends the request to the chosen backend
// and writes the response back to the client.
func (lb *LoadBalancer) forwardToBackend(
	backend string,
	w http.ResponseWriter,
	r *http.Request,
) {
	url := strings.TrimRight(backend, "/") + "/handle"

	// read request body so we can forward it
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	req, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		url,
		strings.NewReader(string(body)),
	)
	if err != nil {
		http.Error(w, "failed to build request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := lb.client.Do(req)
	if err != nil {
		// backend unreachable mark unhealthy
		lb.markUnhealthy(backend)
		observability.LBErrorsTotal.Inc()
		observability.BackendErrorsTotal.WithLabelValues(backend).Inc()
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		// backend returned server error  mark unhealthy
		lb.markUnhealthy(backend)
		observability.LBErrorsTotal.Inc()
		observability.BackendErrorsTotal.WithLabelValues(backend).Inc()
		http.Error(w, "backend error", http.StatusBadGateway)
		return
	}

	// success track routing
	observability.BackendRoutingsTotal.WithLabelValues(backend).Inc()

	// copy response back to client
	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// Async probing
// fireProbes sends rprobe probes to randomly sampled healthy backends.
// Runs in a background goroutine — never blocks the request path.
func (lb *LoadBalancer) fireProbes() {
	targets := lb.sampleHealthyBackends(lb.cfg.Rprobe)

	for _, url := range targets {
		rif, latencyMs, errorRate, err := lb.probe(url)
		if err != nil {
			// probe failed — do not add to pool
			continue
		}
		lb.router.AddProbe(url, rif, latencyMs, errorRate, lb.cfg.Rprobe)
		observability.BackendRIF.WithLabelValues(url).Set(float64(rif))
		observability.BackendLatencyEst.WithLabelValues(url).Set(float64(latencyMs))
	}
}

// probe sends a single GET /status request to a backend
// and returns its current RIF, latency estimate, and error rate.
func (lb *LoadBalancer) probe(backend string) (rif int64, latencyMs int64, errorRate float64, err error) {
	url := strings.TrimRight(backend, "/") + "/status"

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, 0, err
	}

	resp, err := lb.client.Do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()

	var result struct {
		RIF       int64   `json:"rif"`
		LatencyMs int64   `json:"latency_ms"`
		ErrorRate float64 `json:"error_rate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, 0, err
	}

	return result.RIF, result.LatencyMs, result.ErrorRate, nil
}

// sampleHealthyBackends returns up to n randomly sampled healthy backends.
// Sampling is without replacement.
// If fewer than n healthy backends exist returns all healthy ones.
func (lb *LoadBalancer) sampleHealthyBackends(n float64) []string {
	lb.healthyMu.RLock()
	healthy := make([]string, 0, len(lb.cfg.Backends))
	for _, b := range lb.cfg.Backends {
		if state, ok := lb.healthy[b]; ok && state.healthy {
			healthy = append(healthy, b)
		}
	}
	lb.healthyMu.RUnlock()

	if len(healthy) == 0 {
		// everything is unhealthy use all backends as fallback
		healthy = lb.cfg.Backends
	}

	// how many to sample this call  fractional rprobe support
	lb.probeMu.Lock()
	lb.probeAccum += n
	count := int(lb.probeAccum)
	lb.probeAccum -= float64(count)
	lb.probeMu.Unlock()

	if count <= 0 {
		return nil
	}
	if count >= len(healthy) {
		return healthy
	}

	// shuffle and take first count
	shuffled := make([]string, len(healthy))
	copy(shuffled, healthy)

	for i := len(shuffled) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	return shuffled[:count]
}

// runIdleProber fires probes at minimum rate during quiet periods.
// Prevents pool from emptying when no user traffic arrives.
func (lb *LoadBalancer) runIdleProber() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		// only fire idle probes for Prequal
		// WRR manages its own polling schedule

		if p, ok := lb.router.(*pool.Pool); ok {
			if p.FreshSize() < 2 {
				go lb.fireProbes()
			}
		}
	}
}

// Health checking
// runHealthChecker polls each backend's /health endpoint
// every 3 seconds and updates health state.
func (lb *LoadBalancer) runHealthChecker() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for _, backend := range lb.cfg.Backends {
			go lb.checkHealth(backend)
		}
	}
}

// checkHealth polls /health on one backend and updates its state.
func (lb *LoadBalancer) checkHealth(backend string) {
	url := strings.TrimRight(backend, "/") + "/health"

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		lb.markUnhealthy(backend)
		return
	}

	resp, err := lb.client.Do(req)
	if err != nil {
		lb.markUnhealthy(backend)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		lb.markUnhealthy(backend)
		return
	}

	// health check passed consider recovery
	lb.healthyMu.Lock()
	state := lb.healthy[backend]
	if !state.healthy && time.Since(state.lastFail) > 10*time.Second {
		state.healthy = true
		log.Printf("lb: backend %s recovered", backend)
		observability.BackendHealthy.WithLabelValues(backend).Set(1)
	}
	lb.healthyMu.Unlock()
}

// markUnhealthy marks a backend as unhealthy immediately.
func (lb *LoadBalancer) markUnhealthy(backend string) {
	lb.healthyMu.Lock()
	defer lb.healthyMu.Unlock()

	state := lb.healthy[backend]
	if state.healthy {
		log.Printf("lb: backend %s marked unhealthy", backend)
	}
	state.healthy = false
	state.lastFail = time.Now()
	observability.BackendHealthy.WithLabelValues(backend).Set(0)
}

// MetricsHandler exposes Prometheus metrics
// MetricsHandler returns an HTTP handler for Prometheus scraping.
func (lb *LoadBalancer) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "# Prequal metrics available at /metrics via promhttp\n")
	})
}

// Router returns the underlying Router interface.
// Used by the client package to call SelectBest directly.
func (lb *LoadBalancer) Router() Router {
	return lb.router
}

// MarkUnhealthy exposes the internal markUnhealthy for the client package.
func (lb *LoadBalancer) MarkUnhealthy(url string) {
	lb.markUnhealthy(url)
}
