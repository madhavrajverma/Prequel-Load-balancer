package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// load balancer
	LBRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "prequal_lb_requests_total",
		Help: "Total requests received by the load balancer",
	})

	LBRequestLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "prequal_lb_request_latency_ms",
		Help:    "End-to-end request latency in milliseconds",
		Buckets: []float64{5, 10, 25, 50, 100, 200, 500, 1000, 2000, 5000},
	})

	LBFallbackTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "prequal_lb_fallback_total",
		Help: "Requests routed via random fallback due to empty pool",
	})

	LBErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "prequal_lb_errors_total",
		Help: "Requests that returned 5xx from backend",
	})

	//  pool
	PoolSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "prequal_pool_size",
		Help: "Number of fresh entries currently in the probe pool",
	})

	PoolRemovalsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "prequal_pool_removals_total",
		Help: "Probe entries removed — labelled by reason",
	}, []string{"reason"})

	// per-backend
	BackendRIF = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prequal_backend_rif",
		Help: "Current requests in flight per backend",
	}, []string{"backend"})

	BackendLatencyEst = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prequal_backend_latency_est_ms",
		Help: "Current latency estimate per backend from probe",
	}, []string{"backend"})

	BackendErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "prequal_backend_errors_total",
		Help: "5xx responses from each backend",
	}, []string{"backend"})

	BackendHealthy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prequal_backend_healthy",
		Help: "1 if backend is healthy 0 if not",
	}, []string{"backend"})

	BackendRoutingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "prequal_backend_routings_total",
		Help: "Requests routed to each backend",
	}, []string{"backend"})

	//  server-side (used by server replicas)
	ServerRIF = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "prequal_server_rif", // ← distinct name
		Help: "Current RIF on this server replica",
	}, []string{"server"})

	ServerRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "prequal_server_requests_total",
		Help: "Total requests handled per server",
	}, []string{"server"})

	ServerErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "prequal_server_errors_total",
		Help: "Error responses per server",
	}, []string{"server"})

	ServerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "prequal_server_latency_ms",
		Help:    "Request processing latency per server",
		Buckets: []float64{5, 10, 25, 50, 100, 200, 500, 1000},
	}, []string{"server"})
)
