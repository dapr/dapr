package diagnostics

import (
	"context"
	"strconv"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

const (
	SuccessClass = "success"
	FailureClass = "failure"
	UnknownClass = "unknown"
)

// Tag keys.
var (
	componentKey        = tag.MustNewKey("component")
	failReasonKey       = tag.MustNewKey("reason")
	operationKey        = tag.MustNewKey("operation")
	actorTypeKey        = tag.MustNewKey("actor_type")
	trustDomainKey      = tag.MustNewKey("trustDomain")
	namespaceKey        = tag.MustNewKey("namespace")
	policyActionKey     = tag.MustNewKey("policyAction")
	resiliencyNameKey   = tag.MustNewKey("name")
	policyKey           = tag.MustNewKey("policy")
	componentNameKey    = tag.MustNewKey("componentName")
	destinationAppIDKey = tag.MustNewKey("dst_app_id")
	sourceAppIDKey      = tag.MustNewKey("src_app_id")
	methodKey           = tag.MustNewKey("method")
	statusKey           = tag.MustNewKey("status")
)

// serviceMetrics holds dapr runtime metric monitoring methods.
type serviceMetrics struct {
	// component metrics
	componentLoaded        *stats.Int64Measure
	componentInitCompleted *stats.Int64Measure
	componentInitFailed    *stats.Int64Measure

	// mTLS metrics
	mtlsInitCompleted             *stats.Int64Measure
	mtlsInitFailed                *stats.Int64Measure
	mtlsWorkloadCertRotated       *stats.Int64Measure
	mtlsWorkloadCertRotatedFailed *stats.Int64Measure

	// Actor metrics
	actorStatusReportTotal       *stats.Int64Measure
	actorStatusReportFailedTotal *stats.Int64Measure
	actorTableOperationRecvTotal *stats.Int64Measure
	actorRebalancedTotal         *stats.Int64Measure
	actorDeactivationTotal       *stats.Int64Measure
	actorDeactivationFailedTotal *stats.Int64Measure
	actorPendingCalls            *stats.Int64Measure

	// Access Control Lists for Service Invocation metrics
	appPolicyActionAllowed    *stats.Int64Measure
	globalPolicyActionAllowed *stats.Int64Measure
	appPolicyActionBlocked    *stats.Int64Measure
	globalPolicyActionBlocked *stats.Int64Measure

	// Service Invocation metrics
	serviceInvocationRequestSentTotal        *stats.Int64Measure
	serviceInvocationRequestReceivedTotal    *stats.Int64Measure
	serviceInvocationResponseSentTotal       *stats.Int64Measure
	serviceInvocationResponseReceivedTotal   *stats.Int64Measure
	serviceInvocationResponseReceivedLatency *stats.Float64Measure

	appID   string
	ctx     context.Context
	enabled bool
}

// newServiceMetrics returns serviceMetrics instance with default service metric stats.
func newServiceMetrics() *serviceMetrics {
	return &serviceMetrics{
		// Runtime Component metrics
		componentLoaded: stats.Int64(
			"runtime/component/loaded",
			"The number of successfully loaded components.",
			stats.UnitDimensionless),
		componentInitCompleted: stats.Int64(
			"runtime/component/init_total",
			"The number of initialized components.",
			stats.UnitDimensionless),
		componentInitFailed: stats.Int64(
			"runtime/component/init_fail_total",
			"The number of component initialization failures.",
			stats.UnitDimensionless),

		// mTLS
		mtlsInitCompleted: stats.Int64(
			"runtime/mtls/init_total",
			"The number of successful mTLS authenticator initialization.",
			stats.UnitDimensionless),
		mtlsInitFailed: stats.Int64(
			"runtime/mtls/init_fail_total",
			"The number of mTLS authenticator init failures.",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotated: stats.Int64(
			"runtime/mtls/workload_cert_rotated_total",
			"The number of the successful workload certificate rotations.",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotatedFailed: stats.Int64(
			"runtime/mtls/workload_cert_rotated_fail_total",
			"The number of the failed workload certificate rotations.",
			stats.UnitDimensionless),

		// Actor
		actorStatusReportTotal: stats.Int64(
			"runtime/actor/status_report_total",
			"The number of the successful status reports to placement service.",
			stats.UnitDimensionless),
		actorStatusReportFailedTotal: stats.Int64(
			"runtime/actor/status_report_fail_total",
			"The number of the failed status reports to placement service.",
			stats.UnitDimensionless),
		actorTableOperationRecvTotal: stats.Int64(
			"runtime/actor/table_operation_recv_total",
			"The number of the received actor placement table operations.",
			stats.UnitDimensionless),
		actorRebalancedTotal: stats.Int64(
			"runtime/actor/rebalanced_total",
			"The number of the actor rebalance requests.",
			stats.UnitDimensionless),
		actorDeactivationTotal: stats.Int64(
			"runtime/actor/deactivated_total",
			"The number of the successful actor deactivation.",
			stats.UnitDimensionless),
		actorDeactivationFailedTotal: stats.Int64(
			"runtime/actor/deactivated_failed_total",
			"The number of the failed actor deactivation.",
			stats.UnitDimensionless),
		actorPendingCalls: stats.Int64(
			"runtime/actor/pending_actor_calls",
			"The number of pending actor calls waiting to acquire the per-actor lock.",
			stats.UnitDimensionless),

		// Access Control Lists for service invocation
		appPolicyActionAllowed: stats.Int64(
			"runtime/acl/app_policy_action_allowed_total",
			"The number of requests allowed by the app specific action specified in the access control policy.",
			stats.UnitDimensionless),
		globalPolicyActionAllowed: stats.Int64(
			"runtime/acl/global_policy_action_allowed_total",
			"The number of requests allowed by the global action specified in the access control policy.",
			stats.UnitDimensionless),
		appPolicyActionBlocked: stats.Int64(
			"runtime/acl/app_policy_action_blocked_total",
			"The number of requests blocked by the app specific action specified in the access control policy.",
			stats.UnitDimensionless),
		globalPolicyActionBlocked: stats.Int64(
			"runtime/acl/global_policy_action_blocked_total",
			"The number of requests blocked by the global action specified in the access control policy.",
			stats.UnitDimensionless),

		// Service Invocation
		serviceInvocationRequestSentTotal: stats.Int64(
			"runtime/service_invocation/req_sent_total",
			"The number of requests sent via service invocation.",
			stats.UnitDimensionless),
		serviceInvocationRequestReceivedTotal: stats.Int64(
			"runtime/service_invocation/req_recv_total",
			"The number of requests received via service invocation.",
			stats.UnitDimensionless),
		serviceInvocationResponseSentTotal: stats.Int64(
			"runtime/service_invocation/res_sent_total",
			"The number of responses sent via service invocation.",
			stats.UnitDimensionless),
		serviceInvocationResponseReceivedTotal: stats.Int64(
			"runtime/service_invocation/res_recv_total",
			"The number of responses received via service invocation.",
			stats.UnitDimensionless),
		serviceInvocationResponseReceivedLatency: stats.Float64(
			"runtime/service_invocation/res_recv_latency_ms",
			"The latency of service invocation response.",
			stats.UnitMilliseconds),

		// TODO: use the correct context for each request
		ctx:     context.Background(),
		enabled: false,
	}
}

// Init initialize metrics views for metrics.
func (s *serviceMetrics) Init(appID string) error {
	s.appID = appID
	s.enabled = true
	return view.Register(
		diagUtils.NewMeasureView(s.componentLoaded, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(s.componentInitCompleted, []tag.Key{appIDKey, componentKey}, view.Count()),
		diagUtils.NewMeasureView(s.componentInitFailed, []tag.Key{appIDKey, componentKey, failReasonKey, componentNameKey}, view.Count()),

		diagUtils.NewMeasureView(s.mtlsInitCompleted, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(s.mtlsInitFailed, []tag.Key{appIDKey, failReasonKey}, view.Count()),
		diagUtils.NewMeasureView(s.mtlsWorkloadCertRotated, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(s.mtlsWorkloadCertRotatedFailed, []tag.Key{appIDKey, failReasonKey}, view.Count()),

		diagUtils.NewMeasureView(s.actorStatusReportTotal, []tag.Key{appIDKey, actorTypeKey, operationKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorStatusReportFailedTotal, []tag.Key{appIDKey, actorTypeKey, operationKey, failReasonKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorTableOperationRecvTotal, []tag.Key{appIDKey, actorTypeKey, operationKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorRebalancedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorDeactivationTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorDeactivationFailedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorPendingCalls, []tag.Key{appIDKey, actorTypeKey}, view.Count()),

		diagUtils.NewMeasureView(s.appPolicyActionAllowed, []tag.Key{appIDKey, trustDomainKey, namespaceKey, operationKey, httpMethodKey, policyActionKey}, view.Count()),
		diagUtils.NewMeasureView(s.globalPolicyActionAllowed, []tag.Key{appIDKey, trustDomainKey, namespaceKey, operationKey, httpMethodKey, policyActionKey}, view.Count()),
		diagUtils.NewMeasureView(s.appPolicyActionBlocked, []tag.Key{appIDKey, trustDomainKey, namespaceKey, operationKey, httpMethodKey, policyActionKey}, view.Count()),
		diagUtils.NewMeasureView(s.globalPolicyActionBlocked, []tag.Key{appIDKey, trustDomainKey, namespaceKey, operationKey, httpMethodKey, policyActionKey}, view.Count()),

		diagUtils.NewMeasureView(s.serviceInvocationRequestSentTotal, []tag.Key{appIDKey, destinationAppIDKey, methodKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationRequestReceivedTotal, []tag.Key{appIDKey, sourceAppIDKey, methodKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseSentTotal, []tag.Key{appIDKey, destinationAppIDKey, methodKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseReceivedTotal, []tag.Key{appIDKey, sourceAppIDKey, methodKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseReceivedLatency, []tag.Key{appIDKey, sourceAppIDKey, methodKey, statusKey}, defaultLatencyDistribution),
	)
}

// ComponentLoaded records metric when component is loaded successfully.
func (s *serviceMetrics) ComponentLoaded() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(appIDKey, s.appID), s.componentLoaded.M(1))
	}
}

// ComponentInitialized records metric when component is initialized.
func (s *serviceMetrics) ComponentInitialized(component string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, componentKey, component),
			s.componentInitCompleted.M(1))
	}
}

// ComponentInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) ComponentInitFailed(component string, reason string, name string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, componentKey, component, failReasonKey, reason, componentNameKey, name),
			s.componentInitFailed.M(1))
	}
}

// MTLSInitCompleted records metric when component is initialized.
func (s *serviceMetrics) MTLSInitCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(appIDKey, s.appID), s.mtlsInitCompleted.M(1))
	}
}

// MTLSInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(appIDKey, s.appID, failReasonKey, reason),
			s.mtlsInitFailed.M(1))
	}
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded.
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(appIDKey, s.appID), s.mtlsWorkloadCertRotated.M(1))
	}
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed.
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(appIDKey, s.appID, failReasonKey, reason),
			s.mtlsWorkloadCertRotatedFailed.M(1))
	}
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(appIDKey, s.appID, operationKey, operation),
			s.actorStatusReportTotal.M(1))
	}
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(appIDKey, s.appID, operationKey, operation, failReasonKey, reason),
			s.actorStatusReportFailedTotal.M(1))
	}
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(appIDKey, s.appID, operationKey, operation),
			s.actorTableOperationRecvTotal.M(1))
	}
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
			s.actorRebalancedTotal.M(1))
	}
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
			s.actorDeactivationTotal.M(1))
	}
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, actorTypeKey, actorType, failReasonKey, reason),
			s.actorDeactivationFailedTotal.M(1))
	}
}

