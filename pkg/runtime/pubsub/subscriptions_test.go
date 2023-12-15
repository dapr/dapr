package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
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

func testDeclarativeSubscriptionV1() subscriptionsapiV1alpha1.Subscription {
	return subscriptionsapiV1alpha1.Subscription{
		TypeMeta: v1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: APIVersionV1alpha1,
		},
		Spec: subscriptionsapiV1alpha1.SubscriptionSpec{
			Pubsubname: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Route: "myroute",
		},
	}
}

func testDeclarativeSubscriptionV2() subscriptionsapiV2alpha1.Subscription {
	return subscriptionsapiV2alpha1.Subscription{
		TypeMeta: v1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: APIVersionV2alpha1,
		},
		Spec: subscriptionsapiV2alpha1.SubscriptionSpec{
			Pubsubname: "pubsub",
			Topic:      "topic1",
			Metadata: map[string]string{
				"testName": "testValue",
			},
			Routes: subscriptionsapiV2alpha1.Routes{
				Rules: []subscriptionsapiV2alpha1.Rule{
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

func writeSubscriptionToDisk(subscription any, filePath string) {
	b, _ := yaml.Marshal(subscription)
	os.WriteFile(filePath, b, 0o600)
}

func writeSubscriptionsToDisk(subscriptions []any, filePath string) {
	byteArray := make([][]byte, len(subscriptions))
	for i, sub := range subscriptions {
		if sub != nil {
			byteArray[i], _ = yaml.Marshal(sub)
		} else {
			byteArray[i] = []byte{}
		}
	}

	b := bytes.Join(byteArray, []byte("\n---\n"))
	os.WriteFile(filePath, b, 0o600)
}

func TestDeclarativeSubscriptionsV1(t *testing.T) {
	dir := filepath.Join(".", "components")
	os.Mkdir(dir, 0o777)
	defer os.RemoveAll(dir)
	subscriptionCount := 5

	t.Run("load single valid subscription", func(t *testing.T) {
		s := testDeclarativeSubscriptionV1()
		s.Scopes = []string{"scope1"}

		filePath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionToDisk(s, filePath)
		defer os.RemoveAll(filePath)

		subs := DeclarativeLocal([]string{dir}, "", log)
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

	t.Run("load multiple subscriptions in different files", func(t *testing.T) {
		for i := 0; i < subscriptionCount; i++ {
			s := testDeclarativeSubscriptionV1()
			s.Spec.Topic = strconv.Itoa(i)
			s.Spec.Route = strconv.Itoa(i)
			s.Spec.Pubsubname = strconv.Itoa(i)
			s.Spec.Metadata = map[string]string{
				"testName": strconv.Itoa(i),
			}
			s.Scopes = []string{strconv.Itoa(i)}

			filepath := fmt.Sprintf("%s/%v.yaml", dir, i)
			writeSubscriptionToDisk(s, filepath)
			defer os.RemoveAll(filepath)
		}

		subs := DeclarativeLocal([]string{dir}, "", log)
		if assert.Len(t, subs, subscriptionCount) {
			for i := 0; i < subscriptionCount; i++ {
				assert.Equal(t, strconv.Itoa(i), subs[i].Topic)
				if assert.Len(t, subs[i].Rules, 1) {
					assert.Equal(t, strconv.Itoa(i), subs[i].Rules[0].Path)
				}
				assert.Equal(t, strconv.Itoa(i), subs[i].PubsubName)
				assert.Equal(t, strconv.Itoa(i), subs[i].Scopes[0])
				assert.Equal(t, strconv.Itoa(i), subs[i].Metadata["testName"])
			}
		}
	})

	t.Run("load multiple subscriptions in single file", func(t *testing.T) {
		subscriptions := []any{}
		// Add an empty block at the beginning which will be ignored
		subscriptions = append(subscriptions, nil)

		for i := 0; i < subscriptionCount; i++ {
			s := testDeclarativeSubscriptionV1()
			s.Spec.Topic = strconv.Itoa(i)
			s.Spec.Route = strconv.Itoa(i)
			s.Spec.Pubsubname = strconv.Itoa(i)
			s.Spec.Metadata = map[string]string{
				"testName": strconv.Itoa(i),
			}
			s.Scopes = []string{strconv.Itoa(i)}

			subscriptions = append(subscriptions, s)
		}

		filepath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionsToDisk(subscriptions, filepath)
		defer os.RemoveAll(filepath)

		subs := DeclarativeLocal([]string{dir}, "", log)
		if assert.Len(t, subs, subscriptionCount) {
			for i := 0; i < subscriptionCount; i++ {
				assert.Equal(t, strconv.Itoa(i), subs[i].Topic)
				if assert.Len(t, subs[i].Rules, 1) {
					assert.Equal(t, strconv.Itoa(i), subs[i].Rules[0].Path)
				}
				assert.Equal(t, strconv.Itoa(i), subs[i].PubsubName)
				assert.Equal(t, strconv.Itoa(i), subs[i].Scopes[0])
				assert.Equal(t, strconv.Itoa(i), subs[i].Metadata["testName"])
			}
		}
	})

	t.Run("filter subscriptions by namespace v1", func(t *testing.T) {
		// Subscription v1 in namespace dev
		s := testDeclarativeSubscriptionV1()
		s.ObjectMeta.Namespace = "dev"
		s.Spec.Topic = "dev"
		path := filepath.Join(dir, "dev.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Subscription v1 in namespace prod
		s = testDeclarativeSubscriptionV1()
		s.ObjectMeta.Namespace = "prod"
		s.Spec.Topic = "prod"
		path = filepath.Join(dir, "prod.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Subscription v1 doesn't have a namespace
		s = testDeclarativeSubscriptionV1()
		s.ObjectMeta.Namespace = ""
		s.Spec.Topic = "all"
		path = filepath.Join(dir, "all.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Test function
		loadAndReturnTopics := func(namespace string, expect []string) func(t *testing.T) {
			return func(t *testing.T) {
				res := []string{}
				subs := DeclarativeLocal([]string{dir}, namespace, log)
				for _, sub := range subs {
					res = append(res, sub.Topic)
				}
				slices.Sort(res)

				require.Equal(t, expect, res)
			}
		}

		t.Run("load all subscriptions without a namespace specified", loadAndReturnTopics("", []string{"all", "dev", "prod"}))
		t.Run("load subscriptions for dev namespace only", loadAndReturnTopics("dev", []string{"all", "dev"}))
		t.Run("load subscriptions for prod namespace only", loadAndReturnTopics("prod", []string{"all", "prod"}))
	})

	t.Run("filter subscriptions by namespace v2", func(t *testing.T) {
		// Subscription v2 in namespace dev
		s := testDeclarativeSubscriptionV2()
		s.ObjectMeta.Namespace = "dev"
		s.Spec.Topic = "dev"
		path := filepath.Join(dir, "dev.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Subscription v2 in namespace prod
		s = testDeclarativeSubscriptionV2()
		s.ObjectMeta.Namespace = "prod"
		s.Spec.Topic = "prod"
		path = filepath.Join(dir, "prod.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Subscription v2 doesn't have a namespace
		s = testDeclarativeSubscriptionV2()
		s.ObjectMeta.Namespace = ""
		s.Spec.Topic = "all"
		path = filepath.Join(dir, "all.yaml")
		writeSubscriptionToDisk(s, path)
		defer os.RemoveAll(path)

		// Test function
		loadAndReturnTopics := func(namespace string, expect []string) func(t *testing.T) {
			return func(t *testing.T) {
				res := []string{}
				subs := DeclarativeLocal([]string{dir}, namespace, log)
				for _, sub := range subs {
					res = append(res, sub.Topic)
				}
				slices.Sort(res)

				require.Equal(t, expect, res)
			}
		}

		t.Run("load all subscriptions without a namespace specified", loadAndReturnTopics("", []string{"all", "dev", "prod"}))
		t.Run("load subscriptions for dev namespace only", loadAndReturnTopics("dev", []string{"all", "dev"}))
		t.Run("load subscriptions for prod namespace only", loadAndReturnTopics("prod", []string{"all", "prod"}))
	})

	t.Run("will not load non yaml file", func(t *testing.T) {
		s := testDeclarativeSubscriptionV1()
		s.Scopes = []string{"scope1"}

		filePath := filepath.Join(dir, "sub.txt")
		writeSubscriptionToDisk(s, filePath)
		defer os.RemoveAll(filePath)

		subs := DeclarativeLocal([]string{dir}, "", log)
		assert.Empty(t, subs)
	})

	t.Run("no subscriptions loaded", func(t *testing.T) {
		os.RemoveAll(dir)

		s := testDeclarativeSubscriptionV1()
		s.Scopes = []string{"scope1"}

		writeSubscriptionToDisk(s, dir)

		subs := DeclarativeLocal([]string{dir}, "", log)
		assert.Empty(t, subs)
	})
}

func TestDeclarativeSubscriptionsV2(t *testing.T) {
	dir := filepath.Join(".", "componentsV2")
	os.Mkdir(dir, 0o777)
	defer os.RemoveAll(dir)
	subscriptionCount := 5

	t.Run("load single valid subscription", func(t *testing.T) {
		s := testDeclarativeSubscriptionV2()
		s.Scopes = []string{"scope1"}

		filePath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionToDisk(s, filePath)
		defer os.RemoveAll(filePath)

		subs := DeclarativeLocal([]string{dir}, "", log)
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

	t.Run("load multiple subscriptions in different files", func(t *testing.T) {
		for i := 0; i < subscriptionCount; i++ {
			iStr := strconv.Itoa(i)
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

			filePath := fmt.Sprintf("%s/%v.yaml", dir, i)
			writeSubscriptionToDisk(s, filePath)
			defer os.RemoveAll(filePath)
		}

		subs := DeclarativeLocal([]string{dir}, "", log)
		if assert.Len(t, subs, subscriptionCount) {
			for i := 0; i < subscriptionCount; i++ {
				iStr := strconv.Itoa(i)
				assert.Equal(t, iStr, subs[i].Topic)
				if assert.Len(t, subs[i].Rules, 3) {
					assert.Equal(t, iStr, subs[i].Rules[0].Path)
				}
				assert.Equal(t, iStr, subs[i].PubsubName)
				assert.Equal(t, iStr, subs[i].Scopes[0])
				assert.Equal(t, iStr, subs[i].Metadata["testName"])
			}
		}
	})

	t.Run("load multiple subscriptions in single file", func(t *testing.T) {
		subscriptions := []any{}
		for i := 0; i < subscriptionCount; i++ {
			iStr := strconv.Itoa(i)
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

			subscriptions = append(subscriptions, s)
		}

		filepath := filepath.Join(dir, "sub.yaml")
		writeSubscriptionsToDisk(subscriptions, filepath)
		defer os.RemoveAll(filepath)

		subs := DeclarativeLocal([]string{dir}, "", log)
		if assert.Len(t, subs, subscriptionCount) {
			for i := 0; i < subscriptionCount; i++ {
				iStr := strconv.Itoa(i)
				assert.Equal(t, iStr, subs[i].Topic)
				if assert.Len(t, subs[i].Rules, 3) {
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

		subs := DeclarativeLocal([]string{dir}, "", log)
		assert.Empty(t, subs)
	})
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

type mockK8sSubscriptions struct {
	operatorv1pb.OperatorClient
}

func (m *mockK8sSubscriptions) ListSubscriptionsV2(ctx context.Context, in *operatorv1pb.ListSubscriptionsRequest, opts ...grpc.CallOption) (*operatorv1pb.ListSubscriptionsResponse, error) {
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
	subs := DeclarativeKubernetes(context.TODO(), &m, "testPodName", "testNamespace", log)
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
		rule, err := createRoutingRule(v.Match, v.Path)
		require.NoError(t, err)
		assert.Equal(t, v.Match, rule.Match.String())
	}
}
