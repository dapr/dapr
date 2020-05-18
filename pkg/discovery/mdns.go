// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package discovery

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/grandcat/zeroconf"
)

var log = logger.NewLogger("dapr.runtime.discovery")

// RegisterMDNS uses mdns to publish an entry of the service to a local network
func RegisterMDNS(id string, host string, ips []string, port int) error {
	go func() {
		host, _ := os.Hostname()
		info := []string{id}
		var server *zeroconf.Server
		var err error
		if len(ips) > 0 {
			server, err = zeroconf.RegisterProxy(host, id, "local.", port, host, ips, info, nil)
		} else {
			server, err = zeroconf.Register(host, id, "local.", port, info, nil)
		}
		if err != nil {
			log.Errorf("error from zeroconf register: %s", err)
			return
		}
		defer server.Shutdown()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
	}()
	return nil
}
