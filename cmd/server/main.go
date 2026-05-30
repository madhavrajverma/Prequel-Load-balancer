package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/madhavrajverma/Prequel-Load-balancer/internal/server"
)

func main() {
	id := flag.String("id", "server1", "unique server replica ID")
	port := flag.Int("port", 9001, "port to listen on")
	baseDelay := flag.Duration("base-delay", 50*time.Millisecond, "base request processing delay")
	flag.Parse()

	s := server.New(*id, *baseDelay)

	addr := fmt.Sprintf(":%d", *port)
	if err := s.Start(addr); err != nil {
		log.Fatalf("server %s failed: %v", *id, err)
	}
}
