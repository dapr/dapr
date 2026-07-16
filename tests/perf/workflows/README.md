# Dapr Workflows Performance Tests

This project aims to test the performance of Dapr workflows under a few common
patterns (constant VUs, constant iterations, max VUs, delayed workflows, and 
varying payload sizes). The tests are implemented in `workflow_test.go` and 
driven by k6.

## Glossary

- VU (Virtual User): The number of concurrent workflows running at a time
- Iterations: Total number of workflow runs
- Req_Duration: Time taken to complete a workflow run
- Sidecar: Dapr Sidecar

## Test plan

For these tests, a single instance of the `perf-workflowsapp` application with 
its Dapr sidecar is deployed on a Kubernetes cluster. Resource requests/limits
for the app and sidecar (CPU and memory) are configured in `workflow_test.go`
via the `App*` and `Dapr*` fields of the `AppDescription`.

All scenarios:

- Use k6 to send HTTP requests against the workflow app
- Initialize the workflow runtime before starting the load
- Collect k6 metrics plus app/sidecar resource usage and restart counts into a
  summary table where we then chart the metrics in `tests/perf/report/charts/v1.16.3/workflows/`

### TestWorkflowWithConstantVUs

Workflow Description

* Contains 5 chained activities ran in the `sum_series_wf` workflow
* Each activity performs numeric calculation and the result is sent back to the workflow

Scenario:

- Uses a single scenario `t_30_300` (30 VUs and 300 total
  iterations) and runs it 3 times without restarting the app:
    - Inputs: `["100"]`
    - Scenarios: `["t_30_300", "t_30_300", "t_30_300"]`
- After each run, the test records:
    - k6 latency and throughput metrics
    - App and sidecar CPU / memory usage
    - Total restart count

### TestWorkflowWithConstantIterations

Workflow Description

* Contains 5 chained activities ran in the `sum_series_wf` workflow
* Each activity performs numeric calculation and the result is sent back to the workflow

Scenario:

- Keeps the total iterations constant (300) while increasing the maximum VUs:
  - Inputs: `["100"]`
  - Scenarios: `["t_30_300", "t_60_300", "t_90_300"]`
- Before each subtest run, the app is restarted to clear state:
  - This isolates how scaling the VUs affects latency and resource usage for
    the same total amount of work

### TestSeriesWorkflowWithMaxVUs

Workflow Description

* Contains 5 chained activities ran in the `sum_series_wf` workflow with a higher VU count to stress the series workflow
* Each activity performs numeric calculation and the result is sent back to the workflow

Scenario:

- Single scenario with high concurrency and more iterations:
  - Inputs: `["100"]`
  - Scenarios: `["t_350_1400"]` (350 VUs and 1400 iterations)
- Verifies all workflow instances complete successfully and records app/sidecar
  resource usage and latency metrics

### TestParallelWorkflowWithMaxVUs

Workflow Description

* Runs `sum_parallel_wf`, which contains 5 activities ran in parallel whose
  results are aggregated by the workflow

Scenario:

- Single high-concurrency scenario:
  - Inputs: `["100"]`
  - Scenarios: `["t_110_440"]` (110 VUs and 440 iterations)
- Focuses on fan-out/fan-in behavior under load, again recording k6 metrics and
  resource usage

### TestWorkflowWithDifferentPayloads

Workflow Description

* Runs `state_wf`, which performs state operations:
  * Activity 1 saves data of a given size in the configured state store
  * Activity 2 reads the saved data back
  * Activity 3 deletes the data
  * The workflow takes the data size as input and passes a string of that size as the payload to the activity

Scenario:

- Keeps the scenario constant while varying payload size:
  - Scenarios: `["t_30_300"]` for all runs
  - Inputs: `["10000", "50000", "100000"]` bytes
- For each payload size, the test:
  - Runs the same k6 scenario
  - Records latency, throughput, and app/sidecar resource usage
  - Outputs the effective payload size to the summary table
  - Restarts the application between runs