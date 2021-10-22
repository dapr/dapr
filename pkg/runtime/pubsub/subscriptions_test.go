package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subscriptionsapi_v1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapi_v2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

func testDeclarativeSubscriptionV1() subscriptionsapi_v1alpha1.Subscription {
	return subscriptionsapi_v1alpha1.Subscription{
		TypeMeta: v1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: APIVersionV1alpha1,
		},
		Spec: subscriptionsapi_v1alpha1.SubscriptionSpec{
			Pubsubname: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Route: "myroute",
		},
	}
}

func testDeclarativeSubscriptionV2() subscriptionsapi_v2alpha1.Subscription {
	return subscriptionsapi_v2alpha1.Subscription{
		TypeMeta: v1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: APIVersionV2alpha1,
		},
		Spec: subscriptionsapi_v2alpha1.SubscriptionSpec{
			Pubsubname: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: subscriptionsapi_v2alpha1.Routes{
				Rules: []subscriptionsapi_v2alpha1.Rule{
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
}

func writeSubscriptionToDisk(subscription interface{}, filePath string) {
	b, _ := yaml.Marshal(subscription)
	os.WriteFile(filePath, b, 0600)
}

func TestDeclarativeSubscriptionsV1(t *testing.T) {
	dir := filepath.Join(".", "components")
	os.Mkdir(dir, 0777)
	defer os.RemoveAll(dir)

	t.Run("load single valid subscription", func(t *testing.T) {
		s := testDeclarativeSubscriptionV1()
		s.Scopes = []string{"scope1"}

		filePath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionToDisk(s, filePath)

		subs := DeclarativeSelfHosted(dir, log)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 1) {
				assert.Equal(t, "myroute", subs[0].Rules[0].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "scope1", subs[0].Scopes[0])
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("load multiple subscriptions", func(t *testing.T) {
		for i := 0; i < 1; i++ {
			s := testDeclarativeSubscriptionV1()
			s.Spec.Topic = fmt.Sprintf("%v", i)
			s.Spec.Route = fmt.Sprintf("%v", i)
			s.Spec.Pubsubname = fmt.Sprintf("%v", i)
			s.Spec.Metadata = map[string]string{
				"testName": fmt.Sprintf("%v", i),
			}
			s.Scopes = []string{fmt.Sprintf("%v", i)}

			writeSubscriptionToDisk(s, fmt.Sprintf("%s/%v", dir, i))
		}

		subs := DeclarativeSelfHosted(dir, log)
		if assert.Len(t, subs, 2) {
			for i := 0; i < 1; i++ {
				assert.Equal(t, fmt.Sprintf("%v", i), subs[i].Topic)
				if assert.Equal(t, 1, len(subs[i].Rules)) {
					assert.Equal(t, fmt.Sprintf("%v", i), subs[i].Rules[0].Path)
				}
				assert.Equal(t, fmt.Sprintf("%v", i), subs[i].PubsubName)
				assert.Equal(t, fmt.Sprintf("%v", i), subs[i].Scopes[0])
				assert.Equal(t, fmt.Sprintf("%v", i), subs[i].Metadata["testName"])
			}
		}
	})

	t.Run("no subscriptions loaded", func(t *testing.T) {
		os.RemoveAll(dir)

		s := testDeclarativeSubscriptionV1()
		s.Scopes = []string{"scope1"}

		writeSubscriptionToDisk(s, dir)

		subs := DeclarativeSelfHosted(dir, log)
		assert.Len(t, subs, 0)
	})
}

func TestDeclarativeSubscriptionsV2(t *testing.T) {
	dir := filepath.Join(".", "componentsV2")
	os.Mkdir(dir, 0777)
	defer os.RemoveAll(dir)

	t.Run("load single valid subscription", func(t *testing.T) {
		s := testDeclarativeSubscriptionV2()
		s.Scopes = []string{"scope1"}

		filePath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionToDisk(s, filePath)

		subs := DeclarativeSelfHosted(dir, log)
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 3) {
				assert.Equal(t, "myroute.v3", subs[0].Rules[0].Path)
				assert.Equal(t, "myroute.v2", subs[0].Rules[1].Path)
				assert.Equal(t, "myroute", subs[0].Rules[2].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, "scope1", subs[0].Scopes[0])
			assert.Equal(t, "testValue", subs[0].Metadata["testName"])
		}
	})

	t.Run("load multiple subscriptions", func(t *testing.T) {
		for i := 0; i < 1; i++ {
			iStr := fmt.Sprintf("%v", i)
			s := testDeclarativeSubscriptionV2()
			s.Spec.Topic = iStr
			for j := range s.Spec.Routes.Rules {
				s.Spec.Routes.Rules[j].Path = iStr
			}
			s.Spec.Routes.Default = iStr
			s.Spec.Pubsubname = iStr
			s.Spec.Metadata = map[string]string{
				"testName": iStr,
			}
			s.Scopes = []string{iStr}

			writeSubscriptionToDisk(s, fmt.Sprintf("%s/%v", dir, i))
		}

		subs := DeclarativeSelfHosted(dir, log)
		if assert.Len(t, subs, 2) {
			for i := 0; i < 1; i++ {
				iStr := fmt.Sprintf("%v", i)
				assert.Equal(t, iStr, subs[i].Topic)
				if assert.Equal(t, 3, len(subs[i].Rules)) {
					assert.Equal(t, iStr, subs[i].Rules[0].Path)
				}
				assert.Equal(t, iStr, subs[i].PubsubName)
				assert.Equal(t, iStr, subs[i].Scopes[0])
				assert.Equal(t, iStr, subs[i].Metadata["testName"])
			}
		}
	})

	t.Run("no subscriptions loaded", func(t *testing.T) {
		os.RemoveAll(dir)

		s := testDeclarativeSubscriptionV2()
		s.Scopes = []string{"scope1"}

		writeSubscriptionToDisk(s, dir)

		subs := DeclarativeSelfHosted(dir, log)
		assert.Len(t, subs, 0)
	})
}

type mockHTTPSubscriptions struct {
	channel.AppChannel
}

func (m *mockHTTPSubscriptions) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
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

	response := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	response.WithRawData(responseBytes, "content/json")
	return response, nil
}

func TestHTTPSubscriptions(t *testing.T) {
	m := mockHTTPSubscriptions{}
	subs, err := GetSubscriptionsHTTP(&m, log)
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
	m := mockGRPCSubscriptions{}
	subs, err := GetSubscriptionsGRPC(&m, log)
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
}

type mockK8sSubscriptions struct {
	operatorv1pb.OperatorClient
}

func (m *mockK8sSubscriptions) ListSubscriptions(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (*operatorv1pb.ListSubscriptionsResponse, error) {
	v2 := testDeclarativeSubscriptionV2()
	v2Bytes, err := yaml.Marshal(v2)
	if err != nil {
		return nil, err
	}
	return &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{v2Bytes},
	}, nil
}

func TestK8sSubscriptions(t *testing.T) {
	m := mockK8sSubscriptions{}
	subs := DeclarativeKubernetes(&m, log)
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
}
