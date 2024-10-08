package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.test")

func TestFilterSubscriptions(t *testing.T) {
	subs := []Subscription{
		{
			Topic: "topic0",
			Rules: []*Rule{
				{
					Path: "topic0",
				},
			},
		},
		{
			Topic: "topic1",
		},
		{
			Topic: "topic1",
			Rules: []*Rule{
				{
					Path: "custom/topic1",
				},
			},
		},
	}

	subs = filterSubscriptions(subs, log)
	if assert.Len(t, subs, 2) {
		assert.Equal(t, "topic0", subs[0].Topic)
		assert.Equal(t, "topic1", subs[1].Topic)
		if assert.Len(t, subs[1].Rules, 1) {
			assert.Equal(t, "custom/topic1", subs[1].Rules[0].Path)
		}
	}
}

type mockUnstableHTTPSubscriptions struct {
	channel.AppChannel
	callCount        int
	alwaysError      bool
	successThreshold int
}

func (m *mockUnstableHTTPSubscriptions) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	if m.alwaysError {
		return nil, errors.New("error")
	}

	m.callCount++

	if m.callCount < m.successThreshold {
		return nil, errors.New("connection refused")
	}

	subs := []SubscriptionJSON{
		{
			PubsubName: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: RoutesJSON{
				Rules: []*RuleJSON{
					{
						Match: `event.type == "myevent.v3"`,
						Path:  "myroute.v3",
					},
					{
						Match: `event.type == "myevent.v2"`,
						Path:  "myroute.v2",
					},
				},
				Default: "myroute",
			},
		},
	}

	responseBytes, _ := json.Marshal(subs)

	response := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataBytes(responseBytes).
		WithContentType("application/json")
	return response, nil
}

type mockHTTPSubscriptions struct {
	channel.AppChannel
}

func (m *mockHTTPSubscriptions) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	subs := []SubscriptionJSON{
		{
			PubsubName: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: RoutesJSON{
				Rules: []*RuleJSON{
					{
						Match: `event.type == "myevent.v3"`,
						Path:  "myroute.v3",
					},
					{
						Match: `event.type == "myevent.v2"`,
						Path:  "myroute.v2",
					},
				},
				Default: "myroute",
			},
		},
	}

	responseBytes, _ := json.Marshal(subs)

	response := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataBytes(responseBytes).
		WithContentType("application/json")
	return response, nil
}

func TestHTTPSubscriptions(t *testing.T) {
	t.Run("topics received, no errors", func(t *testing.T) {
		m := mockHTTPSubscriptions{}
		subs, err := GetSubscriptionsHTTP(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("error from app, success after retries", func(t *testing.T) {
		m := mockUnstableHTTPSubscriptions{
			successThreshold: 3,
		}

		subs, err := GetSubscriptionsHTTP(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		assert.Equal(t, m.successThreshold, m.callCount)
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("error from app, retries exhausted", func(t *testing.T) {
		m := mockUnstableHTTPSubscriptions{
			alwaysError: true,
		}

		_, err := GetSubscriptionsHTTP(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		require.Error(t, err)
	})

	t.Run("error from app, success after retries with resiliency", func(t *testing.T) {
		m := mockUnstableHTTPSubscriptions{
			successThreshold: 3,
		}

		subs, err := GetSubscriptionsHTTP(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		assert.Equal(t, m.successThreshold, m.callCount)
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("error from app, retries exhausted with resiliency", func(t *testing.T) {
		m := mockUnstableHTTPSubscriptions{
			alwaysError: true,
		}

		_, err := GetSubscriptionsHTTP(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		require.Error(t, err)
	})
}

type mockUnstableGRPCSubscriptions struct {
	runtimev1pb.AppCallbackClient
	callCount        int
	successThreshold int
	unimplemented    bool
}

func (m *mockUnstableGRPCSubscriptions) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	m.callCount++

	if m.unimplemented {
		return nil, status.Error(codes.Unimplemented, "Unimplemented method")
	}

	if m.callCount < m.successThreshold {
		return nil, errors.New("connection refused")
	}

	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "pubsub",
				Topic:      "topic1",
				Metadata: map[string]string{
					"testName": "testValue",
				},
				Routes: &runtimev1pb.TopicRoutes{
					Rules: []*runtimev1pb.TopicRule{
						{
							Match: `event.type == "myevent.v3"`,
							Path:  "myroute.v3",
						},
						{
							Match: `event.type == "myevent.v2"`,
							Path:  "myroute.v2",
						},
					},
					Default: "myroute",
				},
			},
		},
	}, nil
}

type mockGRPCSubscriptions struct {
	runtimev1pb.AppCallbackClient
}

func (m *mockGRPCSubscriptions) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "pubsub",
				Topic:      "topic1",
				Metadata: map[string]string{
					"testName": "testValue",
				},
				Routes: &runtimev1pb.TopicRoutes{
					Rules: []*runtimev1pb.TopicRule{
						{
							Match: `event.type == "myevent.v3"`,
							Path:  "myroute.v3",
						},
						{
							Match: `event.type == "myevent.v2"`,
							Path:  "myroute.v2",
						},
					},
					Default: "myroute",
				},
			},
		},
	}, nil
}

func TestGRPCSubscriptions(t *testing.T) {
	t.Run("topics received, no errors", func(t *testing.T) {
		m := mockGRPCSubscriptions{}
		subs, err := GetSubscriptionsGRPC(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("error from app, success after retries", func(t *testing.T) {
		m := mockUnstableGRPCSubscriptions{
			successThreshold: 3,
		}

		subs, err := GetSubscriptionsGRPC(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		assert.Equal(t, m.successThreshold, m.callCount)
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("server is running, app returns unimplemented error, no retries", func(t *testing.T) {
		m := mockUnstableGRPCSubscriptions{
			successThreshold: 3,
			unimplemented:    true,
		}

		subs, err := GetSubscriptionsGRPC(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		// not implemented error is not retried and is returned as "zero" subscriptions
		require.NoError(t, err)
		assert.Equal(t, 1, m.callCount)
		assert.Empty(t, subs)
	})

	t.Run("error from app, success after retries with resiliency", func(t *testing.T) {
		m := mockUnstableGRPCSubscriptions{
			successThreshold: 3,
		}

		subs, err := GetSubscriptionsGRPC(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		assert.Equal(t, m.successThreshold, m.callCount)
		require.NoError(t, err)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("server is running, app returns unimplemented error, no retries with resiliency", func(t *testing.T) {
		m := mockUnstableGRPCSubscriptions{
			successThreshold: 3,
			unimplemented:    true,
		}

		subs, err := GetSubscriptionsGRPC(context.TODO(), &m, log, resiliency.FromConfigurations(log))
		// not implemented error is not retried and is returned as "zero" subscriptions
		require.NoError(t, err)
		assert.Equal(t, 1, m.callCount)
		assert.Empty(t, subs)
	})
}

func TestGetRuleMatchString(t *testing.T) {
	cases := []subscriptionsapiV2alpha1.Rule{
		{
			Match: `event.type == "myevent.v3"`,
			Path:  "myroute.v3",
		},
		{
			Match: `event.type == "myevent.v2"`,
			Path:  "myroute.v2",
		},
		{
			Match: "",
			Path:  "myroute.v1",
		},
	}

	for _, v := range cases {
		rule, err := CreateRoutingRule(v.Match, v.Path)
		require.NoError(t, err)
		assert.Equal(t, v.Match, rule.Match.String())
	}
}
