package diagnostics_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	resiliencyCountViewName  = "resiliency/count"
	resiliencyLoadedViewName = "resiliency/loaded"
	testAppID                = "fakeID"
	testResiliencyName       = "testResiliency"
	testResiliencyNamespace  = "testNamespace"
	testStateStoreName       = "testStateStore"
)

func TestResiliencyCountMonitoring(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		unitFn           func()
		wantTags         []tag.Tag
		wantNumberOfRows int
		wantErr          bool
		appID            string
	}{
		{
			name:  "EndpointPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.EndpointPolicy(ctx, "fakeApp", "fakeEndpoint")
			},
			wantNumberOfRows: 3,
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag("policy", "timeout"),
				newTag("policy", "retry"),
				newTag("policy", "circuitbreaker"),
			},
		},
		{
			name:  "ActorPreLockPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.ActorPreLockPolicy(ctx, "fakeActor", "fakeActorId")
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag("policy", "retry"),
				newTag("policy", "circuitbreaker"),
			},
			wantNumberOfRows: 2,
		},
		{
			name:  "ActorPostLockPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.ActorPostLockPolicy(ctx, "fakeActor", "fakeActorId")
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag("policy", "timeout"),
			},
			wantNumberOfRows: 1,
		},
		{
			name:  "ComponentOutboundPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, testStateStoreName)
				_ = r.ComponentOutboundPolicy(ctx, testStateStoreName, resiliency.Statestore)
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag("policy", "timeout"),
				newTag("policy", "retry"),
				newTag("policy", "timeout"),
			},
			wantNumberOfRows: 3,
		},
		{
			name:  "ComponentInboundPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, testStateStoreName)
				_ = r.ComponentInboundPolicy(ctx, testStateStoreName, resiliency.Statestore)
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag("policy", "timeout"),
				newTag("policy", "retry"),
				newTag("policy", "timeout"),
			},
			wantNumberOfRows: 3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Cleanup(func() {
				view.Unregister(view.Find(resiliencyCountViewName))
			})
			_ = diag.InitMetrics(test.appID, "fakeRuntimeNamespace", nil)
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyCountViewName)
			if test.wantErr {
				require.Error(t, err)
			}
			require.NoError(t, err)
			require.Equal(t, len(rows), test.wantNumberOfRows)
			for _, wantTag := range test.wantTags {
				requireTagExist(t, rows, wantTag)
			}
		})
	}
}

func createTestResiliency(resiliencyName string, resiliencyNamespace string, stateStoreName string) *resiliency.Resiliency {
	ctx := context.Background()

	r := resiliency.FromConfigurations(ctx, logger.NewLogger("fake-logger"), newTestResiliencyConfig(
		resiliencyName,
		resiliencyNamespace,
		"fakeApp",
		"fakeActor",
		stateStoreName,
	))
	return r
}

func TestResiliencyLoadedMonitoring(t *testing.T) {
	t.Run(resiliencyLoadedViewName, func(t *testing.T) {
		t.Cleanup(func() {
			view.Unregister(view.Find(resiliencyCountViewName))
		})
		_ = diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil)
		_ = createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStoreName")

		rows, err := view.RetrieveData(resiliencyLoadedViewName)

		require.NoError(t, err)
		require.Equal(t, 1, len(rows))

		requireTagExist(t, rows, newTag("app_id", testAppID))
		requireTagExist(t, rows, newTag("name", testResiliencyName))
		requireTagExist(t, rows, newTag("namespace", testResiliencyNamespace))
	})
}

func newTag(key string, value string) tag.Tag {
	return tag.Tag{
		Key:   tag.MustNewKey(key),
		Value: value,
	}
}

func requireTagExist(t *testing.T, rows []*view.Row, wantedTag tag.Tag) {
	t.Helper()
	var found bool
outerLoop:
	for _, row := range rows {
		for _, aTag := range row.Tags {
			if reflect.DeepEqual(wantedTag, aTag) {
				found = true
				break outerLoop
			}
		}
	}
	require.True(t, found, fmt.Sprintf("did not found tag (%s) in rows:", wantedTag), rows)
}

func newTestResiliencyConfig(resiliencyName, resiliencyNamespace, appName, actorType, storeName string) *resiliencyV1alpha.Resiliency {
	return &resiliencyV1alpha.Resiliency{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resiliencyName,
			Namespace: resiliencyNamespace,
		},
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"testTimeout": "5s",
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"testRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: ptr.Of(10),
					},
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"testCB": {
						Interval:    "8s",
						Timeout:     "45s",
						Trip:        "consecutiveFailures > 8",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					appName: {
						Timeout:                 "testTimeout",
						Retry:                   "testRetry",
						CircuitBreaker:          "testCB",
						CircuitBreakerCacheSize: 100,
					},
				},
				Actors: map[string]resiliencyV1alpha.ActorPolicyNames{
					actorType: {
						Timeout:                 "testTimeout",
						Retry:                   "testRetry",
						CircuitBreaker:          "testCB",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					storeName: {
						Outbound: resiliencyV1alpha.PolicyNames{
							Timeout:        "testTimeout",
							Retry:          "testRetry",
							CircuitBreaker: "testCB",
						},
						Inbound: resiliencyV1alpha.PolicyNames{
							Timeout:        "testTimeout",
							Retry:          "testRetry",
							CircuitBreaker: "testCB",
						},
					},
				},
			},
		},
	}
}
