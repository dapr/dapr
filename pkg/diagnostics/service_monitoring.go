package diagnostics

import (
	"context"
	"strconv"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

// Tag keys.
var (
	componentKey        = tag.MustNewKey("component")
	failReasonKey       = tag.MustNewKey("reason")
	operationKey        = tag.MustNewKey("operation")
	actorTypeKey        = tag.MustNewKey("actor_type")
	trustDomainKey      = tag.MustNewKey("trustDomain")
	namespaceKey        = tag.MustNewKey("namespace")
	resiliencyNameKey   = tag.MustNewKey("name")
	policyKey           = tag.MustNewKey("policy")
	componentNameKey    = tag.MustNewKey("componentName")
	destinationAppIDKey = tag.MustNewKey("dst_app_id")
	sourceAppIDKey      = tag.MustNewKey("src_app_id")
	statusKey           = tag.MustNewKey("status")
	flowDirectionKey    = tag.MustNewKey("flow_direction")
	targetKey           = tag.MustNewKey("target")
	typeKey             = tag.MustNewKey("type")
)

const (
	typeUnary     = "unary"
	typeStreaming = "streaming"
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
	actorReminders               *stats.Int64Measure
	actorReminderFiredTotal      *stats.Int64Measure
	actorTimers                  *stats.Int64Measure
	actorTimerFiredTotal         *stats.Int64Measure

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
		actorTimers: stats.Int64(
			"runtime/actor/timers",
			"The number of actor timer requests.",
			stats.UnitDimensionless),
		actorReminders: stats.Int64(
			"runtime/actor/reminders",
			"The number of actor reminder requests.",
			stats.UnitDimensionless),
		actorReminderFiredTotal: stats.Int64(
			"runtime/actor/reminders_fired_total",
			"The number of actor reminders fired requests.",
			stats.UnitDimensionless),
		actorTimerFiredTotal: stats.Int64(
			"runtime/actor/timers_fired_total",
			"The number of actor timers fired requests.",
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
		diagUtils.NewMeasureView(s.actorTimers, []tag.Key{appIDKey, actorTypeKey}, view.LastValue()),
		diagUtils.NewMeasureView(s.actorReminders, []tag.Key{appIDKey, actorTypeKey}, view.LastValue()),
		diagUtils.NewMeasureView(s.actorReminderFiredTotal, []tag.Key{appIDKey, actorTypeKey, successKey}, view.Count()),
		diagUtils.NewMeasureView(s.actorTimerFiredTotal, []tag.Key{appIDKey, actorTypeKey, successKey}, view.Count()),

		diagUtils.NewMeasureView(s.appPolicyActionAllowed, []tag.Key{appIDKey, trustDomainKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(s.globalPolicyActionAllowed, []tag.Key{appIDKey, trustDomainKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(s.appPolicyActionBlocked, []tag.Key{appIDKey, trustDomainKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(s.globalPolicyActionBlocked, []tag.Key{appIDKey, trustDomainKey, namespaceKey}, view.Count()),

		diagUtils.NewMeasureView(s.serviceInvocationRequestSentTotal, []tag.Key{appIDKey, destinationAppIDKey, typeKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationRequestReceivedTotal, []tag.Key{appIDKey, sourceAppIDKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseSentTotal, []tag.Key{appIDKey, destinationAppIDKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseReceivedTotal, []tag.Key{appIDKey, sourceAppIDKey, statusKey, typeKey}, view.Count()),
		diagUtils.NewMeasureView(s.serviceInvocationResponseReceivedLatency, []tag.Key{appIDKey, sourceAppIDKey, statusKey}, defaultLatencyDistribution),
	)
}

// ComponentLoaded records metric when component is loaded successfully.
func (s *serviceMetrics) ComponentLoaded() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(s.componentLoaded.Name(), appIDKey, s.appID), s.componentLoaded.M(1))
	}
}

// ComponentInitialized records metric when component is initialized.
func (s *serviceMetrics) ComponentInitialized(component string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.componentInitCompleted.Name(), appIDKey, s.appID, componentKey, component),
			s.componentInitCompleted.M(1))
	}
}

// ComponentInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) ComponentInitFailed(component string, reason string, name string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.componentInitFailed.Name(), appIDKey, s.appID, componentKey, component, failReasonKey, reason, componentNameKey, name),
			s.componentInitFailed.M(1))
	}
}

// MTLSInitCompleted records metric when component is initialized.
func (s *serviceMetrics) MTLSInitCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(s.mtlsInitCompleted.Name(), appIDKey, s.appID), s.mtlsInitCompleted.M(1))
	}
}

// MTLSInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(s.mtlsInitFailed.Name(), appIDKey, s.appID, failReasonKey, reason),
			s.mtlsInitFailed.M(1))
	}
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded.
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diagUtils.WithTags(s.mtlsWorkloadCertRotated.Name(), appIDKey, s.appID), s.mtlsWorkloadCertRotated.M(1))
	}
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed.
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(s.mtlsWorkloadCertRotatedFailed.Name(), appIDKey, s.appID, failReasonKey, reason),
			s.mtlsWorkloadCertRotatedFailed.M(1))
	}
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(s.actorStatusReportTotal.Name(), appIDKey, s.appID, operationKey, operation),
			s.actorStatusReportTotal.M(1))
	}
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(s.actorStatusReportFailedTotal.Name(), appIDKey, s.appID, operationKey, operation, failReasonKey, reason),
			s.actorStatusReportFailedTotal.M(1))
	}
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diagUtils.WithTags(s.actorTableOperationRecvTotal.Name(), appIDKey, s.appID, operationKey, operation),
			s.actorTableOperationRecvTotal.M(1))
	}
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorRebalancedTotal.Name(), appIDKey, s.appID, actorTypeKey, actorType),
			s.actorRebalancedTotal.M(1))
	}
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorDeactivationTotal.Name(), appIDKey, s.appID, actorTypeKey, actorType),
			s.actorDeactivationTotal.M(1))
	}
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType string, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorDeactivationFailedTotal.Name(), appIDKey, s.appID, actorTypeKey, actorType, failReasonKey, reason),
			s.actorDeactivationFailedTotal.M(1))
	}
}

// ActorReminderFired records metric when actor reminder is fired.
func (s *serviceMetrics) ActorReminderFired(actorType string, success bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorReminderFiredTotal.Name(), appIDKey, s.appID, actorTypeKey, actorType, successKey, strconv.FormatBool(success)),
			s.actorReminderFiredTotal.M(1))
	}
}

// ActorTimerFired records metric when actor timer is fired.
func (s *serviceMetrics) ActorTimerFired(actorType string, success bool) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorTimerFiredTotal.Name(), appIDKey, s.appID, actorTypeKey, actorType, successKey, strconv.FormatBool(success)),
			s.actorTimerFiredTotal.M(1))
	}
}

// ActorReminders records the current number of reminders for an actor type.
func (s *serviceMetrics) ActorReminders(actorType string, reminders int64) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorReminders.Name(), appIDKey, s.appID, actorTypeKey, actorType),
			s.actorReminders.M(reminders))
	}
}

// ActorTimers records the current number of timers for an actor type.
func (s *serviceMetrics) ActorTimers(actorType string, timers int64) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorTimers.Name(), appIDKey, s.appID, actorTypeKey, actorType),
			s.actorTimers.M(timers))
	}
}

// ReportActorPendingCalls records the current pending actor locks.
func (s *serviceMetrics) ReportActorPendingCalls(actorType string, pendingLocks int32) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(s.actorPendingCalls.Name(), appIDKey, s.appID, actorTypeKey, actorType),
			s.actorPendingCalls.M(int64(pendingLocks)))
	}
}

