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

package pool

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/connection"
)

// streamConnection run starts the goroutines to handle sending jobs to the
// client and receiving job process results from the client.
func (p *Pool) streamConnection(ctx context.Context,
	req *schedulerv1pb.WatchJobsRequestInitial,
	stream schedulerv1pb.Scheduler_WatchJobsServer,
	conn *connection.Connection,
) {
	p.wg.Add(2)

	go func() {
		defer func() {
			log.Debugf("Closed send connection to %s/%s", req.GetNamespace(), req.GetAppId())
			p.wg.Done()
		}()

		for {
			job, err := conn.Next(ctx)
			if err != nil {
				return
			}

			if err := stream.Send(job); err != nil {
				log.Warnf("Error sending job to connection %s/%s: %s", req.GetNamespace(), req.GetAppId(), err)
				return
			}
		}
	}()

	go func() {
		defer func() {
			log.Debugf("Closed receive connection to %s/%s", req.GetNamespace(), req.GetAppId())
			p.wg.Done()
		}()

		for {
			resp, err := stream.Recv()
			if err == nil {
				conn.Ack(stream.Context(), resp.GetResult())
				continue
			}

			isEOF := errors.Is(err, io.EOF)
			s, ok := status.FromError(err)
			if stream.Context().Err() != nil || isEOF || (ok && s.Code() != codes.Canceled) {
				return
			}

			log.Warnf("Error receiving from connection %s/%s: %s", req.GetNamespace(), req.GetAppId(), err)
			return
		}
	}()
}
