package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/httpapi"
	"github.com/rottentail/solo-0043-emberstripe/internal/lightbench"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7431", "station listen address")
	scans := flag.String("scans", "var/scans", "catalog of optical scans")
	output := flag.String("parcels", "var/parcels", "parcel output directory")
	flag.Parse()
	s, err := station.Open(*scans, *output)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	api := httpapi.New(s)
	mux.Handle("/captures", api)
	mux.Handle("/captures/", api)
	mux.Handle("/parcels/", api)
	mux.Handle("/handoff/", api)
	mux.Handle("/", lightbench.New(s))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Emberstripe listening on %s", listener.Addr())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(deadline); err != nil {
			log.Fatal(err)
		}
	}
}
