package diagnostics

import (
	"context"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"

	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

// serviceMetrics holds dapr runtime metric monitoring methods.
type serviceMetrics struct {
	// component metrics
	componentLoaded        syncint64.Counter
	componentInitCompleted syncint64.Counter
	componentInitFailed    syncint64.Counter

	// mTLS metrics
	mtlsInitCompleted             syncint64.Counter
	mtlsInitFailed                syncint64.Counter
	mtlsWorkloadCertRotated       syncint64.Counter
	mtlsWorkloadCertRotatedFailed syncint64.Counter

	// Access Control Lists for Service Invocation metrics
	appPolicyActionAllowed    syncint64.Counter
	globalPolicyActionAllowed syncint64.Counter
	appPolicyActionBlocked    syncint64.Counter
	globalPolicyActionBlocked syncint64.Counter

	actorStatusReportTotal       syncint64.Counter
	actorStatusReportFailedTotal syncint64.Counter
	actorTableOperationRecvTotal syncint64.Counter
	actorRebalancedTotal         syncint64.Counter
	actorDeactivationTotal       syncint64.Counter
	actorDeactivationFailedTotal syncint64.Counter
	actorPendingCalls            syncint64.Counter
}

// newServiceMetrics returns serviceMetrics instance with default service metric stats.
func (m *MetricClient) newServiceMetrics() *serviceMetrics {
	sm := new(serviceMetrics)
	// Runtime Component metrics
	sm.componentLoaded, _ = m.meter.SyncInt64().Counter(
		"runtime/component/loaded",
		instrument.WithDescription("The number of successfully loaded components."),
		instrument.WithUnit(unit.Dimensionless))
	sm.componentInitCompleted, _ = m.meter.SyncInt64().Counter(
		"runtime/component/init_total",
		instrument.WithDescription("The number of initialized components."),
		instrument.WithUnit(unit.Dimensionless))
	sm.componentInitFailed, _ = m.meter.SyncInt64().Counter(
		"runtime/component/init_fail_total",
		instrument.WithDescription("The number of component initialization failures."),
		instrument.WithUnit(unit.Dimensionless))
	// mTLS
	sm.mtlsInitCompleted, _ = m.meter.SyncInt64().Counter(
		"runtime/mtls/init_total",
		instrument.WithDescription("The number of successful mTLS authenticator initialization."),
		instrument.WithUnit(unit.Dimensionless))
	sm.mtlsInitFailed, _ = m.meter.SyncInt64().Counter(
		"runtime/mtls/init_fail_total",
		instrument.WithDescription("The number of mTLS authenticator init failures."),
		instrument.WithUnit(unit.Dimensionless))
	sm.mtlsWorkloadCertRotated, _ = m.meter.SyncInt64().Counter(
		"runtime/mtls/workload_cert_rotated_total",
		instrument.WithDescription("The number of the successful workload certificate rotations."),
		instrument.WithUnit(unit.Dimensionless))
	sm.mtlsWorkloadCertRotatedFailed, _ = m.meter.SyncInt64().Counter(
		"runtime/mtls/workload_cert_rotated_fail_total",
		instrument.WithDescription("The number of the failed workload certificate rotations."),
		instrument.WithUnit(unit.Dimensionless))
	// Access Control Lists for service invocation
	sm.appPolicyActionAllowed, _ = m.meter.SyncInt64().Counter(
		"runtime/acl/app_policy_action_allowed_total",
		instrument.WithDescription("The number of requests allowed by the app specific action specified in the access control policy."),
		instrument.WithUnit(unit.Dimensionless))
	sm.globalPolicyActionAllowed, _ = m.meter.SyncInt64().Counter(
		"runtime/acl/global_policy_action_allowed_total",
		instrument.WithDescription("The number of requests allowed by the global action specified in the access control policy."),
		instrument.WithUnit(unit.Dimensionless))
	sm.appPolicyActionBlocked, _ = m.meter.SyncInt64().Counter(
		"runtime/acl/app_policy_action_blocked_total",
		instrument.WithDescription("The number of requests blocked by the app specific action specified in the access control policy."),
		instrument.WithUnit(unit.Dimensionless))
	sm.globalPolicyActionBlocked, _ = m.meter.SyncInt64().Counter(
		"runtime/acl/global_policy_action_blocked_total",
		instrument.WithDescription("The number of requests blocked by the global action specified in the access control policy."),
		instrument.WithUnit(unit.Dimensionless))

	// Actor
	sm.actorStatusReportTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/status_report_total",
		instrument.WithDescription("The number of the successful status reports to placement service."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorStatusReportFailedTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/status_report_fail_total",
		instrument.WithDescription("The number of the failed status reports to placement service."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorTableOperationRecvTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/table_operation_recv_total",
		instrument.WithDescription("The number of the received actor placement table operations."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorRebalancedTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/rebalanced_total",
		instrument.WithDescription("The number of the actor rebalance requests."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorDeactivationTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/deactivated_total",
		instrument.WithDescription("The number of the successful actor deactivation."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorDeactivationFailedTotal, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/deactivated_failed_total",
		instrument.WithDescription("The number of the failed actor deactivation."),
		instrument.WithUnit(unit.Dimensionless))
	sm.actorPendingCalls, _ = m.meter.SyncInt64().Counter(
		"runtime/actor/pending_actor_calls",
		instrument.WithDescription("The number of pending actor calls waiting to acquire the per-actor lock."),
		instrument.WithUnit(unit.Dimensionless))

	return sm
}

// ComponentLoaded records metric when component is loaded successfully.
func (s *serviceMetrics) ComponentLoaded() {
	if s == nil {
		return
	}
	s.componentLoaded.Add(context.Background(), 1)
}

// ComponentInitialized records metric when component is initialized.
func (s *serviceMetrics) ComponentInitialized(component string) {
	if s == nil {
		return
	}
	s.componentInitCompleted.Add(context.Background(), 1,
		isemconv.ComponentNameKey.String(component),
	)
}

// ComponentInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) ComponentInitFailed(typ string, reason string, component string) {
	if s == nil {
		return
	}
	s.componentInitFailed.Add(context.Background(), 1,
		isemconv.ComponentTypeKey.String(typ),
		isemconv.ComponentNameKey.String(component),
		isemconv.FailReasonKey.String(reason),
	)
}

// MTLSInitCompleted records metric when component is initialized.
func (s *serviceMetrics) MTLSInitCompleted() {
	if s == nil {
		return
	}
	s.mtlsInitCompleted.Add(context.Background(), 1)
}

// MTLSInitFailed records metric when component initialization is failed.
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	if s == nil {
		return
	}
	s.mtlsInitFailed.Add(context.Background(), 1,
		isemconv.FailReasonKey.String(reason),
	)
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded.
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	if s == nil {
		return
	}
	s.mtlsWorkloadCertRotated.Add(context.Background(), 1)
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed.
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	if s == nil {
		return
	}
	s.mtlsWorkloadCertRotatedFailed.Add(context.Background(), 1,
		isemconv.FailReasonKey.String(reason),
	)
}

// RequestAllowedByAppAction records the requests allowed due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestAllowedByAppAction(trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s == nil {
		return
	}
	s.appPolicyActionAllowed.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
		semconv.HTTPMethodKey.String(httpverb),
		isemconv.PolicyActionKey.Bool(policyAction),
		isemconv.TrustDomainKey.String(trustDomain),
	)
}

