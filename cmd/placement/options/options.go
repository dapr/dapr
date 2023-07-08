/*
Copyright 2021 The Dapr Authors
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

package options

import (
	"flag"
	"os"
	"strings"

	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/placement/raft"
)

const (
	//nolint:gosec
	defaultCredentialsPath   = "/var/run/dapr/credentials"
	defaultHealthzPort       = 8080
	defaultPlacementPort     = 50005
	defaultReplicationFactor = 100
	envMetadataEnabled       = "DAPR_PLACEMENT_METADATA_ENABLED"
)

type Options struct {
	// Raft protocol configurations
	RaftID           string
	RaftPeerString   string
	RaftPeers        []raft.PeerInfo
	RaftInMemEnabled bool
	RaftLogStorePath string

	// Placement server configurations
	PlacementPort   int
	HealthzPort     int
	CertChainPath   string
	TLSEnabled      bool
	MetadataEnabled bool

	ReplicationFactor int

	// Log and metrics configurations
	Logger  logger.Options
	Metrics *metrics.Options
}

func New() *Options {
	// Default options
	var opts Options

	flag.StringVar(&opts.RaftID, "id", "dapr-placement-0", "Placement server ID.")
	flag.StringVar(&opts.RaftPeerString, "initial-cluster", "dapr-placement-0=127.0.0.1:8201", "raft cluster peers")
	flag.BoolVar(&opts.RaftInMemEnabled, "inmem-store-enabled", true, "Enable in-memory log and snapshot store unless --raft-logstore-path is set")
	flag.StringVar(&opts.RaftLogStorePath, "raft-logstore-path", "", "raft log store path.")
	flag.IntVar(&opts.PlacementPort, "port", defaultPlacementPort, "sets the gRPC port for the placement service")
	flag.IntVar(&opts.HealthzPort, "healthz-port", defaultHealthzPort, "sets the HTTP port for the healthz server")
	flag.StringVar(&opts.CertChainPath, "certchain", defaultCredentialsPath, "Path to the credentials directory holding the cert chain")
	flag.BoolVar(&opts.TLSEnabled, "tls-enabled", false, "Should TLS be enabled for the placement gRPC server")
	flag.BoolVar(&opts.MetadataEnabled, "metadata-enabled", opts.MetadataEnabled, "Expose the placement tables on the healthz server")
	flag.IntVar(&opts.ReplicationFactor, "replicationFactor", defaultReplicationFactor, "sets the replication factor for actor distribution on vnodes")

	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-filename", credentials.RootCertFilename, "Certificate Authority certificate filename")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-filename", credentials.IssuerCertFilename, "Issuer certificate filename")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-filename", credentials.IssuerKeyFilename, "Issuer private key filename")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	// parse env variables before parsing flags, so the flags takes priority over env variables
	opts.MetadataEnabled = utils.IsTruthy(os.Getenv(envMetadataEnabled))

	flag.Parse()

	opts.RaftPeers = parsePeersFromFlag(opts.RaftPeerString)
	if opts.RaftLogStorePath != "" {
		opts.RaftInMemEnabled = false
	}

	return &opts
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
