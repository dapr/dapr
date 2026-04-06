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

package grpc

/*
func init() {
	suite.Register(new(appapitokenbulk))
}

type appapitokenbulk struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (a *appapitokenbulk) Setup(t *testing.T) []framework.Option {
	a.ch = make(chan metadata.MD, 1)

	appProc := app.New(t,
		app.WithOnBulkTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventBulkRequest) (*rtv1.TopicEventBulkResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			a.ch <- md

			stats := make([]*rtv1.TopicEventBulkResponseEntry, len(in.GetEntries()))
			for i, e := range in.GetEntries() {
				stats[i] = &rtv1.TopicEventBulkResponseEntry{
					EntryId: e.GetEntryId(),
					Status:  rtv1.TopicEventResponse_SUCCESS,
				}
			}
			return &rtv1.TopicEventBulkResponse{Statuses: stats}, nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "test-bulk-topic",
						Routes: &rtv1.TopicRoutes{
							Default: "/test-bulk-topic",
						},
						BulkSubscribe: &rtv1.BulkSubscribeConfig{
							Enabled:            true,
							MaxMessagesCount:   10,
							MaxAwaitDurationMs: 1000,
						},
					},
				},
			}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "test-bulk-app-token"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(appProc, a.daprd),
	}
}

func (a *appapitokenbulk) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)

	// Give the subscription a moment to register
	time.Sleep(time.Millisecond * 100)

	_, err := client.BulkPublishEvent(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "test-bulk-topic",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{
				EntryId:     "1",
				Event:       []byte(`{"message": "hello1"}`),
				ContentType: "application/json",
			},
			{
				EntryId:     "2",
				Event:       []byte(`{"message": "hello2"}`),
				ContentType: "application/json",
			},
			{
				EntryId:     "3",
				Event:       []byte(`{"message": "hello3"}`),
				ContentType: "application/json",
			},
		},
	})
	require.NoError(t, err)

	// Check that the app received the bulk event with the APP_API_TOKEN in metadata
	// Note: Bulk subscribe waits for MaxAwaitDurationMs (1000ms) to batch messages
	select {
	case md := <-a.ch:
		tokens := md.Get("dapr-api-token")
		assert.NotEmpty(t, tokens)
		assert.Equal(t, "test-bulk-app-token", tokens[0])
	case <-time.After(time.Second * 2):
		require.Fail(t, "Timed out waiting for bulk event to be delivered to app")
	}
}
*/
