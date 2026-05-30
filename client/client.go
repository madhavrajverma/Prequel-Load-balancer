package client

import (
	"net/http"
	"time"

	"github.com/madhavrajverma/Prequel-Load-balancer/internal/lb"
	"github.com/madhavrajverma/Prequel-Load-balancer/internal/pool"
	"github.com/madhavrajverma/Prequel-Load-balancer/internal/wrr"
)

type Config struct {
	Backends []string

	Algorithm string

	QRIF float64

	Rprobe float64

	Rremove float64

	Delta float64

	Timeout time.Duration
}

// defaults fills in zero values with sensible defaults.
func (c *Config) defaults() {
	if c.Algorithm == "" {
		c.Algorithm = "prequal"
	}
	if c.QRIF == 0 {
		c.QRIF = 0.84
	}
	if c.Rprobe == 0 {
		c.Rprobe = 2.0
	}
	if c.Rremove == 0 {
		c.Rremove = 1.0
	}
	if c.Delta == 0 {
		c.Delta = 1.0
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
}

type LoadBalancer struct {
	inner *lb.LoadBalancer
}

func New(cfg Config) *LoadBalancer {
	cfg.defaults()

	inner := lb.New(lb.Config{
		Backends:  cfg.Backends,
		Algorithm: cfg.Algorithm,
		QRIF:      cfg.QRIF,
		Rprobe:    cfg.Rprobe,
		Rremove:   cfg.Rremove,
		Delta:     cfg.Delta,
	})

	return &LoadBalancer{inner: inner}
}

func (l *LoadBalancer) Forward(w http.ResponseWriter, r *http.Request) {
	l.inner.Forward(w, r)
}

func (l *LoadBalancer) SelectBest() string {
	return l.inner.Router().SelectBest()
}

func (l *LoadBalancer) MarkUnhealthy(url string) {
	l.inner.MarkUnhealthy(url)
}

func (l *LoadBalancer) MetricsHandler() http.Handler {
	return l.inner.MetricsHandler()
}

type Router interface {
	SelectBest() string
	MarkUnhealthy(url string)
	Forward(w http.ResponseWriter, r *http.Request)
}

var _ Router = (*LoadBalancer)(nil)

func NewRoundRobin(backends []string) *LoadBalancer {
	return New(Config{
		Backends:  backends,
		Algorithm: "rr",
	})
}

func NewWRR(backends []string) *LoadBalancer {
	return New(Config{
		Backends:  backends,
		Algorithm: "wrr",
	})
}

var _ = pool.MaxSize
var _ = wrr.New