// RequestAllowedByAppAction records the requests allowed due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestAllowedByAppAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.appPolicyActionAllowed.Name(),
				appIDKey, spiffeID.AppID(),
				trustDomainKey, spiffeID.TrustDomain().String(),
				namespaceKey, spiffeID.Namespace()),
			s.appPolicyActionAllowed.M(1))
	}
}

// RequestBlockedByAppAction records the requests blocked due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestBlockedByAppAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.appPolicyActionBlocked.Name(),
				appIDKey, spiffeID.AppID(),
				trustDomainKey, spiffeID.TrustDomain().String(),
				namespaceKey, spiffeID.Namespace()),
			s.appPolicyActionBlocked.M(1))
	}
}

// RequestAllowedByGlobalAction records the requests allowed due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestAllowedByGlobalAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.globalPolicyActionAllowed.Name(),
				appIDKey, spiffeID.AppID(),
				trustDomainKey, spiffeID.TrustDomain().String(),
				namespaceKey, spiffeID.Namespace()),
			s.globalPolicyActionAllowed.M(1))
	}
}

// RequestBlockedByGlobalAction records the requests blocked due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestBlockedByGlobalAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.globalPolicyActionBlocked.Name(),
				appIDKey, spiffeID.AppID(),
				trustDomainKey, spiffeID.TrustDomain().String(),
				namespaceKey, spiffeID.Namespace()),
			s.globalPolicyActionBlocked.M(1))
	}
}

// ServiceInvocationRequestSent records the number of service invocation requests sent.
func (s *serviceMetrics) ServiceInvocationRequestSent(destinationAppID string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationRequestSentTotal.Name(),
				appIDKey, s.appID,
				destinationAppIDKey, destinationAppID,
				typeKey, typeUnary,
			),
			s.serviceInvocationRequestSentTotal.M(1))
	}
}

// ServiceInvocationRequestSent records the number of service invocation requests sent.
func (s *serviceMetrics) ServiceInvocationStreamingRequestSent(destinationAppID string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationRequestSentTotal.Name(),
				appIDKey, s.appID,
				destinationAppIDKey, destinationAppID,
				typeKey, typeStreaming),
			s.serviceInvocationRequestSentTotal.M(1))
	}
}

// ServiceInvocationRequestReceived records the number of service invocation requests received.
func (s *serviceMetrics) ServiceInvocationRequestReceived(sourceAppID string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationRequestReceivedTotal.Name(),
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID),
			s.serviceInvocationRequestReceivedTotal.M(1))
	}
}

// ServiceInvocationResponseSent records the number of service invocation responses sent.
func (s *serviceMetrics) ServiceInvocationResponseSent(destinationAppID string, status int32) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationResponseSentTotal.Name(),
				appIDKey, s.appID,
				destinationAppIDKey, destinationAppID,
				statusKey, statusCode),
			s.serviceInvocationResponseSentTotal.M(1))
	}
}

// ServiceInvocationResponseReceived records the number of service invocation responses received.
func (s *serviceMetrics) ServiceInvocationResponseReceived(sourceAppID string, status int32, start time.Time) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationResponseReceivedTotal.Name(),
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				statusKey, statusCode,
				typeKey, typeUnary),
			s.serviceInvocationResponseReceivedTotal.M(1))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationResponseReceivedLatency.Name(),
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				statusKey, statusCode),
			s.serviceInvocationResponseReceivedLatency.M(ElapsedSince(start)))
	}
}

// ServiceInvocationStreamingResponseReceived records the number of service invocation responses received for streaming operations.
// this is mainly targeted to recording errors for proxying gRPC streaming calls
func (s *serviceMetrics) ServiceInvocationStreamingResponseReceived(sourceAppID string, status int32) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		stats.RecordWithTags(
			s.ctx,
			diagUtils.WithTags(
				s.serviceInvocationResponseReceivedTotal.Name(),
				appIDKey, s.appID,
				sourceAppIDKey, sourceAppID,
				statusKey, statusCode,
				typeKey, typeStreaming),
			s.serviceInvocationResponseReceivedTotal.M(1))
	}
}
