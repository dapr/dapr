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

// Package v1 implements the placement stream transport speaking the v1
// protocol of the standalone placement service (ReportDaprStatus).
package v1

import (
	"fmt"
	"strconv"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	operationLock   = "lock"
	operationUpdate = "update"
	operationUnlock = "unlock"
)

// apiLevel is the actors API level this client reports. Vestigial: the
// placement server no longer negotiates API levels.
const apiLevel = 20

type Options struct {
	Channel   v1pb.Placement_ReportDaprStatusClient
	AppID     string
	Namespace string
}

type v1 struct {
	channel   v1pb.Placement_ReportDaprStatusClient
	appID     string
	namespace string
}

func New(opts Options) transport.Transport {
	return &v1{
		channel:   opts.Channel,
		appID:     opts.AppID,
		namespace: opts.Namespace,
	}
}

func (v *v1) Recv() (*loops.Order, error) {
	resp, err := v.channel.Recv()
	if err != nil {
		return nil, err
	}

	var version uint64
	if ver := resp.GetVersion(); ver > 0 {
		version = ver
	} else {
		//nolint:staticcheck
		version, _ = strconv.ParseUint(resp.GetTables().GetVersion(), 10, 64)
	}

	order := &loops.Order{Version: version}
	switch resp.GetOperation() {
	case operationLock:
		order.Op = loops.OrderLock
	case operationUpdate:
		order.Op = loops.OrderUpdate
		order.V1Tables = resp.GetTables()
	case operationUnlock:
		order.Op = loops.OrderUnlock
	default:
		return nil, fmt.Errorf("unknown operation: %s", resp.GetOperation())
	}

	return order, nil
}

func (v *v1) SendReport(report *loops.Report) error {
	return v.channel.Send(&v1pb.Host{
		Name:      report.Address,
		Id:        report.AppID,
		Namespace: report.Namespace,
		Entities:  report.ActorTypes,
		ApiLevel:  apiLevel,
		Operation: v1pb.HostOperation_REPORT,
	})
}

func (v *v1) SendAck(ack *loops.Ack) error {
	var op v1pb.HostOperation
	switch ack.Op {
	case loops.OrderLock:
		op = v1pb.HostOperation_LOCK
	case loops.OrderUpdate:
		op = v1pb.HostOperation_UPDATE
	case loops.OrderUnlock:
		op = v1pb.HostOperation_UNLOCK
	default:
		return fmt.Errorf("unknown ack operation: %s", ack.Op)
	}

	version := ack.Version
	return v.channel.Send(&v1pb.Host{
		Operation: op,
		Version:   &version,
		Namespace: v.namespace,
		Id:        v.appID,
	})
}

func (v *v1) CloseSend() error {
	return v.channel.CloseSend()
}
