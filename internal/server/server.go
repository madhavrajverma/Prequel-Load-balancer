package server

import (
	"encoding/json"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"prequal/internal/observability"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	numBuckets       = 10
	samplesPerBucket = 8
	unknownPenalty   = 1000 // ms returned when no samples exist
)

// rifToBucket maps a RIF value to a bucket index.
// Fine grained at low RIF, coarse at high RIF.
func rifToBucket(rif int64) int {
	switch {
	case rif <= 0:
		return 0
	case rif <= 1:
		return 1
	case rif <= 2:
		return 2
	case rif <= 4:
		return 3
	case rif <= 7:
		return 4
	case rif <= 11:
		return 5
	case rif <= 17:
		return 6
	case rif <= 25:
		return 7
	case rif <= 40:
		return 8
	default:
		return 9
	}
}

type latencyBucket struct {
	mu      sync.Mutex
	samples [samplesPerBucket]int64
	head    int
	count   int
}

func (b *latencyBucket) record(ms int64) {
	b.mu.Lock()
	b.samples[b.head] = ms
	b.head = (b.head + 1) % samplesPerBucket
	if b.count < samplesPerBucket {
		b.count++
	}
	b.mu.Unlock()
}

// median returns the median latency or 0 if no samples.
func (b *latencyBucket) median() int64 {
	b.mu.Lock()
	count := b.count
	if count == 0 {
		b.mu.Unlock()
		return 0
	}
	buf := make([]int64, count)
	copy(buf, b.samples[:count])
	b.mu.Unlock()

	sort.Slice(buf, func(i, j int) bool { return buf[i] < buf[j] })
	return buf[count/2]
}

type latencyTracker struct {
	buckets [numBuckets]latencyBucket
}

// record stores a latency sample tagged with the RIF at request arrival.
func (t *latencyTracker) record(ms int64, arrivalRIF int64) {
	t.buckets[rifToBucket(arrivalRIF)].record(ms)
}

// estimate returns the median latency at the given current RIF level.
// Falls back to nearest populated bucket, then unknownPenalty.
func (t *latencyTracker) estimate(currentRIF int64) int64 {
	idx := rifToBucket(currentRIF)

	if m := t.buckets[idx].median(); m > 0 {
		return m
	}

	// search outward for nearest populated bucket
	for offset := 1; offset < numBuckets; offset++ {
		if below := idx - offset; below >= 0 {
			if m := t.buckets[below].median(); m > 0 {
				return m
			}
		}
		if above := idx + offset; above < numBuckets {
			if m := t.buckets[above].median(); m > 0 {
				return m
			}
		}
	}

	return unknownPenalty
}

// server modes
const (
	ModeNormal int32 = iota
	ModeSlow
	ModeError
	ModeCPU
	ModeKill
)

type Server struct {
	id        string
	baseDelay time.Duration

	rif     int64 // atomic requests in flight
	latency latencyTracker

	// error tracking for sinkhole prevention
	requestCount int64 // atomic
	errorCount   int64 // atomic

	mode    int32 // atomic  current injection mode
	cpuStop chan struct{}
	cpuOnce sync.Once
}

// New creates a Server with the given id and base processing delay.
func New(id string, baseDelay time.Duration) *Server {
	return &Server{
		id:        id,
		baseDelay: baseDelay,
		cpuStop:   make(chan struct{}),
	}
}

func (s *Server) setMode(m int32) {
	atomic.StoreInt32(&s.mode, m)
}

func (s *Server) getMode() int32 {
	return atomic.LoadInt32(&s.mode)
}

func (s *Server) startCPUBurn() {
	s.cpuOnce.Do(func() {
		go func() {
			log.Printf("server %s: CPU burn started", s.id)
			for {
				select {
				case <-s.cpuStop:
					log.Printf("server %s: CPU burn stopped", s.id)
					return
				default:
					// busy loop burns one core
					_ = 0
				}
			}
		}()
	})
}

func (s *Server) stopCPUBurn() {
	select {
	case s.cpuStop <- struct{}{}:
	default:
	}
	// reset once so burn can be started again
	s.cpuOnce = sync.Once{}
	s.cpuStop = make(chan struct{})
}

