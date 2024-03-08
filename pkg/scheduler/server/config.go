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
	"strconv"
	"strings"

	"github.com/dapr/dapr/utils"
	"go.etcd.io/etcd/server/v3/embed"
)

func parseEtcdUrls(strs []string) ([]url.URL, error) {
	urls := make([]url.URL, 0, len(strs))
	for _, str := range strs {
		u, err := url.Parse(str)
		if err != nil {
			return nil, fmt.Errorf("invalid url %s: %s", str, err)
		}
		urls = append(urls, *u)
	}

	return urls, nil
}

func (s *Server) conf() *embed.Config {
	config := embed.NewConfig()

	config.Name = s.etcdID
	config.Dir = s.dataDir

	config.InitialCluster = s.etcdInitialPeers

	advertisePeerURLs, err := parseURLs(s.etcdInitialPeers)
	if err != nil {
		log.Warnf("Invalid format for initial cluster")
	}
	config.AdvertisePeerUrls = advertisePeerURLs

	peerPort, err := extractSinglePeerPort(s.etcdInitialPeers)
	if err != nil {
		log.Warnf("Invalid format for initial cluster port")
	}
	config.ListenPeerUrls = []url.URL{{
		Scheme: "http",
		Host:   "0.0.0.0:" + peerPort,
	}}

	hostAddress, err := utils.GetHostAddress()
	if err != nil {
		log.Fatal(fmt.Errorf("failed to determine host address: %w", err))
	}

	config.ListenClientUrls = []url.URL{{
		Scheme: "http",
		Host:   hostAddress + ":" + strconv.Itoa(s.etcdClientPort),
	}}
	config.AdvertiseClientUrls = []url.URL{{
		Scheme: "http",
		Host:   hostAddress + ":" + strconv.Itoa(s.etcdClientPort),
	}}

	config.LogLevel = "info" // Only supports debug, info, warn, error, panic, or fatal. Default 'info'.
	// TODO: Look into etcd config and if we need to do any raft compacting

	return config
}

func parseURLs(input string) ([]url.URL, error) {
	var urls []url.URL

	pairs := strings.Split(input, ",")

	for _, pair := range pairs {
		idAndAddress := strings.SplitN(pair, "=", 2)
		if len(idAndAddress) != 2 {
			return nil, fmt.Errorf("invalid format: %s", pair)
		}

		// Construct the URL without "http://"
		u, err := url.Parse(idAndAddress[1])
		if err != nil {
			return nil, fmt.Errorf("error parsing URL: %w", err)
		}

		urls = append(urls, *u)
	}

	return urls, nil
}

func extractSinglePeerPort(input string) (string, error) {
	parts := strings.Split(input, "=")

	if len(parts) != 2 {
		return "", fmt.Errorf("invalid format: %s", input)
	}

	// Extract the port
	u, err := url.Parse(parts[1])
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %w", err)
	}

	// Check if the port is in the URL
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", fmt.Errorf("error extracting port: %w", err)
	}

	return port, nil
}
