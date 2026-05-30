package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Stats tracker
type stats struct {
	requests  int64
	errors    int64
	totalMs   int64
	p50Bucket [5000]int32 // 1ms buckets up to 5000ms
}

func (s *stats) record(latencyMs int64, isError bool) {
	atomic.AddInt64(&s.requests, 1)
	if isError {
		atomic.AddInt64(&s.errors, 1)
		return
	}
	atomic.AddInt64(&s.totalMs, latencyMs)

	// bucket for percentile tracking
	idx := latencyMs
	if idx >= 5000 {
		idx = 4999
	}
	if idx < 0 {
		idx = 0
	}
	atomic.AddInt32(&s.p50Bucket[idx], 1)
}

func (s *stats) percentile(p float64) int64 {
	total := atomic.LoadInt64(&s.requests) - atomic.LoadInt64(&s.errors)
	if total == 0 {
		return 0
	}

	target := int64(math.Ceil(float64(total) * p))
	var cumulative int64

	for ms := int64(0); ms < 5000; ms++ {
		cumulative += int64(atomic.LoadInt32(&s.p50Bucket[ms]))
		if cumulative >= target {
			return ms
		}
	}
	return 4999
}

func (s *stats) print(elapsed time.Duration) {
	reqs := atomic.LoadInt64(&s.requests)
	errs := atomic.LoadInt64(&s.errors)
	success := reqs - errs

	var meanMs float64
	if success > 0 {
		meanMs = float64(atomic.LoadInt64(&s.totalMs)) / float64(success)
	}

	rps := float64(reqs) / elapsed.Seconds()

	fmt.Printf(
		"[%5.0fs] requests=%d errors=%d rps=%.0f mean=%.0fms p50=%dms p99=%dms p999=%dms\n",
		elapsed.Seconds(),
		reqs,
		errs,
		rps,
		meanMs,
		s.percentile(0.50),
		s.percentile(0.99),
		s.percentile(0.999),
	)
}

// Response shape from backend

type response struct {
	Server       string `json:"server"`
	ProcessingMs int64  `json:"processing_ms"`
}

// Worker sends requests in a loop

func worker(
	id int,
	target string,
	client *http.Client,
	tasks <-chan struct{},
	st *stats,
	verbose bool,
) {
	for range tasks {
		start := time.Now()

		req, err := http.NewRequest(http.MethodPost, target, nil)
		if err != nil {
			st.record(0, true)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		latencyMs := time.Since(start).Milliseconds()

		if err != nil {
			st.record(latencyMs, true)
			if verbose {
				log.Printf("worker %d: error %v", id, err)
			}
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		isError := resp.StatusCode >= 400
		st.record(latencyMs, isError)

		if verbose && !isError {
			var r response
			if err := json.Unmarshal(body, &r); err == nil {
				log.Printf("worker %d: %s %dms", id, r.Server, latencyMs)
			}
		}
	}
}

// normalRPS — variable query cost
// The paper draws request cost from a normal distribution
// with stddev = mean. We implement this by varying
// the inter-arrival time, which simulates variable load.

// nextInterval returns the next inter-request interval
// drawn from a normal distribution around 1/rps.
// Clamped to prevent negative or excessively long intervals.
func nextInterval(rps float64) time.Duration {
	mean := 1.0 / rps
	stddev := mean // stddev = mean — matches paper's variable cost model

	// Box-Muller transform for normal distribution
	u1 := rand.Float64()
	u2 := rand.Float64()
	z := math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2*math.Pi*u2)

	interval := mean + stddev*z

	// clamp — no negative intervals, max 3x mean
	if interval < mean*0.1 {
		interval = mean * 0.1
	}
	if interval > mean*3.0 {
		interval = mean * 3.0
	}

	return time.Duration(interval * float64(time.Second))
}

func main() {
	target := flag.String("target", "http://localhost:8080/api",
		"load balancer endpoint")
	rps := flag.Float64("rps", 100,
		"target requests per second")
	duration := flag.Duration("dur", 60*time.Second,
		"how long to run")
	concurrency := flag.Int("c", 50,
		"number of concurrent worker goroutines")
	verbose := flag.Bool("v", false,
		"print each request")
	variable := flag.Bool("variable", true,
		"use variable inter-arrival times (normal distribution)")
	timeout := flag.Duration("timeout", 5*time.Second,
		"per-request timeout")
	flag.Parse()

	log.Printf("load generator starting")
	log.Printf("  target:      %s", *target)
	log.Printf("  rps:         %.0f", *rps)
	log.Printf("  duration:    %v", *duration)
	log.Printf("  concurrency: %d", *concurrency)
	log.Printf("  variable:    %v", *variable)

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	st := &stats{}
	tasks := make(chan struct{}, *concurrency*2)

	// start workers
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, *target, client, tasks, st, *verbose)
		}(i)
	}

	// stats printer  prints every 5 seconds
	startTime := time.Now()
	stopPrinter := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				st.print(time.Since(startTime))
			case <-stopPrinter:
				return
			}
		}
	}()

	// signal handler — graceful shutdown on ctrl+c
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// producer  sends tasks at configured rate
	deadline := time.After(*duration)
	done := false

	for !done {
		select {
		case <-deadline:
			done = true
		case <-sigCh:
			log.Printf("shutting down...")
			done = true
		default:
			// send one task token
			select {
			case tasks <- struct{}{}:
			default:
				// workers busy — skip this tick
			}

			// wait before next request
			var interval time.Duration
			if *variable {
				interval = nextInterval(*rps)
			} else {
				interval = time.Duration(float64(time.Second) / *rps)
			}
			time.Sleep(interval)
		}
	}

	close(tasks)
	wg.Wait()
	close(stopPrinter)

	// final stats
	elapsed := time.Since(startTime)
	fmt.Println("\n final results")
	st.print(elapsed)
	fmt.Printf("total duration: %v\n", elapsed.Round(time.Millisecond))
}
