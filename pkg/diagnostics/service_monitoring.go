package diagnostics

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/dapr/dapr/pkg/security/spiffe"
)

// Tag keys.
var (
	componentKey        = "component"
	failReasonKey       = "reason"
	operationKey        = "operation"
	actorTypeKey        = "actor_type"
	trustDomainKey      = "trustDomain"
	namespaceKey        = "namespace"
	resiliencyNameKey   = "name"
	policyKey           = "policy"
	componentNameKey    = "componentName"
	destinationAppIDKey = "dst_app_id"
	sourceAppIDKey      = "src_app_id"
	statusKey           = "status"
	flowDirectionKey    = "flow_direction"
	targetKey           = "target"
	typeKey             = "type"
)

const (
	typeUnary     = "unary"
	typeStreaming = "streaming"
)

// serviceMetrics holds dapr runtime metric monitoring methods.
type serviceMetrics struct {
	meter metric.Meter

	// component metrics
	componentLoaded        metric.Int64Counter
	componentInitCompleted metric.Int64Counter
	componentInitFailed    metric.Int64Counter

	// mTLS metrics
	mtlsInitCompleted             metric.Int64Counter
	mtlsInitFailed                metric.Int64Counter
	mtlsWorkloadCertRotated       metric.Int64Counter
	mtlsWorkloadCertRotatedFailed metric.Int64Counter

	// Actor metrics
	actorStatusReportTotal       metric.Int64Counter
	actorStatusReportFailedTotal metric.Int64Counter
	actorTableOperationRecvTotal metric.Int64Counter
	actorRebalancedTotal         metric.Int64Counter
	actorDeactivationTotal       metric.Int64Counter
	actorDeactivationFailedTotal metric.Int64Counter
	actorPendingCalls            metric.Int64Counter
	actorReminders               metric.Int64Counter
	actorReminderFiredTotal      metric.Int64Counter
	actorTimers                  metric.Int64Counter
	actorTimerFiredTotal         metric.Int64Counter

	// Access Control Lists for Service Invocation metrics
	appPolicyActionAllowed    metric.Int64Counter
	globalPolicyActionAllowed metric.Int64Counter
	appPolicyActionBlocked    metric.Int64Counter
	globalPolicyActionBlocked metric.Int64Counter

	// Service Invocation metrics
	serviceInvocationRequestSentTotal        metric.Int64Counter
	serviceInvocationRequestReceivedTotal    metric.Int64Counter
	serviceInvocationResponseSentTotal       metric.Int64Counter
	serviceInvocationResponseReceivedTotal   metric.Int64Counter
	serviceInvocationResponseReceivedLatency metric.Float64Histogram

	appID   string
	ctx     context.Context
	enabled bool
}

// newServiceMetrics returns serviceMetrics instance with default service metric stats.
func newServiceMetrics() *serviceMetrics {
	m := otel.Meter("runtime")

	return &serviceMetrics{
		meter:   m,
		enabled: false,
		// TODO: use the correct context for each request
		ctx: context.Background(),
	}
}

