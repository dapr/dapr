/*
Copyright 2026 The Dapr Authors
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

package leadership

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/raft"
	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/internal/leadership/fsm"
	"github.com/dapr/dapr/pkg/placement/internal/leadership/logr"
	"github.com/dapr/dapr/pkg/placement/internal/leadership/spiffenet"
	"github.com/dapr/dapr/pkg/placement/peers"
	"github.com/dapr/dapr/pkg/security"
)

type Options struct {
	ID       string
	Peers    []peers.PeerInfo
	Healthz  healthz.Healthz
	Security security.Handler
}

type Leadership struct {
	id string

	raftBind string
	peers    []peers.PeerInfo
	leaderCh chan struct{}

	sec     security.Handler
	htarget healthz.Target
}

func New(opts Options) (*Leadership, error) {
	raftBind, err := peers.AddressForID(opts.ID, opts.Peers)
	if err != nil {
		return nil, err
	}

	return &Leadership{
		id:       opts.ID,
		raftBind: raftBind,
		peers:    opts.Peers,
		sec:      opts.Security,
		leaderCh: make(chan struct{}),
		htarget:  opts.Healthz.AddTarget("placement-raft-leadership"),
	}, nil
}

func (s *Leadership) Run(ctx context.Context) error {
	defer s.htarget.NotReady()

	addr, err := s.tryResolveRaftAdvertiseAddr(ctx, s.raftBind)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", addr.String())
	if err != nil {
		return fmt.Errorf("failed to create raft listener: %w", err)
	}
	defer listener.Close()

	placeID, err := spiffeid.FromSegments(
		s.sec.ControlPlaneTrustDomain(),
		"ns",
		s.sec.ControlPlaneNamespace(),
		"dapr-placement",
	)
	if err != nil {
		return err
	}

	loggerAdapter := logr.New()
	raftTransport := raft.NewNetworkTransportWithLogger(spiffenet.New(ctx,
		spiffenet.Options{
			Listener: s.sec.NetListenerID(listener, placeID),
			PlaceID:  placeID,
			Security: s.sec,
		},
	), 3, 10*time.Second, loggerAdapter)
	defer raftTransport.Close()

	var config *raft.Config
	if len(s.peers) == 1 {
		config = &raft.Config{
			ProtocolVersion:    raft.ProtocolVersionMax,
			HeartbeatTimeout:   5 * time.Millisecond,
			ElectionTimeout:    5 * time.Millisecond,
			CommitTimeout:      5 * time.Millisecond,
			MaxAppendEntries:   64,
			ShutdownOnRemove:   true,
			TrailingLogs:       500,
			SnapshotInterval:   60 * time.Second,
			SnapshotThreshold:  1000,
			LeaderLeaseTimeout: 5 * time.Millisecond,
		}
	} else {
		config = &raft.Config{
			ProtocolVersion:    raft.ProtocolVersionMax,
			HeartbeatTimeout:   2 * time.Second,
			ElectionTimeout:    2 * time.Second,
			CommitTimeout:      100 * time.Millisecond,
			MaxAppendEntries:   64,
			ShutdownOnRemove:   true,
			TrailingLogs:       1000,
			SnapshotInterval:   30 * time.Second,
			SnapshotThreshold:  2000,
			LeaderLeaseTimeout: 2 * time.Second,
		}
	}

	// Use LoggerAdapter to integrate with Dapr logger. Log level relies on placement log level.
	config.Logger = loggerAdapter
	config.LocalID = raft.ServerID(s.id)

	raftInmem := raft.NewInmemStore()
	stableStore := raftInmem
	logStore := raftInmem
	snapStore := raft.NewInmemSnapshotStore()

	bootstrapConf, err := peers.BootstrapConfig(s.peers)
	if err != nil {
		return err
	}

	if err = raft.BootstrapCluster(
		config, logStore, stableStore,
		snapStore, raftTransport, *bootstrapConf,
	); err != nil {
		return err
	}

	ra, err := raft.NewRaft(config, fsm.New(), logStore, stableStore, snapStore, raftTransport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}
	defer ra.Shutdown()

	s.htarget.Ready()

	ch := ra.LeaderCh()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
			if ra.State() == raft.Leader {
				select {
				case <-s.leaderCh:
				default:
					close(s.leaderCh)
				}
			} else {
				select {
				case <-s.leaderCh:
					return errors.New("lost leadership")
				default:
				}
			}
		}
	}
}

func (s *Leadership) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.leaderCh:
		return nil
	}
}
