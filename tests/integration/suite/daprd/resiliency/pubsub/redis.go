package pubsub

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(redis))
}

type redis struct {
	daprd *daprd.Daprd
}

func (s *redis) Setup(t *testing.T) []framework.Option {
	s.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: dapr-resiliency
spec:
  policies:
    retries:
      retryAppCall:
        duration: 1s
        maxRetries: 4
        policy: constant
  targets:
    components:
      mypub:
        inbound:
          retry: retryAppCall
`,
			`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.redis
  version: v1
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: ""
  - name: processingTimeout
    value: "0"
  - name: redeliverInterval
    value: "0"
`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *redis) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	topic := "a-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	client := s.daprd.GRPCClient(t, ctx)

	stream, err := client.SubscribeTopicEventsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{
				PubsubName: "mypub", Topic: topic,
			},
		},
	}))

	_, err = stream.Recv()
	require.NoError(t, err)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: topic,
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	// initial + 4 from maxRetries
	for range 5 {
		resp, err := stream.Recv()
		require.NoError(t, err)
		event := resp.GetEventMessage()
		assert.Equal(t, topic, event.GetTopic())
		assert.Equal(t, "mypub", event.GetPubsubName())
		assert.JSONEq(t, `{"status": "completed"}`, string(event.GetData()))
		assert.Equal(t, "/", event.GetPath())

		require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
			SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
				EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
					Id:     event.GetId(),
					Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
				},
			},
		}))
	}

	gotRecv := make(chan *rtv1.SubscribeTopicEventsResponseAlpha1)
	go func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				return
			}
			gotRecv <- event
		}
	}()

	for {
		select {
		case event := <-gotRecv:
			fmt.Printf(">> Received event: %v\n", event)
			require.NoError(t, stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
				SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_EventProcessed{
					EventProcessed: &rtv1.SubscribeTopicEventsRequestProcessedAlpha1{
						Id:     event.GetEventMessage().GetId(),
						Status: &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY},
					},
				},
			}))
			assert.Fail(t, "expected no more messages")
		case <-time.After(time.Second * 10):
			require.NoError(t, stream.CloseSend())
			return
		}
	}
}
