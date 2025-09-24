/*
Copyright 2024 The Dapr Authors
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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler")

type Options struct {
	Port                 int
	HealthzPort          int
	HealthzListenAddress string

	ListenAddress             string
	OverrideBroadcastHostPort *string

	TLSEnabled       bool
	TrustDomain      string
	TrustAnchorsFile *string
	SentryAddress    string
	PlacementAddress string
	Mode             string
	KubeConfig       *string

	ID string

	EtcdEmbed                      bool
	EtcdInitialCluster             []string
	EtcdDataDir                    string
	EtcdClientPort                 uint64
	EtcdSpaceQuota                 int64
	EtcdCompactionMode             string
	EtcdCompactionRetention        string
	EtcdSnapshotCount              uint64
	EtcdMaxSnapshots               uint
	EtcdMaxWALs                    uint
	EtcdBackendBatchLimit          int
	EtcdBackendBatchInterval       string
	EtcdDefragThresholdMB          uint
	EtcdInitialElectionTickAdvance bool
	EtcdMetrics                    string

	// TODO: @joshvanl: add Etcd client TLS enabled flag.
	EtcdClientEndpoints []string
	EtcdClientUsername  string
	EtcdClientPassword  string

	IdentityDirectoryWrite string

	Logger  logger.Options
	Metrics *metrics.FlagOptions

	taFile                    string
	kubeconfig                string
	etcdSpaceQuota            string
	overrideBroadcastHostPort string
}

func New(origArgs []string) (*Options, error) {
	var opts Options

	// Create a flag set
	fs := pflag.NewFlagSet("scheduler", pflag.ExitOnError)
	fs.SortFlags = true

	fs.IntVar(&opts.Port, "port", 50006, "The port for the scheduler server to listen on")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")
	fs.StringVar(&opts.HealthzListenAddress, "healthz-listen-address", "", "The listening address for the healthz server")

	fs.StringVar(&opts.ListenAddress, "listen-address", "", "The address for the Scheduler to listen on")
	fs.StringVar(&opts.overrideBroadcastHostPort, "override-broadcast-host-port", "", "Override the address (host:port) which is broadcast by this scheduler host that daprd instances will use to connect to this scheduler. This option should only be set by the CLI when in standalone mode, or in exotic environments whereby the routable scheduler address (host:port) is different from its own understood routable address, i.e. in a layered or natted network.")
	fs.BoolVar(&opts.TLSEnabled, "tls-enabled", false, "Should TLS be enabled for the scheduler gRPC server")
	fs.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "Trust domain for the Dapr control plane")
	fs.StringVar(&opts.taFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")
	fs.StringVar(&opts.SentryAddress, "sentry-address", fmt.Sprintf("dapr-sentry.%s.svc:443", security.CurrentNamespace()), "Address of the Sentry service")
	fs.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr Scheduler")
	fs.StringVar(&opts.kubeconfig, "kubeconfig", "", "Kubernetes mode only. Absolute path to the kubeconfig file.")
	if err := fs.MarkHidden("kubeconfig"); err != nil {
		log.Fatal(err)
	}

	fs.StringVar(&opts.ID, "id", "dapr-scheduler-server-0", "Scheduler server ID")

	fs.BoolVar(&opts.EtcdEmbed, "etcd-embed", true, "When enabled, the Etcd database will be embedded in the scheduler server. If false, the scheduler will connect to an external Etcd cluster using the --etcd-client-endpoints flag.")

	fs.StringSliceVar(&opts.EtcdInitialCluster, "etcd-initial-cluster", []string{"dapr-scheduler-server-0=http://localhost:2380"}, "Initial etcd cluster peers")
	fs.StringVar(&opts.EtcdDataDir, "etcd-data-dir", "./data", "Directory to store scheduler etcd data")
	fs.Uint64Var(&opts.EtcdClientPort, "etcd-client-port", 2379, "Port for etcd client communication")
	fs.StringVar(&opts.etcdSpaceQuota, "etcd-space-quota", "9.2E", "Space quota for etcd")
	fs.StringVar(&opts.EtcdCompactionMode, "etcd-compaction-mode", "periodic", "Compaction mode for etcd. Can be 'periodic' or 'revision'")
	fs.StringVar(&opts.EtcdCompactionRetention, "etcd-compaction-retention", "10m", "Compaction retention for etcd. Can express time  or number of revisions, depending on the value of 'etcd-compaction-mode'")
	fs.Uint64Var(&opts.EtcdSnapshotCount, "etcd-snapshot-count", 10000, "Number of committed transactions to trigger a snapshot to disk.")
	fs.UintVar(&opts.EtcdMaxSnapshots, "etcd-max-snapshots", 10, "Maximum number of snapshot files to retain (0 is unlimited).")
	fs.UintVar(&opts.EtcdMaxWALs, "etcd-max-wals", 10, "Maximum number of write-ahead logs to retain (0 is unlimited).")
	fs.IntVar(&opts.EtcdBackendBatchLimit, "etcd-backend-batch-limit", 5000, "Maximum operations before committing the backend transaction.")
	fs.StringVar(&opts.EtcdBackendBatchInterval, "etcd-backend-batch-interval", "50ms", "Maximum time before committing the backend transaction.")
	fs.UintVar(&opts.EtcdDefragThresholdMB, "etcd-experimental-bootstrap-defrag-threshold-megabytes", 100, "Minimum number of megabytes needed to be freed for etcd to consider running defrag during bootstrap. Needs to be set to non-zero value to take effect.")
	fs.BoolVar(&opts.EtcdInitialElectionTickAdvance, "etcd-initial-election-tick-advance", false, "Whether to fast-forward initial election ticks on boot for faster election. When it is true, then local member fast-forwards election ticks to speed up “initial” leader election trigger. This benefits the case of larger election ticks. Disabling this would slow down initial bootstrap process for cross datacenter deployments. Make your own tradeoffs by configuring this flag at the cost of slow initial bootstrap.")
	fs.StringVar(&opts.EtcdMetrics, "etcd-metrics", "basic", "Level of detail for exported metrics, specify ’extensive’ to include histogram metrics.")

	fs.StringArrayVar(&opts.EtcdClientEndpoints, "etcd-client-endpoints", []string{}, "Comma-separated list of etcd client endpoints to connect to. Only used when --etcd-embed is false.")
	fs.StringVar(&opts.EtcdClientUsername, "etcd-client-username", "", "Username for etcd client authentication. Only used when --etcd-embed is false.")
	fs.StringVar(&opts.EtcdClientPassword, "etcd-client-password", "", "Password for etcd client authentication. Only used when --etcd-embed is false.")

	fs.StringVar(&opts.IdentityDirectoryWrite, "identity-directory-write", filepath.Join(os.TempDir(), "secrets/dapr.io/tls"), "Directory to write identity certificate certificate, private key and trust anchors")

	if err := fs.MarkHidden("identity-directory-write"); err != nil {
		log.Fatal(err)
	}

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultFlagOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	_ = fs.Parse(origArgs)

	if fs.Changed("trust-anchors-file") {
		opts.TrustAnchorsFile = &opts.taFile
	}

	etcdSpaceQuota, err := resource.ParseQuantity(opts.etcdSpaceQuota)
	if err != nil {
		return nil, fmt.Errorf("failed to parse etcd space quota: %s", err)
	}
	opts.EtcdSpaceQuota, _ = etcdSpaceQuota.AsInt64()

	if fs.Changed("kubeconfig") {
		if opts.Mode != string(modes.KubernetesMode) {
			return nil, errors.New("kubeconfig flag is only valid in --mode=kubernetes")
		}
		opts.KubeConfig = &opts.kubeconfig
	}

	if fs.Changed("override-broadcast-host-port") {
		opts.OverrideBroadcastHostPort = &opts.overrideBroadcastHostPort
	}

	if opts.EtcdEmbed {
		if len(opts.EtcdClientEndpoints) > 0 {
			return nil, errors.New("cannot use --etcd-client-endpoints with --etcd-embed")
		}

		if len(opts.EtcdClientUsername) > 0 {
			return nil, errors.New("cannot use --etcd-client-username with --etcd-embed")
		}

		if len(opts.EtcdClientPassword) > 0 {
			return nil, errors.New("cannot use --etcd-client-password with --etcd-embed")
		}
	}

	if !opts.EtcdEmbed && len(opts.EtcdClientEndpoints) == 0 {
		return nil, errors.New("must specify --etcd-client-endpoints when not using embedded etcd")
	}

	return &opts, nil
}
