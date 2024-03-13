/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"go.etcd.io/etcd/server/v3/embed"
)

func (s *Server) conf() *embed.Config {
	config := embed.NewConfig()

	config.Name = s.etcdID
	config.Dir = s.dataDir

	config.InitialCluster = s.etcdInitialPeers

	advertisePeerURLs, err := parseURLs(s.etcdID, s.etcdInitialPeers)
	if err != nil {
		log.Warnf("Invalid format for initial cluster: %s", err)
	}
	log.Infof("CASSIE: advertisePeerURLs %+v", advertisePeerURLs)

	//config.AdvertisePeerUrls = advertisePeerURLs
	log.Infof("CASSIE: initialCluster %s", s.etcdInitialPeers)

	// TODO: cassie use input from etcdInitialPeers
	//hostAddress, err := utils.GetHostAddress()
	//if err != nil {
	//	log.Fatal(fmt.Errorf("failed to determine host address: %w", err))
	//}

	etcdUrl, peerPort, err := peerHostAndPort(s.etcdID, s.etcdInitialPeers)
	config.AdvertisePeerUrls = []url.URL{{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%s", etcdUrl, peerPort),
	}}

	log.Infof("CASSIE: peerPort %s, etcdURL %s", peerPort, etcdUrl)
	if err != nil {
		log.Warnf("Invalid format for initial cluster port. Make sure to include 'http://' in Scheduler URL")
	}

	config.ListenPeerUrls = []url.URL{{
		Scheme: "http",
		//Host:   "0.0.0.0:" + peerPort,
		Host: fmt.Sprintf("%s:%s", etcdUrl, peerPort),
	}}

	idToClientPort := make(map[string]string)

	// Populate map
	for _, str := range s.etcdClientPorts {
		parts := strings.Split(str, "=")
		if len(parts) != 2 {
			fmt.Printf("Invalid format: %s\n", str)
			continue
		}

		id := strings.TrimSpace(parts[0])
		port := strings.TrimSpace(parts[1])
		idToClientPort[id] = port
	}

	fmt.Printf("\nCASSIE: idToClientPort: %+v\n", idToClientPort)
	fmt.Printf("\nCASSIE: idToClientPort[s.etcdID]: %+v\n", idToClientPort[s.etcdID])

	config.ListenClientUrls = []url.URL{{
		Scheme: "http",
		//Host:   fmt.Sprintf("%s:%s", etcdUrl, strconv.Itoa(s.etcdClientPorts)),
		Host: fmt.Sprintf("%s:%s", etcdUrl, idToClientPort[s.etcdID]),
	}}
	config.AdvertiseClientUrls = []url.URL{{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%s", etcdUrl, idToClientPort[s.etcdID]),
	}}

	config.LogLevel = "debug" // Only supports debug, info, warn, error, panic, or fatal. Default 'info'.
	// TODO: Look into etcd config and if we need to do any raft compacting

	log.Debugf("CASSIE: etcd config: %+v", config)

	return config
}

func parseURLs(name, input string) ([]url.URL, error) {
	//var urls []url.URL

	pairs := strings.Split(input, ",")

	for _, pair := range pairs {
		idAndAddress := strings.SplitN(pair, "=", 2)
		if len(idAndAddress) != 2 {
			return nil, fmt.Errorf("invalid format for: %s. Format should be: <id>=<ip>:<port>", pair)
		}
		idCandidate := strings.TrimPrefix(idAndAddress[0], "http://")

		if idCandidate == name {
			// Construct the URL without "http://"
			u, err := url.Parse(idAndAddress[1])
			if err != nil {
				return nil, fmt.Errorf("error parsing URL: %w", err)
			}
			log.Infof("CASSIE: AdvertisePeerUrl %+v", *u)
			//urls = append(urls, *u)
			return []url.URL{*u}, nil
		}
	}
	//log.Infof("CASSIE: URLS %+v", urls)
	return []url.URL{}, fmt.Errorf("AdvertisePeerUrl not found for id: %s", name)
}

func peerHostAndPort(name, input string) (string, string, error) {
	if strings.Contains(input, ",") {
		pairs := strings.Split(input, ",")
		for i, pair := range pairs {
			idAndAddress := strings.SplitN(pairs[i], "=", 2)
			if len(idAndAddress) != 2 {
				return "", "", fmt.Errorf("invalid format: %s", pair)
			}
			idCandidate := strings.TrimPrefix(idAndAddress[0], "http://")
			log.Infof("CASSIE*** BEFORE id=name pair: %v && idAndAddress: %v", pair, idAndAddress)

			if idCandidate == name {
				u, err := url.Parse(idAndAddress[1])
				if err != nil {
					return "", "", fmt.Errorf("error parsing URL: %w", err)
				}

				log.Infof("CASSIE*** in id=name u: %v", u)
				host, port, err := net.SplitHostPort(u.Host)
				log.Infof("CASSIE*** in id=name port: %v", port)

				if err != nil {
					return "", "", fmt.Errorf("error extracting port: %w", err)
				}

				//hostAndPort := strings.Split(input, ":")

				//return port, strings.TrimPrefix(idAndAddress[1], "http://"), nil
				return host, port, nil
			}
		}
	}

	// If there's no comma, split by ":" to get the port
	idAndAddress := strings.SplitN(input, "=", 2)
	if len(idAndAddress) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", input)
	}

	u, err := url.Parse(idAndAddress[1])
	if err != nil {
		return "", "", fmt.Errorf("error parsing URL: %w", err)
	}

	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", "", fmt.Errorf("error extracting port: %w", err)
	}

	return host, port, nil
}