// RequestBlockedByAppAction records the requests blocked due to a match with the action specified in the access control policy for the app.
func (s *serviceMetrics) RequestBlockedByAppAction(trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s == nil {
		return
	}
	s.appPolicyActionAllowed.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
		semconv.HTTPMethodKey.String(httpverb),
		isemconv.PolicyActionKey.Bool(policyAction),
		isemconv.TrustDomainKey.String(trustDomain),
	)
}

// RequestAllowedByGlobalAction records the requests allowed due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestAllowedByGlobalAction(trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s == nil {
		return
	}
	s.globalPolicyActionAllowed.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
		semconv.HTTPMethodKey.String(httpverb),
		isemconv.PolicyActionKey.Bool(policyAction),
		isemconv.TrustDomainKey.String(trustDomain),
	)
}

// RequestBlockedByGlobalAction records the requests blocked due to a match with the global action in the access control policy.
func (s *serviceMetrics) RequestBlockedByGlobalAction(trustDomain, namespace, operation, httpverb string, policyAction bool) {
	if s == nil {
		return
	}
	s.globalPolicyActionBlocked.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
		semconv.HTTPMethodKey.String(httpverb),
		isemconv.PolicyActionKey.Bool(policyAction),
		isemconv.TrustDomainKey.String(trustDomain),
	)
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	if s == nil {
		return
	}
	s.actorStatusReportFailedTotal.Add(context.Background(), 1,
		isemconv.FailReasonKey.String(reason),
		isemconv.ComponentOperationKey.String(operation),
	)
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	if s == nil {
		return
	}
	s.actorStatusReportTotal.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
	)
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	if s == nil {
		return
	}
	s.actorTableOperationRecvTotal.Add(context.Background(), 1,
		isemconv.ComponentOperationKey.String(operation),
	)
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	if s == nil {
		return
	}
	s.actorRebalancedTotal.Add(context.Background(), 1,
		isemconv.APIActorTypeKey.String(actorType))
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	if s == nil {
		return
	}
	s.actorDeactivationTotal.Add(context.Background(), 1,
		isemconv.APIActorTypeKey.String(actorType))
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType, reason string) {
	if s == nil {
		return
	}

	s.actorDeactivationFailedTotal.Add(context.Background(), 1,
		isemconv.FailReasonKey.String(reason),
		isemconv.APIActorTypeKey.String(actorType))
}

// ReportActorPendingCalls records the current pending actor locks.
func (s *serviceMetrics) ReportActorPendingCalls(actorType string, pendingLocks int32) {
	if s == nil {
		return
	}
	s.actorPendingCalls.Add(context.Background(), int64(pendingLocks),
		isemconv.APIActorTypeKey.String(actorType))
}