// ReportActorPendingCalls records the current pending actor locks.
func (s *serviceMetrics) ReportActorPendingCalls(actorType string, pendingLocks int32) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
			s.actorPendingCalls.M(int64(pendingLocks)))
	}
}

// RequestAllowedByAppAction records the requests allowed due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestAllowedByAppAction(appID, trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, appID,
				trustDomainKey, trustDomain,
				namespaceKey, namespace,
				operationKey, operation,
				httpMethodKey, httpverb,
				policyActionKey, policyAction),
			s.appPolicyActionAllowed.M(1))
	}
}

// RequestBlockedByAppAction records the requests blocked due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestBlockedByAppAction(appID, trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, appID,
				trustDomainKey, trustDomain,
				namespaceKey, namespace,
				operationKey, operation,
				httpMethodKey, httpverb,
				policyActionKey, policyAction),
			s.appPolicyActionBlocked.M(1))
	}
}

// RequestAllowedByGlobalAction records the requests allowed due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestAllowedByGlobalAction(appID, trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, appID,
				trustDomainKey, trustDomain,
				namespaceKey, namespace,
				operationKey, operation,
				httpMethodKey, httpverb,
				policyActionKey, policyAction),
			s.globalPolicyActionAllowed.M(1))
	}
}

// RequestBlockedByGlobalAction records the requests blocked due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestBlockedByGlobalAction(appID, trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, appID,
				trustDomainKey, trustDomain,
				namespaceKey, namespace,
				operationKey, operation,
				httpMethodKey, httpverb,
				policyActionKey, policyAction),
			s.globalPolicyActionBlocked.M(1))
	}
}

