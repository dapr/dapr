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

package options

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

type Options struct {
	Port        int
	HealthzPort int

	ListenAddress    string
	TLSEnabled       bool
	TrustDomain      string
	TrustAnchorsFile *string
	SentryAddress    string
	PlacementAddress string
	Mode             string

	ID                      string
	ReplicaID               uint32
	ReplicaCount            uint32
	EtcdInitialPeers        []string
	EtcdDataDir             string
	EtcdClientPorts         []string
	EtcdClientHTTPPorts     []string
	EtcdSpaceQuota          int64
	EtcdCompactionMode      string
	EtcdCompactionRetention string

	Logger  logger.Options
	Metrics *metrics.Options

	taFile string
}

func New(origArgs []string) (*Options, error) {
	// We are using pflag to parse the CLI flags
	// pflag is a drop-in replacement for the standard library's "flag" package, however…
	// There's one key difference: with the stdlib's "flag" package, there are no short-hand options so options can be defined with a single slash (such as "daprd -mode").
	// With pflag, single slashes are reserved for shorthands.
	// So, we are doing this thing where we iterate through all args and double-up the slash if it's single
	// This works *as long as* we don't start using shorthand flags (which haven't been in use so far).
	args := make([]string, len(origArgs))
	for i, a := range origArgs {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			args[i] = "-" + a
		} else {
			args[i] = a
		}
	}

	var opts Options

	// Create a flag set
	fs := pflag.NewFlagSet("scheduler", pflag.ExitOnError)
	fs.SortFlags = true

	fs.IntVar(&opts.Port, "port", 50006, "The port for the scheduler server to listen on")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	fs.StringVar(&opts.ListenAddress, "listen-address", "127.0.0.1", "The address for the Scheduler to listen on")
	fs.BoolVar(&opts.TLSEnabled, "tls-enabled", false, "Should TLS be enabled for the scheduler gRPC server")
	fs.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "Trust domain for the Dapr control plane")
	fs.StringVar(&opts.taFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")
	fs.StringVar(&opts.SentryAddress, "sentry-address", fmt.Sprintf("dapr-sentry.%s.svc:443", security.CurrentNamespace()), "Address of the Sentry service")
	fs.StringVar(&opts.PlacementAddress, "placement-address", "", "Addresses for Dapr Actor Placement service")
	fs.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr Scheduler")

	fs.StringVar(&opts.ID, "id", "dapr-scheduler-server-0", "Scheduler server ID")
	fs.Uint32Var(&opts.ReplicaCount, "replica-count", 1, "The total number of scheduler replicas in the cluster")
	fs.StringSliceVar(&opts.EtcdInitialPeers, "initial-cluster", []string{"dapr-scheduler-server-0=http://localhost:2380"}, "Initial etcd cluster peers")
	fs.StringVar(&opts.EtcdDataDir, "etcd-data-dir", "./data", "Directory to store scheduler etcd data")
	fs.StringSliceVar(&opts.EtcdClientPorts, "etcd-client-ports", []string{"dapr-scheduler-server-0=2379"}, "Ports for etcd client communication")
	fs.StringSliceVar(&opts.EtcdClientHTTPPorts, "etcd-client-http-ports", nil, "Ports for etcd client http communication")
	fs.Int64Var(&opts.EtcdSpaceQuota, "etcd-space-quota", 2*1024*1024*1024, "Space quota for etcd")
	fs.StringVar(&opts.EtcdCompactionMode, "etcd-compaction-mode", "periodic", "Compaction mode for etcd. Can be 'periodic' or 'revision'")
	fs.StringVar(&opts.EtcdCompactionRetention, "etcd-compaction-retention", "24h", "Compaction retention for etcd. Can express time  or number of revisions, depending on the value of 'etcd-compaction-mode'")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	_ = fs.Parse(args)

	replicaID, err := strconv.ParseUint(opts.ID, 10, 32)
	if err != nil {
		x := strings.LastIndex(opts.ID, "-")
		if x == -1 {
			return nil, fmt.Errorf("replica ID is not contained in '-id' flag: %s", err)
		}
		suffix := opts.ID[x+1:]
		replicaID, err = strconv.ParseUint(suffix, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse '--replica-id' flag: %s", err)
		}
	}

	opts.ReplicaID = uint32(replicaID)

	if fs.Changed("trust-anchors-file") {
		opts.TrustAnchorsFile = &opts.taFile
	}

	return &opts, nil
}
