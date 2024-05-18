package diagnostics_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	resiliencyCountViewName      = "resiliency/count"
	resiliencyActivationViewName = "resiliency/activations_total"
	resiliencyLoadedViewName     = "resiliency/loaded"
	testAppID                    = "fakeID"
	testResiliencyName           = "testResiliency"
	testResiliencyNamespace      = "testNamespace"
	testStateStoreName           = "testStateStore"
)

func cleanupRegisteredViews() {
	diag.CleanupRegisteredViews(
		resiliencyCountViewName,
		resiliencyLoadedViewName,
		resiliencyActivationViewName)
}

func TestResiliencyCountMonitoring(t *testing.T) {
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
				_ = r.EndpointPolicy("fakeApp", "fakeEndpoint")
			},
			wantNumberOfRows: 3,
			wantTags: []tag.Tag{
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyAppTarget("fakeApp")),
				diag.NewTag(diag.StatusKey.Name(), "closed"),
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
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyActorTarget("fakeActor")),
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)),
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
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyActorTarget("fakeActor")),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
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
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)),
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
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.InboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)),
			},
			wantNumberOfRows: 3,
		},
		{
			name:  "ComponentInboundDefaultPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createDefaultTestResiliency(testResiliencyName, testResiliencyNamespace)
				_ = r.ComponentInboundPolicy(testStateStoreName, resiliency.Statestore)
			},
			wantNumberOfRows: 3,
			wantTags: []tag.Tag{
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.InboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)),
			},
		},
		{
			name:  "ComponentOutboundDefaultPolicy",
			appID: testAppID,
			unitFn: func() {
				r := createDefaultTestResiliency(testResiliencyName, testResiliencyNamespace)
				_ = r.ComponentOutboundPolicy(testStateStoreName, resiliency.Statestore)
			},
			wantNumberOfRows: 2,
			wantTags: []tag.Tag{
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyComponentTarget(testStateStoreName, string(resiliency.Statestore))),
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleanupRegisteredViews()
			require.NoError(t, diag.InitMetrics(test.appID, "fakeRuntimeNamespace", nil, false))
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyCountViewName)
			if test.wantErr {
				require.Error(t, err)
			}
			require.NoError(t, err)
			require.Len(t, rows, test.wantNumberOfRows)
			for _, wantTag := range test.wantTags {
				diag.RequireTagExist(t, rows, wantTag)
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
			wantCbStateTagCount: map[tag.Tag]int64{diag.NewTag(diag.StatusKey.Name(), "closed"): 2},
		},
		{
			name: "EndpointPolicyOpenState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 3; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
			},
			wantNumberOfRows: 4,
			wantCbStateTagCount: map[tag.Tag]int64{
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)): 2,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):   1,
			},
		},
		{
			name: "EndpointPolicyHalfOpenState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 3; i++ {
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
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)):   2,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):     1,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateHalfOpen)): 1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleanupRegisteredViews()
			require.NoError(t, diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil, false))
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyCountViewName)
			require.NoError(t, err)
			require.Len(t, rows, test.wantNumberOfRows)

			wantedTags := []tag.Tag{
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyAppTarget("fakeApp")),
			}
			for _, wantTag := range wantedTags {
				diag.RequireTagExist(t, rows, wantTag)
			}
			for cbTag, wantCount := range test.wantCbStateTagCount {
				gotCount := diag.GetValueForObservationWithTagSet(
					rows, map[tag.Tag]bool{cbTag: true, diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)): true})
				require.Equal(t, wantCount, gotCount)
			}
		})
	}
}

