package pubsub

/*
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	resapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(redisk8s))
}

type redisk8s struct {
	daprd    *daprd.Daprd
	sentry   *sentry.Sentry
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (s *redisk8s) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	maxRetries := 4
	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			s.sentry.Port(),
		),
		kubernetes.WithClusterDaprResiliencyList(t, &resapi.ResiliencyList{Items: []resapi.Resiliency{{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Resiliency"},
			ObjectMeta: metav1.ObjectMeta{Name: "dapr-resiliency", Namespace: "default"},
			Spec: resapi.ResiliencySpec{
				Policies: resapi.Policies{
					Retries: map[string]resapi.Retry{
						"retryAppCall": {
							Policy:     "constant",
							Duration:   "1s",
							MaxRetries: &maxRetries,
						},
					},
				},
				Targets: resapi.Targets{
					Components: map[string]resapi.ComponentPolicyNames{
						"mypub": {
							Inbound: resapi.PolicyNames{
								Retry: "retryAppCall",
							},
						},
					},
				},
			},
		}}}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{Items: []compapi.Component{{
			ObjectMeta: metav1.ObjectMeta{Name: "mypub", Namespace: "default"},
			Spec: compapi.ComponentSpec{
				Type:    "pubsub.redis",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{Name: "redisHost", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`"localhost:6379"`)}},},
					{Name: "redisPassword", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`""`)}},},
					{Name: "processingTimeout", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`"0"`)}},},
					{Name: "redeliverInterval", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`"0"`)}},},
				},
			},
		}}}),
	)
	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(s.sentry.TrustAnchorsFile(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(s.sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(s.sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.kubeapi, s.operator, s.daprd),
	}
}

func (s *redisk8s) Run(t *testing.T, ctx context.Context) {
	s.operator.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() {
		s.kubeapi.Cleanup(t)
	})

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
*/