// ServiceInvocationRequestSent records the number of service invocation requests sent.
func (s *serviceMetrics) ServiceInvocationRequestSent(destinationAppID, method string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, s.appID,
				destinationAppIDKey, destinationAppID,
				methodKey, method),
			s.serviceInvocationRequestSentTotal.M(1))
	}
}

// ServiceInvocationRequestReceived records the number of service invocation requests received.
func (s *serviceMetrics) ServiceInvocationRequestReceived(sourceAppID, method string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				methodKey, method),
			s.serviceInvocationRequestReceivedTotal.M(1))
	}
}

// ServiceInvocationResponseSent records the number of service invocation responses sent.
func (s *serviceMetrics) ServiceInvocationResponseSent(destinationAppID, method string, status int32) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, s.appID,
				destinationAppIDKey, destinationAppID,
				methodKey, method,
				statusKey, statusCode),
			s.serviceInvocationResponseSentTotal.M(1))
	}
}

// ServiceInvocationResponseReceived records the number of service invocation responses received.
func (s *serviceMetrics) ServiceInvocationResponseReceived(sourceAppID, method string, status int32, start time.Time) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				methodKey, method,
				statusKey, statusCode),
			s.serviceInvocationResponseReceivedTotal.M(1))
		elapsed := float64(time.Since(start) / time.Millisecond)
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				methodKey, method,
				statusKey, statusCode),
			s.serviceInvocationResponseReceivedLatency.M(ElapsedSince(start)))
	}
}