// handle route procces a real requests
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// increment RIF at arrival  record arrival level for latency bucketing
	rifAtArrival := atomic.AddInt64(&s.rif, 1)
	defer atomic.AddInt64(&s.rif, -1)

	observability.ServerRIF.WithLabelValues(s.id).Set(
		float64(atomic.LoadInt64(&s.rif)),
	)
	atomic.AddInt64(&s.requestCount, 1)
	observability.ServerRequestsTotal.WithLabelValues(s.id).Inc()

	mode := s.getMode()

	// error mode return immediately without doing real work
	if mode == ModeError {
		atomic.AddInt64(&s.errorCount, 1)
		observability.ServerErrorsTotal.WithLabelValues(s.id).Inc()
		http.Error(w, "simulated error", http.StatusInternalServerError)
		return
	}

	// kill mode  exit process after short delay
	if mode == ModeKill {
		http.Error(w, "server killed", http.StatusServiceUnavailable)
		go func() {
			time.Sleep(50 * time.Millisecond)
			log.Printf("server %s: exiting (kill mode)", s.id)
			os.Exit(1)
		}()
		return
	}

	// start CPU burn if in CPU mode (idempotent via sync.Once)
	if mode == ModeCPU {
		s.startCPUBurn()
	}

	// compute processing delay
	jitter := time.Duration(rand.IntN(50)) * time.Millisecond
	delay := s.baseDelay + jitter

	if mode == ModeSlow {
		delay += 200 * time.Millisecond
	}

	// occasional random stall — 1% chance — simulates GC pause
	if rand.IntN(100) == 0 {
		delay += 150 * time.Millisecond
	}

	start := time.Now()
	time.Sleep(delay)
	latencyMs := time.Since(start).Milliseconds()

	// record latency tagged with RIF at arrival
	s.latency.record(latencyMs, rifAtArrival)
	observability.ServerLatency.WithLabelValues(s.id).Observe(float64(latencyMs))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"server":        s.id,
		"processing_ms": latencyMs,
	})
}

// status route probe end point for load balancer
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	currentRIF := atomic.LoadInt64(&s.rif)
	latEst := s.latency.estimate(currentRIF)

	// compute error rate for sinkhole prevention
	reqs := atomic.LoadInt64(&s.requestCount)
	errs := atomic.LoadInt64(&s.errorCount)
	var errorRate float64
	if reqs > 0 {
		errorRate = float64(errs) / float64(reqs)
	}

	observability.BackendRIF.WithLabelValues(s.id).Set(float64(currentRIF))
	observability.BackendLatencyEst.WithLabelValues(s.id).Set(float64(latEst))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"server":     s.id,
		"rif":        currentRIF,
		"latency_ms": latEst,
		"error_rate": errorRate,
	})
}

// health route for checking server health
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	mode := s.getMode()

	// functional failures means unhealthy
	if mode == ModeError || mode == ModeKill {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}

	// performance degradation  still healthy
	// HCL will naturally route less traffic here via /status signals
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// /control route  experiment failure injection
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")

	switch mode {
	case "normal":
		s.stopCPUBurn()
		s.setMode(ModeNormal)
		// reset error counters when returning to normal
		atomic.StoreInt64(&s.errorCount, 0)
		atomic.StoreInt64(&s.requestCount, 0)

	case "slow":
		s.setMode(ModeSlow)

	case "error":
		s.setMode(ModeError)

	case "cpu":
		s.setMode(ModeCPU)
		s.startCPUBurn()

	case "kill":
		s.setMode(ModeKill)

	default:
		http.Error(w, "unknown mode: "+mode, http.StatusBadRequest)
		return
	}

	log.Printf("server %s: mode set to %s", s.id, mode)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"server": s.id,
		"mode":   mode,
	})
}

// Start register routes and listen
// Start registers all HTTP endpoints and begins listening on addr.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/handle", s.handle)
	mux.HandleFunc("/status", s.status)
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/control", s.control)
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("server %s listening on %s (baseDelay=%v)", s.id, addr, s.baseDelay)
	return srv.ListenAndServe()
}
