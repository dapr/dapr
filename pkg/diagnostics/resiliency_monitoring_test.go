package diagnostics_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

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
	tests := []struct {
		name             string
		unitFn           func()
		wantTags         []tag.Tag
		wantNumberOfRows int
		wantErr          bool
		appID            string
		enableDefault    bool
	}{
		{
			name:  "EndpointPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.EndpointPolicy("fakeApp", "fakeEndpoint")
			},
			wantNumberOfRows: 3,
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyAppTarget("fakeApp")),
				newTag(diag.StatusKey.Name(), "closed"),
			},
		},
		{
			name:  "ActorPreLockPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.ActorPreLockPolicy("fakeActor", "fakeActorId")
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyActorTarget("fakeActor")),
				newTag(diag.StatusKey.Name(), "closed"),
			},
			wantNumberOfRows: 2,
		},
		{
			name:  "ActorPostLockPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				_ = r.ActorPostLockPolicy("fakeActor", "fakeActorId")
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyActorTarget("fakeActor")),
				newTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
			},
			wantNumberOfRows: 1,
		},
		{
			name:  "ComponentOutboundPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, testStateStoreName)
				_ = r.ComponentOutboundPolicy(testStateStoreName, resiliency.Statestore)
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				newTag(diag.StatusKey.Name(), "closed"),
			},
			wantNumberOfRows: 3,
		},
		{
			name:  "ComponentInboundPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, testStateStoreName)
				_ = r.ComponentInboundPolicy(testStateStoreName, resiliency.Statestore)
			},
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.InboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				newTag(diag.StatusKey.Name(), "closed"),
			},
			wantNumberOfRows: 3,
		},
		{
			name:          "ComponentInboundDefaultPolicy",
			enableDefault: true,
			appID:         testAppID,
			unitFn: func() {
				r := createDefaultTestResiliency(testResiliencyName, testResiliencyNamespace)
				_ = r.ComponentInboundPolicy(testStateStoreName, resiliency.Statestore)
			},
			wantNumberOfRows: 3,
			wantTags: []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.InboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				newTag(diag.StatusKey.Name(), "closed"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Cleanup(func() {
				view.Unregister(view.Find(resiliencyCountViewName))
			})
			_ = diag.InitMetrics(test.appID, "fakeRuntimeNamespace", nil, test.enableDefault)
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyCountViewName)
			if test.wantErr {
				require.Error(t, err)
			}
			require.NoError(t, err)
			require.Equal(t, test.wantNumberOfRows, len(rows))
			for _, wantTag := range test.wantTags {
				requireTagExist(t, rows, wantTag)
			}
		})
	}
}

func TestResiliencyCountMonitoringCBStates(t *testing.T) {
	tests := []struct {
		name                string
		unitFn              func()
		wantNumberOfRows    int
		wantCbStateTagCount map[tag.Tag]int64
		wantErr             bool
	}{
		{
			name: "EndpointPolicyCloseState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 2; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, nil
					})
				}
			},
			wantNumberOfRows:    3,
			wantCbStateTagCount: map[tag.Tag]int64{newTag(diag.StatusKey.Name(), "closed"): 2},
		},
		{
			name: "EndpointPolicyOpenState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 2; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
			},
			wantNumberOfRows: 4,
			wantCbStateTagCount: map[tag.Tag]int64{
				newTag(diag.StatusKey.Name(), "closed"): 1,
				newTag(diag.StatusKey.Name(), "open"):   1,
			},
		},
		{
			name: "EndpointPolicyHalfOpenState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 2; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
				// let the circuit breaker to go to half open state (5x cb timeout)
				time.Sleep(500 * time.Millisecond)
				policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
				policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
				_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
					return nil, fmt.Errorf("fake error")
				})
			},
			wantNumberOfRows: 5,
			wantCbStateTagCount: map[tag.Tag]int64{
				newTag(diag.StatusKey.Name(), "closed"):    1,
				newTag(diag.StatusKey.Name(), "open"):      1,
				newTag(diag.StatusKey.Name(), "half-open"): 1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Cleanup(func() {
				view.Unregister(view.Find(resiliencyCountViewName))
			})
			_ = diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil, false)
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyCountViewName)
			if test.wantErr {
				require.Error(t, err)
			}
			require.NoError(t, err)
			require.Equal(t, test.wantNumberOfRows, len(rows))

			wantedTags := []tag.Tag{
				newTag("app_id", testAppID),
				newTag("name", testResiliencyName),
				newTag("namespace", testResiliencyNamespace),
				newTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				newTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				newTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				newTag(diag.TargetKey.Name(), diag.ResiliencyAppTarget("fakeApp")),
			}
			for _, wantTag := range wantedTags {
				requireTagExist(t, rows, wantTag)
			}
			for cbTag, wantCount := range test.wantCbStateTagCount {
				gotCount := getCountForTagSet(rows, map[tag.Tag]bool{cbTag: true, newTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)): true})
				require.Equal(t, wantCount, gotCount)
			}
		})
	}
}

func createTestResiliency(resiliencyName string, resiliencyNamespace string, stateStoreName string) *resiliency.Resiliency {
	r := resiliency.FromConfigurations(logger.NewLogger("fake-logger"), newTestResiliencyConfig(
		resiliencyName,
		resiliencyNamespace,
		"fakeApp",
		"fakeActor",
		stateStoreName,
	))
	return r
}

func createDefaultTestResiliency(resiliencyName string, resiliencyNamespace string) *resiliency.Resiliency {
	r := resiliency.FromConfigurations(logger.NewLogger("fake-logger"), newTestDefaultResiliencyConfig(
		resiliencyName, resiliencyNamespace,
	))
	return r
}

func TestResiliencyLoadedMonitoring(t *testing.T) {
	t.Run(resiliencyLoadedViewName, func(t *testing.T) {
		t.Cleanup(func() {
			view.Unregister(view.Find(resiliencyCountViewName))
		})
		_ = diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil, false)
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

func getCountForTagSet(rows []*view.Row, wantedTagSetCount map[tag.Tag]bool) int64 {
	for _, row := range rows {
		foundTags := 0
		for _, aTag := range row.Tags {
			if wantedTagSetCount[aTag] {
				foundTags++
			}
		}
		if foundTags == len(wantedTagSetCount) {
			return row.Data.(*view.CountData).Value
		}
	}
	return 0
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

func newTestDefaultResiliencyConfig(resiliencyName, resiliencyNamespace string) *resiliencyV1alpha.Resiliency {
	return &resiliencyV1alpha.Resiliency{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resiliencyName,
			Namespace: resiliencyNamespace,
		},
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"DefaultComponentCircuitBreakerPolicy": {
						Interval:    "0",
						Timeout:     "100ms",
						Trip:        "consecutiveFailures > 2",
						MaxRequests: 1,
					},
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"DefaultComponentInboundRetryPolicy": {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: ptr.Of(3),
					},
				},
				Timeouts: map[string]string{
					"DefaultTimeoutPolicy": "100ms",
				},
			},
		},
	}
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
					"testTimeout": "100ms",
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"testRetry": {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: ptr.Of(3),
					},
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"testCB": {
						Interval:    "0",
						Timeout:     "100ms",
						Trip:        "consecutiveFailures > 2",
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
