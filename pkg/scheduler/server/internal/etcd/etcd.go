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
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.etcd")

type Options struct {
	Name string

	Embed bool

	InitialCluster             []string
	ClientPort                 uint64
	SpaceQuota                 int64
	CompactionMode             string
	CompactionRetention        string
	SnapshotCount              uint64
	MaxSnapshots               uint
	MaxWALs                    uint
	BackendBatchLimit          int
	BackendBatchInterval       string
	DefragThresholdMB          uint
	InitialElectionTickAdvance bool
	Metrics                    string

	ClientEndpoints []string
	ClientUsername  string
	ClientPassword  string

	Security security.Handler

	DataDir string
	Healthz healthz.Healthz
	Mode    modes.DaprMode
}

type Interface interface {
	Run(context.Context) error
	Client(context.Context) (*clientv3.Client, error)
}

type etcd struct {
	mode   modes.DaprMode
	etcd   *embed.Etcd
	client *clientv3.Client
	config *embed.Config
	hz     healthz.Target

	existingClusterPath string
	readyCh             chan struct{}
}

func New(opts Options) (Interface, error) {
	if opts.Embed {
		config, err := config(opts)
		if err != nil {
			return nil, fmt.Errorf("failed to create etcd config: %w", err)
		}

		return &etcd{
			hz:      opts.Healthz.AddTarget("scheduler-etcd-embed"),
			config:  config,
			mode:    opts.Mode,
			readyCh: make(chan struct{}),

			existingClusterPath: filepath.Join(opts.DataDir, "dapr-scheduler-existing-cluster"),
		}, nil
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints: opts.ClientEndpoints,
		Username:  opts.ClientUsername,
		Password:  opts.ClientPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return &etcd{
		hz:      opts.Healthz.AddTarget("scheduler-etcd-external"),
		client:  client,
		mode:    opts.Mode,
		readyCh: make(chan struct{}),
	}, nil
}

func (e *etcd) Run(ctx context.Context) error {
	defer e.hz.NotReady()
	log.Info("Starting Etcd provider")

	if e.config == nil {
		log.Info("Using external Etcd database, will not start embedded Etcd")
		e.hz.Ready()
		close(e.readyCh)
		<-ctx.Done()
		return ctx.Err()
	}

	log.Infof("Starting embedded Etcd")

	if err := e.maybeDeleteDataDir(); err != nil {
		return err
	}

	var err error
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

	e.hz.Ready()

	select {
	case <-e.etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		return ctx.Err()
	}

	close(e.readyCh)

	return concurrency.NewRunnerManager(
		e.runDefragLoop,
		func(ctx context.Context) error {
			select {
			case err := <-e.etcd.Err():
				return err
			case <-ctx.Done():
				return nil
			}
		},
	).Run(ctx)
}

func (e *etcd) Client(ctx context.Context) (*clientv3.Client, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.readyCh:
		return e.client, nil
	}
}

func (e *etcd) runDefragLoop(ctx context.Context) error {
	checkInterval := 10 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkInterval):
			if err := e.doDefrag(ctx); err != nil {
				log.Errorf("Failed to defrag Etcd, retrying in 5s: %s", err)
				checkInterval = 5 * time.Second
				break
			}

			checkInterval = 10 * time.Minute
		}
	}
}

func (e *etcd) doDefrag(ctx context.Context) error {
	log.Debug("Checking if Etcd needs Defragmentation")
	resp, err := e.client.Maintenance.Status(ctx, e.config.ListenClientUrls[0].Host)
	if err != nil {
		return err
	}

	dbSize := fmt.Sprintf("%.2fM", float64(resp.DbSize)/(1024*1024))
	dbSizeInUse := fmt.Sprintf("%.2fM", float64(resp.DbSizeInUse)/(1024*1024))

	if resp.DbSize < resp.DbSizeInUse*2 {
		log.Debugf("Defragmenting not needed (dbSize: %s, dbSizeInUse: %s)", dbSize, dbSizeInUse)
		return nil
	}

	log.Infof("Defragmenting Etcd (dbSize: %s, dbSizeInUse: %s)", dbSize, dbSizeInUse)
	start := time.Now()
	_, err = e.client.Maintenance.Defragment(ctx, e.config.ListenClientUrls[0].Host)
	if err != nil {
		return err
	}

	log.Infof("Defragmentation completed in %s", time.Since(start))

	return nil
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

func (e *etcd) maybeDeleteDataDir() error {
	_, err := os.Stat(e.existingClusterPath)
	if err == nil {
		log.Infof("Found existing cluster data, preserving data dir: %s", e.config.Dir)
		return nil
	}

	if !os.IsNotExist(err) {
		return err
	}

	log.Infof("No existing cluster data found, deleting data dir contents: %s", e.config.Dir)
	if err = e.removeContents(); err != nil {
		return fmt.Errorf("failed to remove data dir contents: %w", err)
	}

	log.Infof("Data dir contents removed: %s", e.config.Dir)

	if err := os.MkdirAll(e.config.Dir, 0o700); err != nil {
		return fmt.Errorf("failed to create data dir: %w", err)
	}
	return os.WriteFile(e.existingClusterPath, nil, 0o600)
}

func (e *etcd) removeContents() error {
	d, err := os.Open(e.config.Dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()

	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, name := range names {
		if err = os.RemoveAll(filepath.Join(e.config.Dir, name)); err != nil {
			return err
		}
	}

	return nil
}