// Init initialize metrics views for metrics.
func (s *serviceMetrics) Init(appID string) error {
	componentLoaded, err := s.meter.Int64Counter(
		"runtime.component.loaded",
		metric.WithDescription("The number of successfully loaded components."),
	)
	if err != nil {
		return err
	}

	componentInitCompleted, err := s.meter.Int64Counter(
		"runtime.component.init_total",
		metric.WithDescription("The number of initialized components."),
	)
	if err != nil {
		return err
	}

	componentInitFailed, err := s.meter.Int64Counter(
		"runtime.component.init_fail_total",
		metric.WithDescription("The number of component initialization failures."),
	)
	if err != nil {
		return err
	}

	// mTLS
	mtlsInitCompleted, err := s.meter.Int64Counter(
		"runtime.mtls.init_total",
		metric.WithDescription("The number of successful mTLS authenticator initialization."),
	)
	if err != nil {
		return err
	}

	mtlsInitFailed, err := s.meter.Int64Counter(
		"runtime.mtls.init_fail_total",
		metric.WithDescription("The number of mTLS authenticator init failures."),
	)
	if err != nil {
		return err
	}

	mtlsWorkloadCertRotated, err := s.meter.Int64Counter(
		"runtime.mtls.workload_cert_rotated_total",
		metric.WithDescription("The number of the successful workload certificate rotations."),
	)
	if err != nil {
		return err
	}

	mtlsWorkloadCertRotatedFailed, err := s.meter.Int64Counter(
		"runtime.mtls.workload_cert_rotated_fail_total",
		metric.WithDescription("The number of the failed workload certificate rotations."),
	)
	if err != nil {
		return err
	}

	// Actor
	actorStatusReportTotal, err := s.meter.Int64Counter(
		"runtime.actor.status_report_total",
		metric.WithDescription("The number of the successful status reports to placement service."),
	)
	if err != nil {
		return err
	}

	actorStatusReportFailedTotal, err := s.meter.Int64Counter(
		"runtime.actor.status_report_fail_total",
		metric.WithDescription("The number of the failed status reports to placement service."),
	)
	if err != nil {
		return err
	}

	actorTableOperationRecvTotal, err := s.meter.Int64Counter(
		"runtime.actor.table_operation_recv_total",
		metric.WithDescription("The number of the received actor placement table operations."),
	)
	if err != nil {
		return err
	}

	actorRebalancedTotal, err := s.meter.Int64Counter(
		"runtime.actor.rebalanced_total",
		metric.WithDescription("The number of the actor rebalance requests."),
	)
	if err != nil {
		return err
	}

	actorDeactivationTotal, err := s.meter.Int64Counter(
		"runtime.actor.deactivated_total",
		metric.WithDescription("The number of the successful actor deactivation."),
	)
	if err != nil {
		return err
	}

	actorDeactivationFailedTotal, err := s.meter.Int64Counter(
		"runtime.actor.deactivated_failed_total",
		metric.WithDescription("The number of the failed actor deactivation."),
	)
	if err != nil {
		return err
	}

	actorPendingCalls, err := s.meter.Int64Counter(
		"runtime.actor.pending_actor_calls",
		metric.WithDescription("The number of pending actor calls waiting to acquire the per-actor lock."),
	)
	if err != nil {
		return err
	}

	actorTimers, err := s.meter.Int64Counter(
		"runtime.actor/timers",
		metric.WithDescription("The number of actor timer requests."),
	)
	if err != nil {
		return err
	}

	actorReminders, err := s.meter.Int64Counter(
		"runtime.actor/reminders",
		metric.WithDescription("The number of actor reminder requests."),
	)
	if err != nil {
		return err
	}

	actorReminderFiredTotal, err := s.meter.Int64Counter(
		"runtime.actor/reminders_fired_total",
		metric.WithDescription("The number of actor reminders fired requests."),
	)
	if err != nil {
		return err
	}

	actorTimerFiredTotal, err := s.meter.Int64Counter(
		"runtime.actor/timers_fired_total",
		metric.WithDescription("The number of actor timers fired requests."),
	)
	if err != nil {
		return err
	}

	// Access Control Lists for service invocation
	appPolicyActionAllowed, err := s.meter.Int64Counter(
		"runtime.acl.app_policy_action_allowed_total",
		metric.WithDescription("The number of requests allowed by the app specific action specified in the access control policy."),
	)
	if err != nil {
		return err
	}

	globalPolicyActionAllowed, err := s.meter.Int64Counter(
		"runtime.acl.global_policy_action_allowed_total",
		metric.WithDescription("The number of requests allowed by the global action specified in the access control policy."),
	)
	if err != nil {
		return err
	}

	appPolicyActionBlocked, err := s.meter.Int64Counter(
		"runtime.acl.app_policy_action_blocked_total",
		metric.WithDescription("The number of requests blocked by the app specific action specified in the access control policy."),
	)
	if err != nil {
		return err
	}

	globalPolicyActionBlocked, err := s.meter.Int64Counter(
		"runtime.acl.global_policy_action_blocked_total",
		metric.WithDescription("The number of requests blocked by the global action specified in the access control policy."),
	)
	if err != nil {
		return err
	}

	// Service Invocation
	serviceInvocationRequestSentTotal, err := s.meter.Int64Counter(
		"runtime.service_invocation/req_sent_total",
		metric.WithDescription("The number of requests sent via service invocation."),
	)
	if err != nil {
		return err
	}

	serviceInvocationRequestReceivedTotal, err := s.meter.Int64Counter(
		"runtime.service_invocation/req_recv_total",
		metric.WithDescription("The number of requests received via service invocation."),
	)
	if err != nil {
		return err
	}

	serviceInvocationResponseSentTotal, err := s.meter.Int64Counter(
		"runtime.service_invocation/res_sent_total",
		metric.WithDescription("The number of responses sent via service invocation."),
	)
	if err != nil {
		return err
	}

	serviceInvocationResponseReceivedTotal, err := s.meter.Int64Counter(
		"runtime.service_invocation/res_recv_total",
		metric.WithDescription("The number of responses received via service invocation."),
	)
	if err != nil {
		return err
	}

	serviceInvocationResponseReceivedLatency, err := s.meter.Float64Histogram(
		"runtime.service_invocation/res_recv_latency_ms",
		metric.WithDescription("The latency of service invocation response."),
	)
	if err != nil {
		return err
	}

	s.componentLoaded = componentLoaded
	s.componentInitCompleted = componentInitCompleted
	s.componentInitFailed = componentInitFailed
	s.mtlsInitCompleted = mtlsInitCompleted
	s.mtlsInitFailed = mtlsInitFailed
	s.mtlsWorkloadCertRotated = mtlsWorkloadCertRotated
	s.mtlsWorkloadCertRotatedFailed = mtlsWorkloadCertRotatedFailed
	s.actorStatusReportTotal = actorStatusReportTotal
	s.actorStatusReportFailedTotal = actorStatusReportFailedTotal
	s.actorTableOperationRecvTotal = actorTableOperationRecvTotal
	s.actorRebalancedTotal = actorRebalancedTotal
	s.actorDeactivationTotal = actorDeactivationTotal
	s.actorDeactivationFailedTotal = actorDeactivationFailedTotal
	s.actorPendingCalls = actorPendingCalls
	s.actorReminders = actorReminders
	s.actorReminderFiredTotal = actorReminderFiredTotal
	s.actorTimers = actorTimers
	s.actorTimerFiredTotal = actorTimerFiredTotal
	s.appPolicyActionAllowed = appPolicyActionAllowed
	s.globalPolicyActionAllowed = globalPolicyActionAllowed
	s.appPolicyActionBlocked = appPolicyActionBlocked
	s.globalPolicyActionBlocked = globalPolicyActionBlocked
	s.serviceInvocationRequestSentTotal = serviceInvocationRequestSentTotal
	s.serviceInvocationRequestReceivedTotal = serviceInvocationRequestReceivedTotal
	s.serviceInvocationResponseSentTotal = serviceInvocationResponseSentTotal
	s.serviceInvocationResponseReceivedTotal = serviceInvocationResponseReceivedTotal
	s.serviceInvocationResponseReceivedLatency = serviceInvocationResponseReceivedLatency
	s.appID = appID
	s.enabled = true

	return nil
}

