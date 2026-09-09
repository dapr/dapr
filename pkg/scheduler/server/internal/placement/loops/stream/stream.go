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

// Package stream handles a single ReportActorTypes stream: sending placement
// orders and receiving host reports and acks.
package stream

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/peer"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/authorizer"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.placement.stream")

type Options struct {
	IDx           uint64
	Add           *loops.ConnAdd
	NamespaceLoop loop.Interface[loops.EventNamespace]
	Authorizer    *authorizer.Authorizer
}

type stream struct {
	idx uint64
	ns  string

	channel schedulerv1pb.Scheduler_ReportActorTypesServer
	cancel  context.CancelCauseFunc
	nsLoop  loop.Interface[loops.EventNamespace]
	authz   *authorizer.Authorizer

	loop loop.Interface[loops.EventStream]

	addr string
	wg   sync.WaitGroup
}

func New(ctx context.Context, opts Options) loop.Interface[loops.EventStream] {
	addr := "unknown"
	if p, ok := peer.FromContext(opts.Add.Channel.Context()); ok {
		addr = p.Addr.String()
	}

	s := &stream{
		channel: opts.Add.Channel,
		cancel:  opts.Add.Cancel,
		authz:   opts.Authorizer,
		nsLoop:  opts.NamespaceLoop,
		ns:      opts.Add.Initial.GetNamespace(),
		idx:     opts.IDx,
		addr:    addr,
	}

	s.loop = loop.New[loops.EventStream](64).NewLoop(s)

	s.wg.Go(func() {
		err := s.recvLoop()
		s.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: s.idx,
			Namespace: s.ns,
			Error:     err,
		})
	})

	return s.loop
}

func (s *stream) Handle(ctx context.Context, event loops.EventStream) error {
	var err error
	switch e := event.(type) {
	case *loops.SendLock:
		err = s.send(schedulerv1pb.Operation_OPERATION_LOCK, e.Seq, e.Types, nil, nil)
	case *loops.SendUpdate:
		err = s.send(schedulerv1pb.Operation_OPERATION_UPDATE, e.Seq, e.Types, e.Versions, e.Tables)
	case *loops.SendUnlock:
		err = s.send(schedulerv1pb.Operation_OPERATION_UNLOCK, e.Seq, e.Types, nil, nil)
	case *loops.SendSnapshot:
		err = s.sendSnapshot(e)
	case *loops.StreamShutdown:
		s.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	if err != nil {
		log.Errorf("Error handling stream event %T on %s: %v", event, s.addr, err)
		s.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: s.idx,
			Namespace: s.ns,
			Error:     err,
		})
	}

	return nil
}

func (s *stream) send(op schedulerv1pb.Operation, seq uint64, types []string, versions map[string]uint64, tables *schedulerv1pb.PlacementTables) error {
	log.Debugf("Sending %s for seq %d (types %v) to stream %s:%d", op, seq, types, s.ns, s.idx)

	return s.channel.Send(&schedulerv1pb.PlacementOrder{
		Operation:  op,
		Namespace:  s.ns,
		Seq:        seq,
		ActorTypes: types,
		Versions:   versions,
		Tables:     tables,
	})
}

// sendSnapshot sends a one-shot LOCK+UPDATE+UNLOCK covering all actor types
// (empty scope) to this stream only, outside of any cluster wide round.
func (s *stream) sendSnapshot(e *loops.SendSnapshot) error {
	log.Debugf("Sending table snapshot for seq %d to stream %s:%d", e.Seq, s.ns, s.idx)

	if err := s.send(schedulerv1pb.Operation_OPERATION_LOCK, e.Seq, nil, nil, nil); err != nil {
		return err
	}

	if err := s.send(schedulerv1pb.Operation_OPERATION_UPDATE, e.Seq, nil, e.Versions, e.Tables); err != nil {
		return err
	}

	return s.send(schedulerv1pb.Operation_OPERATION_UNLOCK, e.Seq, nil, nil, nil)
}

// handleShutdown closes the stream.
func (s *stream) handleShutdown(e *loops.StreamShutdown) {
	log.Infof("Closing placement connection to %s: %s", s.addr, e.Error)
	s.cancel(e.Error)
	s.wg.Wait()
}

func (s *stream) recvLoop() error {
	for {
		if err := s.recv(); err != nil {
			return err
		}
	}
}

func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	if err != nil {
		return err
	}

	switch msg := resp.GetMsg().(type) {
	case *schedulerv1pb.ReportActorTypesRequest_Report:
		if err = s.authz.Host(s.channel.Context(), msg.Report); err != nil {
			log.Warnf("Authorization failed for stream %s: %v", s.addr, err)
			return err
		}

		if msg.Report.GetNamespace() != s.ns {
			return fmt.Errorf("stream %s reported namespace %q, expected %q", s.addr, msg.Report.GetNamespace(), s.ns)
		}

		s.nsLoop.Enqueue(&loops.ReportedTypes{
			StreamIDx: s.idx,
			Host:      msg.Report,
		})

	case *schedulerv1pb.ReportActorTypesRequest_Ack:
		s.nsLoop.Enqueue(&loops.Ack{
			StreamIDx: s.idx,
			Namespace: s.ns,
			Operation: msg.Ack.GetOperation(),
			Seq:       msg.Ack.GetSeq(),
		})

	default:
		return fmt.Errorf("unknown message type from stream %s: %T", s.addr, resp.GetMsg())
	}

	return nil
}
