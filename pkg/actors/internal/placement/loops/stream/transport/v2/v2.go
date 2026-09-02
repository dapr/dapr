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

// Package v2 implements the placement stream transport speaking the v2
// protocol of the scheduler placement service (ReportActorTypes): per actor
// type placement orders with seq-keyed rounds and partial tables.
package v2

import (
	"fmt"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type Options struct {
	Channel schedulerv1pb.Scheduler_ReportActorTypesClient
}

type v2 struct {
	channel schedulerv1pb.Scheduler_ReportActorTypesClient
}

func New(opts Options) transport.Transport {
	return &v2{
		channel: opts.Channel,
	}
}

func (v *v2) Recv() (*loops.Order, error) {
	resp, err := v.channel.Recv()
	if err != nil {
		return nil, err
	}

	order := &loops.Order{
		Version:  resp.GetSeq(),
		Scope:    resp.GetActorTypes(),
		Versions: resp.GetVersions(),
		V2Tables: resp.GetTables(),
		Partial:  true,
	}

	switch resp.GetOperation() {
	case schedulerv1pb.Operation_OPERATION_LOCK:
		order.Op = loops.OrderLock
	case schedulerv1pb.Operation_OPERATION_UPDATE:
		order.Op = loops.OrderUpdate
	case schedulerv1pb.Operation_OPERATION_UNLOCK:
		order.Op = loops.OrderUnlock
	default:
		return nil, fmt.Errorf("unknown operation: %s", resp.GetOperation())
	}

	return order, nil
}

func (v *v2) SendReport(report *loops.Report) error {
	return v.channel.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{
			Report: &schedulerv1pb.ActorHost{
				Address:    report.Address,
				AppId:      report.AppID,
				Namespace:  report.Namespace,
				ActorTypes: report.ActorTypes,
			},
		},
	})
}

func (v *v2) SendAck(ack *loops.Ack) error {
	var op schedulerv1pb.Operation
	switch ack.Op {
	case loops.OrderLock:
		op = schedulerv1pb.Operation_OPERATION_LOCK
	case loops.OrderUpdate:
		op = schedulerv1pb.Operation_OPERATION_UPDATE
	case loops.OrderUnlock:
		op = schedulerv1pb.Operation_OPERATION_UNLOCK
	default:
		return fmt.Errorf("unknown ack operation: %s", ack.Op)
	}

	return v.channel.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{
			Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: op,
				Seq:       ack.Version,
			},
		},
	})
}

func (v *v2) CloseSend() error {
	return v.channel.CloseSend()
}
