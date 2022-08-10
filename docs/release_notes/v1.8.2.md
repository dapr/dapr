# Dapr 1.8.2

### Fixes ineffective resiliency policies and panics in service and actor invocation

#### Problem

Dapr 1.8.0 introduced a regression in the direct messaging package, used internally within Dapr to invoke other services and actors, which adversely impacted the operation of resiliency and could, in some situations, cause `daprd` to panic.

### Impact

This issue impacts all users that have enabled the `Resiliency` preview feature in Dapr 1.8.0–1.8.1, even when no resiliency policies are configured.

#### Root cause

Because of a mistaken import statement, an old version of a dependency was used in two occurrences, including in the direct messaging package.

In the most critical scenario, when service or actor invocation was used and Dapr had the `Resiliency` feature enabled (even if no resiliency policies were configured), errors that should have been permanent (ie. that should not have caused Dapr to retry the operation) were instead considered transient. This had multiple consequences including having Dapr retry operations that should not have been retried and, in some occurrences, could have caused `daprd` to panic.

#### Solution

The incorrect import statements have been fixed, so Dapr now behaves as expected (and does not panic) when it encounters a non-retriable error in service and actor invocation.
