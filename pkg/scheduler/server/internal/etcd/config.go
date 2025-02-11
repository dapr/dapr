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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
)

func config(opts Options) (*embed.Config, error) {
	config := embed.NewConfig()

	config.Name = opts.Name
	config.InitialCluster = strings.Join(opts.InitialCluster, ",")
	config.MaxRequestBytes = math.MaxInt32
	config.ExperimentalWarningApplyDuration = time.Second * 5

	if opts.Security.MTLSEnabled() {
		hostName := "dapr-scheduler-server." + opts.Security.ControlPlaneNamespace() + ".svc"
		info := transport.TLSInfo{
			ClientCertAuth:      true,
			InsecureSkipVerify:  false,
			SkipClientSANVerify: false,
			AllowedHostname:     hostName,
			EmptyCN:             true,
			CertFile:            filepath.Join(*opts.Security.IdentityDir(), "cert.pem"),
			KeyFile:             filepath.Join(*opts.Security.IdentityDir(), "key.pem"),
			ClientCertFile:      filepath.Join(*opts.Security.IdentityDir(), "cert.pem"),
			ClientKeyFile:       filepath.Join(*opts.Security.IdentityDir(), "key.pem"),
			TrustedCAFile:       filepath.Join(*opts.Security.IdentityDir(), "ca.pem"),
			ServerName:          hostName,
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
		config.Dir = opts.DataDir
		etcdURL.Host = "0.0.0.0:" + etcdURL.Port()
	default:
		config.Dir = filepath.Join(opts.DataDir, security.CurrentNamespace()+"-"+opts.Name)

		// If not listening on an IP interface or localhost, replace host name with
		// 0.0.0.0 to listen on all interfaces.
		if net.ParseIP(etcdURL.Host) == nil && etcdURL.Host != "localhost" {
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
	config.BackendBatchLimit = int(opts.BackendBatchLimit)
	config.BackendBatchInterval = backendBatchInterval
	config.ElectionMs = opts.ElectionInterval
	config.TickMs = opts.HeartbeatInterval
	config.ExperimentalBootstrapDefragThresholdMegabytes = opts.DefragThresholdMB

	config.Metrics = opts.Metrics

	if len(urls) == 1 {
		config.ForceNewCluster = true
		config.ClusterState = embed.ClusterStateFlagNew
	}

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
