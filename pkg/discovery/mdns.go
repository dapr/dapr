// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package discovery

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	go_log "log"

	log "github.com/Sirupsen/logrus"
	"github.com/hashicorp/mdns"
)

// RegisterMDNS uses mdns to publish an entry of the service to a local network
func RegisterMDNS(id string, port int) error {
	go_log.SetOutput(ioutil.Discard)
	host, _ := os.Hostname()
	info := []string{id}

	service, err := mdns.NewMDNSService(host, id, "", "", port, nil, info)
	if err != nil {
		return err
	}

	go func(srv *mdns.MDNSService) {
		server, err := mdns.NewServer(&mdns.Config{Zone: service})
		if err != nil {
			log.Errorf("error from mdns: %s", err)
			return
		}
		defer server.Shutdown()
		<-(chan int)(nil)
	}(service)
	return nil
}

// LookupPortMDNS uses mdns to find the port of a given service entry on a local network
func LookupPortMDNS(id string) (int, error) {
	go_log.SetOutput(ioutil.Discard)

	entriesCh := make(chan *mdns.ServiceEntry, 1)
	done := make(chan int, 1)
	defer close(done)
	defer close(entriesCh)

	go func(done chan int) {
		for entry := range entriesCh {
			log.Debugf("mdns: got new entry: %v\n", entry)

			if entry.Info == id {
				done <- entry.Port
			}
		}
	}(done)

	port := -1
	err := mdns.Lookup(id, entriesCh)
	if err != nil {
		return -1, err
	}

	select {
	case <-time.After(time.Millisecond * 100):
		break
	case port = <-done:
		break
	}

	if port == -1 {
		return port, fmt.Errorf("couldn't find service entry for id %s", id)
	}
	return port, nil
}