func TestResiliencyActivationsCountMonitoring(t *testing.T) {
	tests := []struct {
		name                string
		unitFn              func()
		wantNumberOfRows    int
		wantCbStateTagCount map[tag.Tag]int64
		wantTags            []tag.Tag
		wantRetriesCount    int64
		wantTimeoutCount    int64
		wantCBChangeCount   int64
	}{
		{
			name: "EndpointPolicyNoActivations",
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
			wantNumberOfRows: 0,
		},
		{
			name: "EndpointPolicyOneRetryNoCBTrip",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
				policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
				_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
					return nil, fmt.Errorf("fake error")
				})
			},
			wantNumberOfRows: 1,
			wantRetriesCount: 1,
			wantTags: []tag.Tag{
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
			},
			wantCbStateTagCount: map[tag.Tag]int64{
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)): 0,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):   0,
			},
		},
		{
			name: "EndpointPolicyTwoRetryWithCBTrip",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
				for i := 0; i < 2; i++ {
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
			},
			wantNumberOfRows: 2,
			wantRetriesCount: 2,
			wantTags: []tag.Tag{
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
			},
			wantCbStateTagCount: map[tag.Tag]int64{
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)): 0,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):   1,
			},
		},
		{
			name: "EndpointPolicyTwoRetryWithCBTripTimeout",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
				policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
				_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
					time.Sleep(500 * time.Millisecond)
					return nil, fmt.Errorf("fake error")
				})
				policyRunner = resiliency.NewRunner[any](context.Background(), policyDef)
				_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
					return nil, fmt.Errorf("fake error")
				})
			},
			wantNumberOfRows: 3,
			wantRetriesCount: 2,
			wantTimeoutCount: 1,
			wantTags: []tag.Tag{
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
			},
			wantCbStateTagCount: map[tag.Tag]int64{
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)): 0,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):   1,
			},
		},
		{
			name: "EndpointPolicyOpenAndCloseState",
			unitFn: func() {
				r := createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStateStore")
				for i := 0; i < 2; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
				// let the circuit breaker to go to half open state (5x cb timeout) and then return success to close it
				time.Sleep(500 * time.Millisecond)
				policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
				policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
				_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
					return nil, nil
				})

				// now open the circuit breaker again
				for i := 0; i < 2; i++ {
					policyDef := r.EndpointPolicy("fakeApp", "fakeEndpoint")
					policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
					_, _ = policyRunner(func(ctx context.Context) (interface{}, error) {
						return nil, fmt.Errorf("fake error")
					})
				}
			},
			wantNumberOfRows: 3,
			wantRetriesCount: 4,
			wantTags: []tag.Tag{
				diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)),
				diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)),
			},
			wantCbStateTagCount: map[tag.Tag]int64{
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateClosed)): 1,
				diag.NewTag(diag.StatusKey.Name(), string(breaker.StateOpen)):   2,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleanupRegisteredViews()
			require.NoError(t, diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil, false))
			test.unitFn()
			rows, err := view.RetrieveData(resiliencyActivationViewName)
			require.NoError(t, err)
			require.Len(t, rows, test.wantNumberOfRows)
			if test.wantNumberOfRows == 0 {
				return
			}

			wantedTags := []tag.Tag{
				diag.NewTag("app_id", testAppID),
				diag.NewTag("name", testResiliencyName),
				diag.NewTag("namespace", testResiliencyNamespace),
				diag.NewTag(diag.FlowDirectionKey.Name(), string(diag.OutboundPolicyFlowDirection)),
				diag.NewTag(diag.TargetKey.Name(), diag.ResiliencyAppTarget("fakeApp")),
			}
			wantedTags = append(wantedTags, test.wantTags...)
			for _, wantTag := range wantedTags {
				diag.RequireTagExist(t, rows, wantTag)
			}
			for cbTag, wantCount := range test.wantCbStateTagCount {
				gotCount := diag.GetValueForObservationWithTagSet(
					rows, map[tag.Tag]bool{cbTag: true, diag.NewTag(diag.PolicyKey.Name(), string(diag.CircuitBreakerPolicy)): true})
				require.Equal(t, wantCount, gotCount)
			}
			gotRetriesCount := diag.GetValueForObservationWithTagSet(
				rows, map[tag.Tag]bool{diag.NewTag(diag.PolicyKey.Name(), string(diag.RetryPolicy)): true})
			require.Equal(t, test.wantRetriesCount, gotRetriesCount)

			gotTimeoutCount := diag.GetValueForObservationWithTagSet(
				rows, map[tag.Tag]bool{diag.NewTag(diag.PolicyKey.Name(), string(diag.TimeoutPolicy)): true})
			require.Equal(t, test.wantTimeoutCount, gotTimeoutCount)
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
		cleanupRegisteredViews()
		require.NoError(t, diag.InitMetrics(testAppID, "fakeRuntimeNamespace", nil, false))
		_ = createTestResiliency(testResiliencyName, testResiliencyNamespace, "fakeStoreName")

		rows, err := view.RetrieveData(resiliencyLoadedViewName)

		require.NoError(t, err)
		require.Len(t, rows, 1)

		diag.RequireTagExist(t, rows, diag.NewTag("app_id", testAppID))
		diag.RequireTagExist(t, rows, diag.NewTag("name", testResiliencyName))
		diag.RequireTagExist(t, rows, diag.NewTag("namespace", testResiliencyNamespace))
	})
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
						Trip:        "consecutiveFailures > 4",
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
