/*
Copyright 2025 The Dapr Authors
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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

const (
	instance0Name = "dapr-scheduler-server-0"
)

var log = logger.NewLogger("dapr.scheduler.server.etcd")

type Options struct {
	Name                string
	InitialCluster      []string
	ClientPort          uint64
	SpaceQuota          int64
	CompactionMode      string
	CompactionRetention string
	SnapshotCount       uint64
	MaxSnapshots        uint
	MaxWALs             uint
	Security            security.Handler

	DataDir string
	Healthz healthz.Healthz
	Mode    modes.DaprMode
}

type Interface interface {
	Run(context.Context) error
	Client(context.Context) (*clientv3.Client, error)
	Instance0Ready(context.Context, string) (*string, error)
}

type etcd struct {
	mode   modes.DaprMode
	etcd   *embed.Etcd
	client *clientv3.Client
	config *embed.Config
	sec    security.Handler
	hz     healthz.Target

	existing0ClusterPath1 string
	existing0ClusterPath2 string
	instanceAddrs         map[string]string

	iaddedAddr map[string]*string
	iadded     map[string]chan struct{}
	i1waiting  chan struct{}
	i2waiting  chan struct{}
	readyCh    chan struct{}
}

func New(opts Options) (Interface, error) {
	config, err := config(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd config: %w", err)
	}

	instanceAddrs := make(map[string]string)
	split := strings.Split(config.InitialCluster, ",")
	for _, s := range split {
		nsplit := strings.Split(s, "=")
		if len(nsplit) != 2 {
			return nil, fmt.Errorf("invalid initial cluster member format: %s", config.InitialCluster)
		}

		instanceAddrs[nsplit[0]] = nsplit[1]
	}

	return &etcd{
		hz:      opts.Healthz.AddTarget(),
		config:  config,
		mode:    opts.Mode,
		sec:     opts.Security,
		readyCh: make(chan struct{}),

		instanceAddrs: instanceAddrs,
		iadded: map[string]chan struct{}{
			"dapr-scheduler-server-1": make(chan struct{}),
			"dapr-scheduler-server-2": make(chan struct{}),
		},
		iaddedAddr: make(map[string]*string),
		i1waiting:  make(chan struct{}),
		i2waiting:  make(chan struct{}),

		existing0ClusterPath1: filepath.Join(opts.DataDir, instance0Name+"-existing-cluster-1"),
		existing0ClusterPath2: filepath.Join(opts.DataDir, instance0Name+"-existing-cluster-2"),
	}, nil
}

func (e *etcd) Run(ctx context.Context) error {
	defer e.hz.NotReady()
	log.Info("Starting Etcd provider")

	should0Init, err := e.shouldInstance0Init()
	if err != nil {
		return fmt.Errorf("failed to check if instance 0 should be initialized: %w", err)
	}

	if should0Init {
		e.hz.Ready()
		log.Infof("Initializing single member etcd cluster for %s to manually join other members", instance0Name)
		e.config.InitialCluster = instance0Name + "=" + e.instanceAddrs[instance0Name]
		e.config.ClusterState = embed.ClusterStateFlagNew
		e.config.ForceNewCluster = true

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.i1waiting:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.i2waiting:
		}
	} else {
		log.Infof("Starting Etcd with existing cluster for %s", e.config.Name)
		close(e.iadded["dapr-scheduler-server-1"])
		close(e.iadded["dapr-scheduler-server-2"])
	}

	if e.mode == modes.KubernetesMode && e.config.Name != instance0Name {
		if err = e.waitForInstance0Ready(ctx); err != nil {
			return fmt.Errorf("failed to wait for %s to be ready: %w", instance0Name, err)
		}
	}

	e.etcd, err = embed.StartEtcd(e.config)
	if err != nil {
		return fmt.Errorf("failed to start embedded etcd: %w", err)
	}

	e.client, err = clientv3.New(clientv3.Config{
		Endpoints: []string{e.config.ListenClientUrls[0].Host},
		Logger:    e.etcd.GetLogger(),
	})
	if err != nil {
		return errors.Join(err, e.client.Close())
	}

	if e.mode == modes.KubernetesMode && e.config.Name != instance0Name {
		members, err := e.client.MemberList(ctx)
		if err != nil {
			return fmt.Errorf("failed to list members: %w", err)
		}

		for _, m := range members.Members {
			if m.Name == e.config.Name {
				log.Infof("Updating member %s with instance address %s", m.ID, e.instanceAddrs[e.config.Name])
				_, err := e.client.MemberUpdate(ctx, m.ID, []string{e.instanceAddrs[e.config.Name]})
				if err != nil {
					return fmt.Errorf("failed to update member %s with instance address %s: %w", m.ID, e.instanceAddrs[e.config.Name], err)
				}
			}
		}
	}

	e.hz.Ready()

	select {
	case <-e.etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		return ctx.Err()
	}

	if should0Init {
		log.Infof("Joining other members from instance %s for %s", instance0Name, e.config.InitialCluster)
		if err := e.instance0JoinMembers(ctx); err != nil {
			return fmt.Errorf("failed to join other members from instance %s: %w", instance0Name, err)
		}
	}

	close(e.readyCh)

	select {
	case err := <-e.etcd.Err():
		return err
	case <-ctx.Done():
		return nil
	}
}

func (e *etcd) shouldInstance0Init() (bool, error) {
	if e.mode != modes.KubernetesMode ||
		e.config.Name != instance0Name ||
		len(strings.Split(e.config.InitialCluster, "=")) == 2 {
		return false, nil
	}

	for _, path := range []string{e.existing0ClusterPath1, e.existing0ClusterPath2} {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			log.Infof("Running initialization, no existing cluster found for %s: %s", instance0Name, path)
			return true, nil
		}

		if err != nil {
			return false, err
		}

		log.Infof("Found existing cluster file for %s: %s", instance0Name, path)
	}

	log.Infof("No initialization needed, existing cluster found for %s", instance0Name)
	return false, nil
}

func (e *etcd) instance0JoinMembers(ctx context.Context) error {
	members, err := e.client.MemberList(ctx)
	if err != nil {
		return err
	}

	if len(e.instanceAddrs) != 3 {
		return fmt.Errorf("invalid initial cluster member count: %d", len(e.instanceAddrs))
	}

	if len(members.Members) == 1 {
		log.Infof("Updating initial cluster member address: %s", e.instanceAddrs["dapr-scheduler-server-0"])
		_, err = e.client.MemberUpdate(ctx, members.Header.MemberId, []string{e.instanceAddrs["dapr-scheduler-server-0"]})
		if err != nil {
			return err
		}
	}

	path := e.existing0ClusterPath1
	name := "dapr-scheduler-server-1"
	initialCluster := strings.Join([]string{
		"dapr-scheduler-server-0=" + e.instanceAddrs["dapr-scheduler-server-0"],
		"dapr-scheduler-server-1=" + e.instanceAddrs["dapr-scheduler-server-1"],
	}, ",")
	if err := e.joinMemebrHost(ctx, path, name, initialCluster); err != nil {
		return err
	}

	path = e.existing0ClusterPath2
	name = "dapr-scheduler-server-2"
	initialCluster = strings.Join([]string{
		"dapr-scheduler-server-0=" + e.instanceAddrs["dapr-scheduler-server-0"],
		"dapr-scheduler-server-1=" + e.instanceAddrs["dapr-scheduler-server-1"],
		"dapr-scheduler-server-2=" + e.instanceAddrs["dapr-scheduler-server-2"],
	}, ",")
	if err := e.joinMemebrHost(ctx, path, name, initialCluster); err != nil {
		return err
	}

	return nil
}

func (e *etcd) joinMemebrHost(ctx context.Context, path, name, initialCluster string) error {
	_, err := os.Stat(path)
	if err == nil {
		log.Infof("Instance %s already joined", name)
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	log.Infof("Adding member %s=%s to etcd cluster", name, e.instanceAddrs[name])

	err = backoff.Retry(func() error {
		if _, err = e.client.MemberAdd(ctx, []string{e.instanceAddrs[name]}); err != nil {
			log.Errorf("Failed to add member %s=%s to etcd cluster: %s", name, e.instanceAddrs[name], err)
			return err
		}
		log.Infof("Added member dapr-scheduler-server-2=%s to etcd cluster", e.instanceAddrs[name])
		return nil
	}, backoff.WithContext(backoff.NewConstantBackOff(time.Second), ctx))
	if err != nil {
		return err
	}

	log.Infof("Added member %s=%s to etcd cluster", name, e.instanceAddrs[name])

	e.iaddedAddr[name] = &initialCluster
	close(e.iadded[name])

	log.Infof("Successfully joined instance %s", name)

	return os.WriteFile(path, nil, 0o600)
}

func (e *etcd) Client(ctx context.Context) (*clientv3.Client, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.readyCh:
		return e.client, nil
	}
}

func (e *etcd) Close() error {
	defer log.Info("Etcd shut down")

	var err error
	if e.client != nil {
		err = e.client.Close()
	}

	if e.etcd != nil {
		e.etcd.Close()
	}

	return err
}

func (e *etcd) Instance0Ready(ctx context.Context, name string) (*string, error) {
	if e.mode != modes.KubernetesMode || e.config.Name != instance0Name {
		return nil, fmt.Errorf("InternalInstance0Ready called on %s rather than %s", e.config.Name, instance0Name)
	}

	switch name {
	case "dapr-scheduler-server-1":

		select {
		case <-e.i1waiting:
		default:
			close(e.i1waiting)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.iadded[name]:
			return e.iaddedAddr[name], nil
		case <-e.readyCh:
			return nil, nil
		}
	case "dapr-scheduler-server-2":

		select {
		case <-e.i2waiting:
		default:
			close(e.i2waiting)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.iadded[name]:
			return e.iaddedAddr[name], nil
		case <-e.readyCh:
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("invalid instance name: %s", name)
	}
}

func (e *etcd) waitForInstance0Ready(ctx context.Context) error {
	e.hz.Ready()

	log.Infof("Starting Etcd with existing cluster for %s", e.config.Name)
	e.config.ClusterState = embed.ClusterStateFlagExisting

	host := fmt.Sprintf("%s.dapr-scheduler-server.%s.svc:50006", instance0Name, e.sec.ControlPlaneNamespace())
	err := backoff.Retry(func() error {
		log.Infof("Waiting for %s to be ready", instance0Name)

		sclient, err := client.New(ctx, host, e.sec)
		if err != nil {
			err = fmt.Errorf("failed to create scheduler client to %s: %w", instance0Name, err)
			log.Error(err)
			return err
		}

		var resp *schedulerv1pb.Instance0ReadyResponse
		resp, err = sclient.Instance0Ready(ctx, &schedulerv1pb.Instance0ReadyRequest{
			InstanceName: e.config.Name,
		})
		if err != nil {
			err = fmt.Errorf("failed to wait for %s to be ready: %w", instance0Name, err)
			log.Error(err)
			return err
		}

		if resp.InitialCluster != nil {
			log.Infof("%s is ready with initial cluster: %s", instance0Name, resp.GetInitialCluster())
			e.config.InitialCluster = resp.GetInitialCluster()
			e.config.ForceNewCluster = true
		} else {
			log.Infof("%s is ready", instance0Name)
		}

		return nil
	}, backoff.WithContext(backoff.NewConstantBackOff(time.Second), ctx))
	if err != nil {
		return fmt.Errorf("failed to wait for %s to be ready: %w", instance0Name, err)
	}

	return nil
}
