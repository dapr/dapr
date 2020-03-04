// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package discovery

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/grandcat/zeroconf"
)

var log = logger.NewLogger("dapr.runtime.discovery")

// RegisterMDNS uses mdns to publish an entry of the service to a local network
func RegisterMDNS(id string, grpcPort int, httpPort int, metricsPort int, metricsEnabled bool, profilingPort int, profilingEnabled bool) error {
	go func() {
		host, _ := os.Hostname()
		info := []string{id}
		server, err := zeroconf.Register(host, id, "local.", grpcPort, info, nil)
		if err != nil {
			log.Errorf("error from zeroconf register: %s", err)
			return
		}
		defer server.Shutdown()

		generalInfo := []string {
			fmt.Sprintf("appId=%s", id),
			fmt.Sprintf("grpcPort=%d", grpcPort),
			fmt.Sprintf("httpPort=%d", httpPort),
		}
		if metricsEnabled {
			generalInfo = append(generalInfo, fmt.Sprintf("metricsPort=%d", metricsPort))
		}
		if profilingEnabled {
			generalInfo = append(generalInfo, fmt.Sprintf("profilingPort=%d", profilingPort))
		}
		generalServer, err := zeroconf.Register(id, "_dapr._tcp", "local.", grpcPort, generalInfo, nil)
		if err != nil {
			log.Errorf("error from general zeroconf register: %s", err)
		}
		defer generalServer.Shutdown()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
	}()
	return nil
}

// LookupPortMDNS uses mdns to find the port of a given service entry on a local network
func LookupPortMDNS(id string) (int, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalf("Failed to initialize resolver: %v", err.Error())
	}

	port := -1
	entries := make(chan *zeroconf.ServiceEntry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*1)
	defer cancel()

	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			for _, text := range entry.Text {
				if text == id {
					port = entry.Port
					cancel()
					return
				}
			}
		}
	}(entries)

	err = resolver.Browse(ctx, id, "local.", entries)
	if err != nil {
		return -1, fmt.Errorf("failed to browse: %s", err.Error())
	}

	<-ctx.Done()
	if port == -1 {
		return port, fmt.Errorf("couldn't find service: %s", id)
	}
	return port, nil
}
