ZeroConf: Service Discovery with mDNS
=====================================
ZeroConf is a pure Golang library that employs Multicast DNS-SD for

* browsing and resolving services in your network
* registering own services

in the local network.

It basically implements aspects of the standards
[RFC 6762](https://tools.ietf.org/html/rfc6762) (mDNS) and
[RFC 6763](https://tools.ietf.org/html/rfc6763) (DNS-SD).
Though it does not support all requirements yet, the aim is to provide a complient solution in the long-term with the community.

By now, it should be compatible to [Avahi](http://avahi.org/) (tested) and Apple's Bonjour (untested).
Target environments: private LAN/Wifi, small or isolated networks.

[![GoDoc](https://godoc.org/github.com/grandcat/zeroconf?status.svg)](https://godoc.org/github.com/grandcat/zeroconf)
[![Go Report Card](https://goreportcard.com/badge/github.com/grandcat/zeroconf)](https://goreportcard.com/report/github.com/grandcat/zeroconf)
[![Build Status](https://travis-ci.com/grandcat/zeroconf.svg?branch=master)](https://travis-ci.com/grandcat/zeroconf)

## Install
Nothing is as easy as that:
```bash
$ go get -u github.com/grandcat/zeroconf
```
This package requires **Go 1.7** (context in std lib) or later.

## Browse for services in your local network

```go
// Discover all services on the network (e.g. _workstation._tcp)
resolver, err := zeroconf.NewResolver(nil)
if err != nil {
    log.Fatalln("Failed to initialize resolver:", err.Error())
}

entries := make(chan *zeroconf.ServiceEntry)
go func(results <-chan *zeroconf.ServiceEntry) {
    for entry := range results {
        log.Println(entry)
    }
    log.Println("No more entries.")
}(entries)

ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
defer cancel()
err = resolver.Browse(ctx, "_workstation._tcp", "local.", entries)
if err != nil {
    log.Fatalln("Failed to browse:", err.Error())
}

<-ctx.Done()
```
See https://github.com/grandcat/zeroconf/blob/master/examples/resolv/client.go.

## Lookup a specific service instance

```go
// Example filled soon.
```

## Register a service

```go
server, err := zeroconf.Register("GoZeroconf", "_workstation._tcp", "local.", 42424, []string{"txtv=0", "lo=1", "la=2"}, nil)
if err != nil {
    panic(err)
}
defer server.Shutdown()

// Clean exit.
sig := make(chan os.Signal, 1)
signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
select {
case <-sig:
    // Exit by user
case <-time.After(time.Second * 120):
    // Exit by timeout
}

log.Println("Shutting down.")
```
See https://github.com/grandcat/zeroconf/blob/master/examples/register/server.go.

## Features and ToDo's
This list gives a quick impression about the state of this library.
See what needs to be done and submit a pull request :)

* [x] Browse / Lookup / Register services
* [x] Multiple IPv6 / IPv4 addresses support
* [x] Send multiple probes (exp. back-off) if no service answers (*)
* [ ] Timestamp entries for TTL checks
* [ ] Compare new multicasts with already received services

_Notes:_

(*) The denoted functionalities might not be 100% standard conform, but should not be a deal breaker.
    Some test scenarios demonstrated that the overall robustness and performance increases when applying the suggested improvements.

## Credits
Great thanks to [hashicorp](https://github.com/hashicorp/mdns) and to [oleksandr](https://github.com/oleksandr/bonjour) and all contributing authors for the code this projects bases upon.
Large parts of the code are still the same.

However, there are several reasons why I decided to create a fork of the original project:
The previous project seems to be unmaintained. There are several useful pull requests waiting. I merged most of them in this project.
Still, the implementation has some bugs and lacks some other features that make it quite unreliable in real LAN environments when running continously.
Last but not least, the aim for this project is to build a solution that targets standard conformance in the long term with the support of the community.
Though, resiliency should remain a top goal.
