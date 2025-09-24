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

package etcd

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/crypto/pem"
)

func config(opts Options) (*embed.Config, error) {
	config := embed.NewConfig()

	config.Name = opts.Name
	config.InitialCluster = strings.Join(opts.InitialCluster, ",")
	config.MaxRequestBytes = math.MaxInt32
	config.ExperimentalWarningApplyDuration = time.Second * 5

	if opts.Security.MTLSEnabled() {
		info := transport.TLSInfo{
			ClientCertAuth:      true,
			InsecureSkipVerify:  false,
			SkipClientSANVerify: false,
			AllowedHostnames: []string{
				fmt.Sprintf("dapr-scheduler-server-0.dapr-scheduler-server.%s.svc.cluster.local", opts.Security.ControlPlaneNamespace()),
				fmt.Sprintf("dapr-scheduler-server-1.dapr-scheduler-server.%s.svc.cluster.local", opts.Security.ControlPlaneNamespace()),
				fmt.Sprintf("dapr-scheduler-server-2.dapr-scheduler-server.%s.svc.cluster.local", opts.Security.ControlPlaneNamespace()),
			},
			EmptyCN:        true,
			CertFile:       filepath.Join(*opts.Security.IdentityDir(), "cert.pem"),
			KeyFile:        filepath.Join(*opts.Security.IdentityDir(), "key.pem"),
			ClientCertFile: filepath.Join(*opts.Security.IdentityDir(), "cert.pem"),
			ClientKeyFile:  filepath.Join(*opts.Security.IdentityDir(), "key.pem"),
			TrustedCAFile:  filepath.Join(*opts.Security.IdentityDir(), "ca.pem"),
			ServerName:     fmt.Sprintf("%s.dapr-scheduler-server.%s.svc.cluster.local", opts.Name, opts.Security.ControlPlaneNamespace()),
		}

		b, err := os.ReadFile(filepath.Join(*opts.Security.IdentityDir(), "cert.pem"))
		if err != nil {
			return nil, err
		}

		certs, err := pem.DecodePEMCertificatesChain(b)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(certs[0].DNSNames, info.ServerName) {
			return nil, fmt.Errorf("peer certificate does not contain the expected DNS name %s", info.ServerName)
		}

		config.ClientTLSInfo = info
		config.PeerTLSInfo = info
		config.PeerAutoTLS = true
	}

	urls, err := peerURLs(opts.InitialCluster)
	if err != nil {
		return nil, fmt.Errorf("invalid format for initial cluster. Make sure to include 'http://' or 'https://' in Scheduler URL: %s", err)
	}

	etcdURL, ok := urls[opts.Name]
	if !ok {
		return nil, fmt.Errorf("scheduler ID: %s is not found in initial cluster", opts.Name)
	}

	config.AdvertisePeerUrls = []url.URL{etcdURL}
	config.ListenClientUrls = []url.URL{{
		Scheme: "http",
		Host:   "127.0.0.1:" + strconv.FormatUint(opts.ClientPort, 10),
	}}

	switch opts.Mode {
	case modes.KubernetesMode:
		config.Dir = filepath.Join(opts.DataDir, "dapr-0.1")
		etcdURL.Host = "0.0.0.0:" + etcdURL.Port()
		config.AdvertiseClientUrls[0].Scheme = "https"
		config.ListenPeerUrls[0].Scheme = "https"
	default:
		config.Dir = filepath.Join(opts.DataDir, security.CurrentNamespace()+"-"+opts.Name, "dapr-0.1")

		if opts.Security.MTLSEnabled() {
			config.AdvertiseClientUrls[0].Scheme = "https"
			config.ListenPeerUrls[0].Scheme = "https"
		}

		// If not listening on an IP interface or localhost, replace host name with
		// 0.0.0.0 to listen on all interfaces.
		if net.ParseIP(etcdURL.Hostname()) == nil && etcdURL.Hostname() != "localhost" {
			etcdURL.Host = "0.0.0.0:" + etcdURL.Port()
		}
	}

	config.ListenPeerUrls = []url.URL{etcdURL}

	config.LogLevel = "info"

	backendBatchInterval, err := time.ParseDuration(opts.BackendBatchInterval)
	if err != nil {
		return nil, errors.New("failed to parse backend batch interval. Please use a string representing time.Duration")
	}

	config.QuotaBackendBytes = opts.SpaceQuota
	config.AutoCompactionMode = opts.CompactionMode
	config.AutoCompactionRetention = opts.CompactionRetention
	config.MaxSnapFiles = opts.MaxSnapshots
	config.MaxWalFiles = opts.MaxWALs
	config.SnapshotCount = opts.SnapshotCount
	config.BackendBatchLimit = opts.BackendBatchLimit
	config.BackendBatchInterval = backendBatchInterval
	config.ExperimentalBootstrapDefragThresholdMegabytes = opts.DefragThresholdMB

	// Must be set to false to prevent aggressive election ticks where leader changes can happen
	// before the state is fully synced. This is especially important with higher WAL/snapshot counts
	// as new leaders might not see previous job triggers, leading to duplicate job executions.
	config.InitialElectionTickAdvance = opts.InitialElectionTickAdvance

	config.Metrics = opts.Metrics

	return config, nil
}

func peerURLs(initialCluster []string) (map[string]url.URL, error) {
	urls := make(map[string]url.URL)

	for _, scheduler := range initialCluster {
		idAndAddress := strings.SplitN(scheduler, "=", 2)
		if len(idAndAddress) != 2 {
			return nil, fmt.Errorf("incorrect format for initialPeerList: %s. Should contain <id>=http://<ip>:<peer-port>", initialCluster)
		}

		address, err := url.Parse(idAndAddress[1])
		if err != nil {
			return nil, fmt.Errorf("unable to parse url from initialPeerList: %s. Should contain <id>=http://<ip>:<peer-port>", initialCluster)
		}

		urls[idAndAddress[0]] = *address
	}

	return urls, nil
}