// ComponentLoaded records metric when component is loaded successfully.
func (s *serviceMetrics) ComponentLoaded() {
	if s.enabled {
		s.componentLoaded.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID)))
	}
}

// ComponentInitialized records metric when component is initialized.
func (s *serviceMetrics) ComponentInitialized(component string) {
	if s.enabled {
		s.componentInitCompleted.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(componentKey, component)))
	}
}

// ComponentInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) ComponentInitFailed(component string, reason string, name string) {
	if s.enabled {
		s.componentInitFailed.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, s.appID),
			attribute.String(componentKey, component),
			attribute.String(failReasonKey, reason),
			attribute.String(componentNameKey, name),
		))
	}
}

// MTLSInitCompleted records metric when component is initialized.
func (s *serviceMetrics) MTLSInitCompleted() {
	if s.enabled {
		s.mtlsInitCompleted.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID)))
	}
}

// MTLSInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	if s.enabled {
		s.mtlsInitFailed.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(failReasonKey, reason)))
	}
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded.
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	if s.enabled {
		s.mtlsWorkloadCertRotated.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID)))
	}
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed.
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	if s.enabled {
		s.mtlsWorkloadCertRotatedFailed.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(failReasonKey, reason)))
	}
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	if s.enabled {
		s.actorStatusReportTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(operationKey, operation)))
	}
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	if s.enabled {
		s.actorStatusReportFailedTotal.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, s.appID),
			attribute.String(operationKey, operation),
			attribute.String(failReasonKey, reason),
		))
	}
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	if s.enabled {
		s.actorTableOperationRecvTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(operationKey, operation)))
	}
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	if s.enabled {
		s.actorRebalancedTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	if s.enabled {
		s.actorDeactivationTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType string, reason string) {
	if s.enabled {
		s.actorDeactivationFailedTotal.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, s.appID),
			attribute.String(actorTypeKey, actorType),
			attribute.String(failReasonKey, reason),
		))
	}
}

