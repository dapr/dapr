// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"net"
	"os"
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
)

// PeerInfo represents raft peer node information
type PeerInfo struct {
	ID      string
	Address string
}

// Server is Raft server implementation.
type Server struct {
	id  string
	fsm *FSM

	bootstrap bool
	inMem     bool
	raftBind  string
	peers     []PeerInfo

	raft          *raft.Raft
	raftStore     *raftboltdb.BoltStore
	raftTransport *raft.NetworkTransport
	raftInmem     *raft.InmemStore
}

// New creates Raft server node.
func New(id string, inMem, bootstrap bool, peers []PeerInfo) *Server {
	raftBind := raftAddressForID(id, peers)
	if raftBind == "" {
		return nil
	}

	return &Server{
		id:        id,
		inMem:     inMem,
		bootstrap: bootstrap,
		raftBind:  raftBind,
		peers:     peers,
	}
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

	// TODO: replace tls enabled transport layer using workload cert
	addr, err := net.ResolveTCPAddr("tcp", s.raftBind)
	if err != nil {
		return err
	}

	trans, err := raft.NewTCPTransport(s.raftBind, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return err
	}

	s.raftTransport = trans

	// Build an all in-memory setup for dev mode, otherwise prepare a full
	// disk-based setup.
	var logStore raft.LogStore
	var stable raft.StableStore
	var snap raft.SnapshotStore

	if s.inMem {
		s.raftInmem = raft.NewInmemStore()
		stable = s.raftInmem
		logStore = s.raftInmem
		snap = raft.NewInmemSnapshotStore()
	} else {
		if err = ensureDir(s.raftStorePath()); err != nil {
			return errors.Wrap(err, "failed to create log store directory")
		}

		// Create the backend raft store for logs and stable storage.
		s.raftStore, err = raftboltdb.NewBoltStore(filepath.Join(s.raftStorePath(), "raft.db"))
		if err != nil {
			return err
		}
		stable = s.raftStore

		// Wrap the store in a LogCache to improve performance.
		logStore, err = raft.NewLogCache(raftLogCacheSize, s.raftStore)
		if err != nil {
			return err
		}

		// Create the snapshot store.
		snap, err = raft.NewFileSnapshotStore(s.raftStorePath(), snapshotsRetained, os.Stderr)
		if err != nil {
			return err
		}
	}

	// Setup Raft configuration.
	if config == nil {
		// Set default configuration for raft
		config = &raft.Config{
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
	}

	// Use LoggerAdapter to integrate with Dapr logger. Log level relies on placement log level.
	config.Logger = newLoggerAdapter()
	config.LocalID = raft.ServerID(s.id)

	// If we are in bootstrap or dev mode and the state is clean then we can
	// bootstrap now.
	if s.inMem || s.bootstrap {
		var hasState bool
		hasState, err = raft.HasExistingState(logStore, stable, snap)
		if err != nil {
			return err
		}
		if !hasState {
			configuration := raft.Configuration{
				Servers: []raft.Server{
					{
						ID:      config.LocalID,
						Address: trans.LocalAddr(),
					},
				},
			}

			if err = raft.BootstrapCluster(config,
				logStore, stable, snap, trans, configuration); err != nil {
				return err
			}
		}
	}

	s.raft, err = raft.NewRaft(config, s.fsm, logStore, stable, snap, trans)
	if err != nil {
		return err
	}

	// Join all peers to Raft cluster.
	if s.bootstrap {
		for _, peer := range s.peers {
			if err = s.JoinCluster(peer.ID, peer.Address); err != nil {
				logging.Errorf("failed to join %s, %s: %v", peer.ID, peer.Address, err)
				continue
			}
		}
	}

	logging.Debug("Raft server is starting")

	return err
}

func (s *Server) raftStorePath() string {
	return logStorePrefix + s.id
}

// FSM returns fsm
func (s *Server) FSM() *FSM {
	return s.fsm
}

// JoinCluster joins new node to Raft cluster. If node is already the member of cluster,
// it will skip to add node.
func (s *Server) JoinCluster(nodeID, addr string) error {
	if !s.IsLeader() {
		return errors.New("only leader can join node to the cluster")
	}

	logging.Debugf("joining peer %s at %s to the cluster.", nodeID, addr)

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		logging.Errorf("failed to get raft configuration: %v", err)
		return err
	}

	for _, srv := range configFuture.Configuration().Servers {
		// If a node already exists with either the joining node's ID or address,
		// that node may need to be removed from the config first.
		if srv.ID == raft.ServerID(nodeID) || srv.Address == raft.ServerAddress(addr) {
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(nodeID) {
				logging.Debugf("node %s at %s is already the member of cluster.", nodeID, addr)
				return nil
			}

			future := s.raft.RemoveServer(srv.ID, 0, 0)
			if err := future.Error(); err != nil {
				return errors.Wrapf(err, "error removing existing node %s at %s", nodeID, addr)
			}
		}
	}

	f := s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if f.Error() != nil {
		return f.Error()
	}
	logging.Debugf("node %s at %s joined successfully", nodeID, addr)

	return nil
}

// IsLeader returns true if the current node is leader
func (s *Server) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

// ApplyCommand applies command log to state machine to upsert or remove members.
func (s *Server) ApplyCommand(cmdType CommandType, data DaprHostMember) (interface{}, error) {
	if !s.IsLeader() {
		return nil, errors.New("this is not the leader node")
	}

	cmdLog, err := makeRaftLogCommand(cmdType, data)
	if err != nil {
		return nil, err
	}

	future := s.raft.Apply(cmdLog, commandTimeout)

	if err := future.Error(); err != nil {
		return nil, err
	}

	resp := future.Response()
	return resp, nil
}
