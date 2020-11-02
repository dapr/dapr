// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"strings"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/placement/raft"
)

const (
	defaultCredentialsPath   = "/var/run/dapr/credentials"
	defaultHealthzPort       = 8080
	defaultPlacementPort     = 50005
	defaultReplicationFactor = 1000
)

type config struct {
	// Raft protocol configurations
	raftID           string
	raftPeerString   string
	raftPeers        []raft.PeerInfo
	raftInMemEnabled bool
	raftBootStrap    bool

	// Placement server configurations
	placementPort int
	healthzPort   int
	certChainPath string
	tlsEnabled    bool

	replicationFactor int

	// Log and metrics configurations
	loggerOptions   logger.Options
	metricsExporter metrics.Exporter
}

func newConfig() *config {
	// Default configuration
	cfg := config{
		raftID:           "node0",
		raftPeerString:   "node0=127.0.0.1:15050",
		raftPeers:        []raft.PeerInfo{},
		raftInMemEnabled: true,
		raftBootStrap:    false,

		placementPort: defaultPlacementPort,
		healthzPort:   defaultHealthzPort,
		certChainPath: defaultCredentialsPath,
		tlsEnabled:    false,
	}

	flag.StringVar(&cfg.raftID, "id", cfg.raftID, "Placement server ID.")
	flag.StringVar(&cfg.raftPeerString, "initial-cluster", cfg.raftPeerString, "raft cluster peers")
	flag.BoolVar(&cfg.raftInMemEnabled, "inmem-store-enabled", cfg.raftInMemEnabled, "Enable in-memory log and snapshot store")
	flag.IntVar(&cfg.placementPort, "port", cfg.placementPort, "sets the gRPC port for the placement service")
	flag.IntVar(&cfg.healthzPort, "healthz-port", cfg.healthzPort, "sets the HTTP port for the healthz server")
	flag.StringVar(&cfg.certChainPath, "certchain", cfg.certChainPath, "Path to the credentials directory holding the cert chain")
	flag.BoolVar(&cfg.tlsEnabled, "tls-enabled", cfg.tlsEnabled, "Should TLS be enabled for the placement gRPC server")
	flag.IntVar(&cfg.replicationFactor, "replicationFactor", defaultReplicationFactor, "sets the replication factor for actor distribution on vnodes")

	cfg.loggerOptions = logger.DefaultOptions()
	cfg.loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	cfg.metricsExporter = metrics.NewExporter(metrics.DefaultMetricNamespace)
	cfg.metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	cfg.raftPeers = parsePeersFromFlag(cfg.raftPeerString)
	cfg.raftBootStrap = len(cfg.raftPeers) > 1

	return &cfg
}

func parsePeersFromFlag(val string) []raft.PeerInfo {
	peers := []raft.PeerInfo{}

	p := strings.Split(val, ",")
	for _, addr := range p {
		peer := strings.Split(addr, "=")
		if len(peer) != 2 {
			continue
		}

		peers = append(peers, raft.PeerInfo{
			ID:      strings.TrimSpace(peer[0]),
			Address: strings.TrimSpace(peer[1]),
		})
	}

	return peers
}
