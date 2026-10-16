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

package stream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(initialresponse))
}

type initialresponse struct {
	daprd *daprd.Daprd
}

func (i *initialresponse) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	i.daprd = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, i.daprd),
	}
}

func (i *initialresponse) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)
	dapr := i.daprd.GRPCClient(t, ctx)

	const subscribers, rounds = 8, 20

	cycle := func(topic string) error {
		sctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()

		stream, err := client.SubscribeStream(sctx, dapr, "mypub", topic)
		if err != nil {
			return err
		}
		if _, err = dapr.PublishEvent(sctx, &rtv1.PublishEventRequest{
			PubsubName: "mypub", Topic: topic, Data: []byte(`"x"`), DataContentType: "application/json",
		}); err != nil {
			return err
		}

		event, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("message published after the initial response on %s was not delivered: %w", topic, err)
		}
		if got := event.GetEventMessage().GetTopic(); got != topic {
			return fmt.Errorf("got a message for topic %s on the stream for %s", got, topic)
		}

		// Ack, then close gracefully: daprd reads the ack before the end of
		// the stream, so the subscription stops with nothing in flight.
		if err = stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
			SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
				EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
					Id:     event.GetEventMessage().GetId(),
					Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS},
				},
			},
		}); err != nil {
			return err
		}
		if err = stream.CloseSend(); err != nil {
			return err
		}
		for {
			if _, err = stream.Recv(); err != nil {
				return nil
			}
		}
	}

	var wg sync.WaitGroup
	for s := range subscribers {
		wg.Go(func() {
			for r := range rounds {
				if err := cycle(fmt.Sprintf("initial-%d-%d", s, r)); err != nil {
					assert.NoError(t, err)
					return
				}
			}
		})
	}
	wg.Wait()
}
