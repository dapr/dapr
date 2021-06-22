// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"net"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"github.com/pkg/errors"
)

const (
	logStorePrefix    = "log-"
	snapshotsRetained = 2

	// raftLogCacheSize is the maximum number of logs to cache in-memory.
	// This is used to reduce disk I/O for the recently committed entries.
	raftLogCacheSize = 512

	commandTimeout = 1 * time.Second

	nameResolveRetryInterval = 2 * time.Second
	nameResolveMaxRetry      = 120
)

// PeerInfo represents raft peer node information.
type PeerInfo struct {
	ID      string
	Address string
}

// Server is Raft server implementation.
type Server struct {
	id  string
	fsm *FSM

	inMem    bool
	raftBind string
	peers    []PeerInfo

	config        *raft.Config
	raft          *raft.Raft
	raftStore     *raftboltdb.BoltStore
	raftTransport *raft.NetworkTransport

	logStore    raft.LogStore
	stableStore raft.StableStore
	snapStore   raft.SnapshotStore

	raftLogStorePath string
}

// New creates Raft server node.
func New(id string, inMem bool, peers []PeerInfo, logStorePath string) *Server {
	raftBind := raftAddressForID(id, peers)
	if raftBind == "" {
		return nil
	}

	return &Server{
		id:               id,
		inMem:            inMem,
		raftBind:         raftBind,
		peers:            peers,
		raftLogStorePath: logStorePath,
	}
}

func tryResolveRaftAdvertiseAddr(bindAddr string) (*net.TCPAddr, error) {
	// HACKHACK: Kubernetes POD DNS A record population takes some time
	// to look up the address after StatefulSet POD is deployed.
	var err error
	var addr *net.TCPAddr
	for retry := 0; retry < nameResolveMaxRetry; retry++ {
		addr, err = net.ResolveTCPAddr("tcp", bindAddr)
		if err == nil {
			return addr, nil
		}
		time.Sleep(nameResolveRetryInterval)
	}
	return nil, err
}

// StartRaft starts Raft node with Raft protocol configuration. if config is nil,
// the default config will be used.
func (s *Server) StartRaft(config *raft.Config) error {
	// If we have an unclean exit then attempt to close the Raft store.
	defer func() {
		if s.raft == nil && s.raftStore != nil {
			if err := s.raftStore.Close(); err != nil {
				logging.Errorf("failed to close log storage: %v", err)
			}
		}
	}()

	s.fsm = newFSM()

	addr, err := tryResolveRaftAdvertiseAddr(s.raftBind)
	if err != nil {
		return err
	}

	loggerAdapter := newLoggerAdapter()
	trans, err := raft.NewTCPTransportWithLogger(s.raftBind, addr, 3, 10*time.Second, loggerAdapter)
	if err != nil {
		return err
	}

	s.raftTransport = trans

	// Build an all in-memory setup for dev mode, otherwise prepare a full
	// disk-based setup.
	if s.inMem {
		raftInmem := raft.NewInmemStore()
		s.stableStore = raftInmem
		s.logStore = raftInmem
		s.snapStore = raft.NewInmemSnapshotStore()
	} else {
		if err = ensureDir(s.raftStorePath()); err != nil {
			return errors.Wrap(err, "failed to create log store directory")
		}

		// Create the backend raft store for logs and stable storage.
		s.raftStore, err = raftboltdb.NewBoltStore(filepath.Join(s.raftStorePath(), "raft.db"))
		if err != nil {
			return err
		}
		s.stableStore = s.raftStore

		// Wrap the store in a LogCache to improve performance.
		s.logStore, err = raft.NewLogCache(raftLogCacheSize, s.raftStore)
		if err != nil {
			return err
		}

		// Create the snapshot store.
		s.snapStore, err = raft.NewFileSnapshotStoreWithLogger(s.raftStorePath(), snapshotsRetained, loggerAdapter)
		if err != nil {
			return err
		}
	}

	// Setup Raft configuration.
	if config == nil {
		// Set default configuration for raft
		s.config = &raft.Config{
			ProtocolVersion:    raft.ProtocolVersionMax,
			HeartbeatTimeout:   1000 * time.Millisecond,
			ElectionTimeout:    1000 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			MaxAppendEntries:   64,
			ShutdownOnRemove:   true,
			TrailingLogs:       10240,
			SnapshotInterval:   120 * time.Second,
			SnapshotThreshold:  8192,
			LeaderLeaseTimeout: 500 * time.Millisecond,
		}
	} else {
		s.config = config
	}

	// Use LoggerAdapter to integrate with Dapr logger. Log level relies on placement log level.
	s.config.Logger = loggerAdapter
	s.config.LocalID = raft.ServerID(s.id)

	// If we are in bootstrap or dev mode and the state is clean then we can
	// bootstrap now.
	bootstrapConf, err := s.bootstrapConfig(s.peers)
	if err != nil {
		return err
	}

	if bootstrapConf != nil {
		if err = raft.BootstrapCluster(
			s.config, s.logStore, s.stableStore,
			s.snapStore, trans, *bootstrapConf); err != nil {
			return err
		}
	}

	s.raft, err = raft.NewRaft(s.config, s.fsm, s.logStore, s.stableStore, s.snapStore, s.raftTransport)
	if err != nil {
		return err
	}

	logging.Infof("Raft server is starting on %s...", s.raftBind)

	return err
}

func (s *Server) bootstrapConfig(peers []PeerInfo) (*raft.Configuration, error) {
	hasState, err := raft.HasExistingState(s.logStore, s.stableStore, s.snapStore)
	if err != nil {
		return nil, err
	}

	if !hasState {
		raftConfig := &raft.Configuration{
			Servers: make([]raft.Server, len(peers)),
		}

		for i, p := range peers {
			raftConfig.Servers[i] = raft.Server{
				ID:      raft.ServerID(p.ID),
				Address: raft.ServerAddress(p.Address),
			}
		}

		return raftConfig, nil
	}

	// return nil for raft.Configuration to use the existing log store files.
	return nil, nil
}

func (s *Server) raftStorePath() string {
	if s.raftLogStorePath == "" {
		return logStorePrefix + s.id
	}
	return s.raftLogStorePath
}

// FSM returns fsm.
func (s *Server) FSM() *FSM {
	return s.fsm
}

// Raft returns raft node.
func (s *Server) Raft() *raft.Raft {
	return s.raft
}

// IsLeader returns true if the current node is leader.
func (s *Server) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

// ApplyCommand applies command log to state machine to upsert or remove members.
func (s *Server) ApplyCommand(cmdType CommandType, data DaprHostMember) (bool, error) {
	if !s.IsLeader() {
		return false, errors.New("this is not the leader node")
	}

	cmdLog, err := makeRaftLogCommand(cmdType, data)
	if err != nil {
		return false, err
	}

	future := s.raft.Apply(cmdLog, commandTimeout)
	if err := future.Error(); err != nil {
		return false, err
	}

	resp := future.Response()
	return resp.(bool), nil
}

// Shutdown shutdown raft server gracefully.
func (s *Server) Shutdown() {
	if s.raft != nil {
		s.raftTransport.Close()
		future := s.raft.Shutdown()
		if err := future.Error(); err != nil {
			logging.Warnf("error shutting down raft: %v", err)
		}
		if s.raftStore != nil {
			s.raftStore.Close()
		}
	}
}