// ActorReminderFired records metric when actor reminder is fired.
func (s *serviceMetrics) ActorReminderFired(actorType string, success bool) {
	if s.enabled {
		s.actorReminderFiredTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ActorTimerFired records metric when actor timer is fired.
func (s *serviceMetrics) ActorTimerFired(actorType string, success bool) {
	if s.enabled {
		s.actorTimerFiredTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ActorReminders records the current number of reminders for an actor type.
func (s *serviceMetrics) ActorReminders(actorType string, reminders int64) {
	if s.enabled {
		s.actorReminders.Add(s.ctx, reminders, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ActorTimers records the current number of timers for an actor type.
func (s *serviceMetrics) ActorTimers(actorType string, timers int64) {
	if s.enabled {
		s.actorTimers.Add(s.ctx, timers, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// ReportActorPendingCalls records the current pending actor locks.
func (s *serviceMetrics) ReportActorPendingCalls(actorType string, pendingLocks int32) {
	if s.enabled {
		s.actorPendingCalls.Add(s.ctx, int64(pendingLocks), metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(actorTypeKey, actorType)))
	}
}

// RequestAllowedByAppAction records the requests allowed due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestAllowedByAppAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		s.appPolicyActionAllowed.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, spiffeID.AppID()),
			attribute.String(trustDomainKey, spiffeID.TrustDomain().String()),
			attribute.String(namespaceKey, spiffeID.Namespace()),
		))
	}
}

// RequestBlockedByAppAction records the requests blocked due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestBlockedByAppAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		s.appPolicyActionBlocked.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, spiffeID.AppID()),
			attribute.String(trustDomainKey, spiffeID.TrustDomain().String()),
			attribute.String(namespaceKey, spiffeID.Namespace()),
		))
	}
}

// RequestAllowedByGlobalAction records the requests allowed due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestAllowedByGlobalAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		s.globalPolicyActionAllowed.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, spiffeID.AppID()),
			attribute.String(trustDomainKey, spiffeID.TrustDomain().String()),
			attribute.String(namespaceKey, spiffeID.Namespace()),
		))
	}
}

// RequestBlockedByGlobalAction records the requests blocked due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestBlockedByGlobalAction(spiffeID *spiffe.Parsed) {
	if s.enabled {
		s.globalPolicyActionBlocked.Add(s.ctx, 1, metric.WithAttributes(
			attribute.String(appIDKey, spiffeID.AppID()),
			attribute.String(trustDomainKey, spiffeID.TrustDomain().String()),
			attribute.String(namespaceKey, spiffeID.Namespace()),
		))
	}
}

// ServiceInvocationRequestSent records the number of service invocation requests sent.
func (s *serviceMetrics) ServiceInvocationRequestSent(destinationAppID string) {
	if s.enabled {
		s.serviceInvocationRequestSentTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(destinationAppIDKey, destinationAppID)))
	}
}

// ServiceInvocationRequestSent records the number of service invocation requests sent.
func (s *serviceMetrics) ServiceInvocationStreamingRequestSent(destinationAppID string) {
	if s.enabled {
		s.serviceInvocationRequestSentTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(destinationAppIDKey, destinationAppID), attribute.String(typeKey, typeStreaming)))
	}
}

// ServiceInvocationRequestReceived records the number of service invocation requests received.
func (s *serviceMetrics) ServiceInvocationRequestReceived(sourceAppID string) {
	if s.enabled {
		s.serviceInvocationRequestReceivedTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(sourceAppIDKey, sourceAppID)))
	}
}

// ServiceInvocationResponseSent records the number of service invocation responses sent.
func (s *serviceMetrics) ServiceInvocationResponseSent(destinationAppID string, status int32) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		s.serviceInvocationResponseSentTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(destinationAppIDKey, destinationAppID), attribute.String(statusKey, statusCode)))
	}
}

// ServiceInvocationResponseReceived records the number of service invocation responses received.
func (s *serviceMetrics) ServiceInvocationResponseReceived(sourceAppID string, status int32, start time.Time) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		s.serviceInvocationResponseReceivedTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(sourceAppIDKey, sourceAppID), attribute.String(statusKey, statusCode)))
		s.serviceInvocationResponseReceivedLatency.Record(s.ctx, ElapsedSince(start), metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(sourceAppIDKey, sourceAppID), attribute.String(statusKey, statusCode)))
	}
}

// ServiceInvocationStreamingResponseReceived records the number of service invocation responses received for streaming operations.
// this is mainly targeted to recording errors for proxying gRPC streaming calls
func (s *serviceMetrics) ServiceInvocationStreamingResponseReceived(sourceAppID string, status int32) {
	if s.enabled {
		statusCode := strconv.Itoa(int(status))
		s.serviceInvocationResponseReceivedTotal.Add(s.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, s.appID), attribute.String(sourceAppIDKey, sourceAppID), attribute.String(statusKey, statusCode), attribute.String(typeKey, typeStreaming)))
	}
}
