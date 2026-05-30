package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"prequal/internal/lb"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	backends := flag.String("backends",
		"http://localhost:9001,http://localhost:9002,http://localhost:9003",
		"comma-separated backend URLs")
	port := flag.Int("port", 8080, "port to listen on")
	algo := flag.String("algo", "prequal", "algorithm: prequal or wrr")
	qrif := flag.Float64("qrif", 0.84, "QRIF quantile threshold")
	rprobe := flag.Float64("rprobe", 2.0, "probes per query")
	rremove := flag.Float64("rremove", 1.0, "removals per query")
	delta := flag.Float64("delta", 1.0, "pool drift factor")
	flag.Parse()

	backendList := strings.Split(*backends, ",")
	for i := range backendList {
		backendList[i] = strings.TrimSpace(backendList[i])
	}

	balancer := lb.New(lb.Config{
		Backends:  backendList,
		Algorithm: *algo,
		QRIF:      *qrif,
		Rprobe:    *rprobe,
		Rremove:   *rremove,
		Delta:     *delta,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/api", balancer.Forward)
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("load balancer starting on %s algo=%s backends=%v",
		addr, *algo, backendList)
	log.Fatal(srv.ListenAndServe())
}
