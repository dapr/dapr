# Dapr 1.18.1

This update contains the following bug fixes:
- [Workflow event-timer reminders leak when the same event name is awaited repeatedly](#workflow-event-timer-reminders-leak-when-the-same-event-name-is-awaited-repeatedly)
- [Workflow sidecars become permanently unavailable after a configuration reload](#workflow-sidecars-become-permanently-unavailable-after-a-configuration-reload)
- [Sidecars restart unnecessarily on operator configuration resyncs](#sidecars-restart-unnecessarily-on-operator-configuration-resyncs)
- [Workflows hang in RUNNING when a child workflow completion crosses a ContinueAsNew boundary](#workflows-hang-in-running-when-a-child-workflow-completion-crosses-a-continueasnew-boundary)
- [Workflow concurrency limits are silently ignored when Dapr is installed via the Helm chart](#workflow-concurrency-limits-are-silently-ignored-when-dapr-is-installed-via-the-helm-chart)

## Workflow event-timer reminders leak when the same event name is awaited repeatedly

### Problem

When a workflow waits on an external event and the event arrives before the wait's timeout timer fires, the timer's reminder is supposed to be deleted, since it no longer serves a purpose.
To find which reminder to delete, the runtime paired each newly arrived event against the oldest unfired event timer of the same name found anywhere in the workflow's history.
A timer cancelled in a previous run is indistinguishable in history from a pending one — its `TimerCreated` is persisted and its `TimerFired` never arrives — so for workflows that wait on the same event name repeatedly (for example agent-style loops of `waitForExternalEvent` plus an activity per turn), those dead timers sat at the head of the per-name FIFO forever.
Every new event re-deleted the first turn's long-gone timer reminder while the reminder of the timer actually cancelled that turn was never deleted.

### Impact

Any workflow that awaits the same external event name more than once is affected.

Visible symptoms include:
- One leaked timer reminder in the scheduler per satisfied wait after the first. For indefinite waits (no timeout) these are far-future one-shots that never fire and only get cleaned up by the reminder sweep at workflow completion; long-running instances accumulate them for their entire lifetime.
- For finite timeouts, the leaked reminder eventually fires, causing a spurious workflow wake-up and a stray `TimerFired` event that the SDK then discards as unexpected.
- Debug logs showing the same `deleting cancelled event timer reminder 'timer-N'` line (always the oldest dead timer) on every turn.

### Root Cause

`deleteCancelledEventTimers` built the set of unfired event timers from the full history but only consumed entries for events arriving in the current run.
Events already in history — which had performed their cancellations in previous runs — never advanced the FIFO, so previously cancelled timers permanently shadowed the timer each new event had actually cancelled.

### Solution

The event-to-timer pairing is now replayed deterministically over the full history in chronological order: each `EventRaised` consumes the oldest unfired event timer of the same name created before it.
Pairings driven by events already in history reproduce the cancellations performed by previous runs, and only timers consumed by the current run's new events trigger reminder deletions.
The created-before constraint additionally guarantees that a timer guarding a still-armed wait is never deleted ahead of its own event, which the previous pairing could do when an event and the next wait's timer were persisted in the same run.
Timers with non-event origins (`CreateTimer`, `ActivityRetry`, `ChildWorkflowRetry`) remain excluded from the pairing regardless of name collisions.

## Workflow sidecars become permanently unavailable after a configuration reload

### Problem

A Dapr sidecar that hosts workflows could become permanently unavailable after a configuration hot-reload (SIGHUP).
Once the sidecar entered this state, every workflow operation failed and the only way to recover was to restart the pod.

### Impact

After any configuration reload, an affected sidecar kept running but never came back to a usable state.

Visible symptoms include:
- The sidecar health endpoint reports `NotReady` indefinitely.
- The Dapr gRPC API (port `50001`) stops accepting connections, so workflow calls fail with errors such as `14 UNAVAILABLE: No connection established. Last error: connect ECONNREFUSED 127.0.0.1:50001`.
- Sidecar logs show the workflow engine stopping during the reload but never logging it starting again (the `API gRPC server is running on port 50001` and `Registering workflow engine for gRPC endpoint` lines never reappear).
- The condition persists until the pod is manually restarted.

### Root Cause

A configuration reload restarts the Dapr runtime, and the new runtime cannot start until the previous one has fully shut down.
Shutdown waits for in-flight gRPC calls to drain gracefully, but a connected workflow worker holds a long-lived `GetWorkItems` streaming connection.
The workflow engine never signalled that stream to close, so the graceful shutdown blocked on it and the runtime never finished restarting, leaving the API server and workflow engine down.
With workflow SDK clients that automatically reconnect, the stream was continually re-established, so the sidecar stayed stuck for as long as a worker was connected.

### Solution

The workflow engine now signals all connected `GetWorkItems` streams to close as part of shutdown, before draining the worker, so graceful shutdown completes promptly instead of blocking on those streams.
After a configuration reload the runtime restarts cleanly: the sidecar returns to `Ready`, the gRPC API rebinds on port `50001`, and connected workflow clients reconnect automatically with no manual intervention.
In-flight work items are not lost; undelivered items are re-dispatched from the durable actor backend after the restart.

## Sidecars restart unnecessarily on operator configuration resyncs

### Problem

In Kubernetes mode, a Dapr sidecar would perform a full runtime restart (SIGHUP) in response to operator events that carried no actual change.

### Impact

Long-running sidecars restarted their runtime periodically with no configuration change and no human action.

### Root Cause

The operator's informer streams resource events for the whole namespace to every connected sidecar.
The Kubernetes informer periodically resyncs, re-delivering every cached object as an `UPDATED` event with an unchanged `resourceVersion`, and also re-delivers on reconnect.
The operator forwarded these no-op replays verbatim, and the sidecar's SIGHUP reconciler restarts the runtime.

### Solution

The operator now drops informer events whose `resourceVersion` is unchanged before streaming them to sidecars.
A genuine change always advances the `resourceVersion`, so real updates are still delivered and hot reload continues to work; only the no-op resync and reconnect replays are suppressed.
Sidecars no longer restart on routine operator resyncs.

## Workflows hang in RUNNING when a child workflow completion crosses a ContinueAsNew boundary

### Problem

A workflow that uses ContinueAsNew and child workflows (or activities) can permanently hang in the RUNNING state.
A child workflow or activity visibly completes on the application side, but the parent never observes the completion and waits forever. The daprd log shows the workflow execution returning `ORCHESTRATION_STATUS_RUNNING` followed later by:

```
Workflow actor '<id>': dropping duplicate completion event already present in history/inbox
Workflow actor '<id>': ignoring run request for reminder 'new-event-...' because the workflow inbox is empty
```

### Impact

Affected workflows freeze indefinitely and never complete, fail, or time out.
The failure is timing dependent and intermittent: it occurs when a child workflow or activity started before a ContinueAsNew completes after it.
Agentic workflows are particularly exposed, as they commonly loop via ContinueAsNew while running child workflows and activities with variable latency.

### Root Cause

ContinueAsNew resets the workflow's task ID sequence.
When a child workflow or activity abandoned by a previous generation completes after the ContinueAsNew, its completion event carries a task ID from the old sequence.
The new generation has no matching scheduled operation, so the workflow execution consumes the event without effect and it is persisted into history as an orphan.

When the new generation later schedules its own operation, that operation legitimately reuses the same task ID.
Its real completion event is then matched against the orphaned event in history by the completion deduplication added in v1.18.0-rc.3 (intended to drop redelivered completions), and is discarded as a duplicate.
The sender is acknowledged, so the event is never redelivered, and the re-asserted wake-up reminder fires against an empty inbox and is deleted.
The workflow is left RUNNING with nothing to drive it.

### Solution

The workflow actor no longer persists resolution events (task or child workflow completions and failures) that match no operation scheduled in history.
Stale cross-generation completions are discarded with a warning at the point they are consumed instead of being written into history, so the completion deduplication can no longer mistake a later, legitimate completion for a duplicate. Completions for operations of the current generation are unaffected.

## Workflow concurrency limits are silently ignored when Dapr is installed via the Helm chart

### Problem

The workflow concurrency limits introduced in 1.18.0 (`globalMaxConcurrentWorkflowInvocations`, `globalMaxConcurrentActivityInvocations`, `workflowConcurrencyLimits`, and `activityConcurrencyLimits` under `spec.workflow` of a `Configuration` resource) were added to the API types but the matching `Configuration` CRD bundled in the Helm chart was never regenerated.
The chart CRD's OpenAPI schema therefore did not know about these fields.

### Impact

You were affected if both of the following were true:
- You installed the Dapr control plane via the Helm chart (which applies the CRDs from `charts/dapr/crds`) rather than via the CLI.
- You set any of the global or per-name workflow/activity concurrency limits on a `Configuration` resource.

### Root Cause

The CRDs shipped in the Helm chart are generated from the Go API types under `pkg/apis` with `controller-gen`, but the generated output had not been copied into `charts/dapr/crds/configuration.yaml` after the concurrency-limit fields were added. The bundled CRD drifted out of sync with the API types.

### Solution

The chart `Configuration` CRD was regenerated so its schema includes the concurrency-limit fields (and the `NamedConcurrencyLimit` schema they reference).
