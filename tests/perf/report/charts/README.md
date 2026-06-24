## Performance charts

This folder contains charts generated from k6 JSON output for the performance
tests. Today, the charts are only generated for the Workflows API based on the
[performance tests here](https://github.com/dapr/dapr/tree/master/tests/perf/workflows), 
but will be expanded upon in the future. See the folder name for performance charts
per Dapr version and API.

### How test runs are grouped and averaged

The perf tests run various tests, including running the same scenario multiple 
times using subtests (for workflows), for example:

- `TestWorkflowWithConstantVUs/[T_30_300]:_`
- `TestWorkflowWithConstantVUs/[T_30_300]:_#01`
- `TestWorkflowWithConstantVUs/[T_30_300]:_#02`

Other scenarios may only run once.

`charts.go`:

- Normalizes these subtest names by stripping the `:_` / `:_#NN` suffix so
  all runs of the same logical test share a single key such as
  `TestWorkflowWithConstantVUs_T_30_300`.
- Collects all k6 summaries for that logical test.
- If there is more than one run, it:
  - Builds an aggregated runner (per-metric averages across all runs)
  - Generates one averaged chart set with an `_avg` suffix in the filename
  - Generates one comparison chart (`*_duration_comparison.png`) showing
    how the individual runs differ in latency
- If there is only one run, it:
  - Generates a single chart set without `_avg` (the metrics are just that run).

Examples (for `WorkflowsWithConstantVUs` `[T_30_300]` when there are multiple runs):

- `TestWorkflowWithConstantVUs_T_30_300_avg_duration_breakdown.png`
- `TestWorkflowWithConstantVUs_T_30_300_avg_duration_low.png`
- `TestWorkflowWithConstantVUs_T_30_300_avg_summary.png`
- `TestWorkflowWithConstantVUs_T_30_300_avg_throughput.png`
- `TestWorkflowWithConstantVUs_T_30_300_avg_data_volume.png`
- `TestWorkflowWithConstantVUs_T_30_300_duration_comparison.png`

---

### Workflows API perf tests

The current charts are built from the Workflows perf suite in
`tests/perf/workflows` (implemented in `workflow_test.go`).

For a detailed description of each scenario (test names, workflows, VUs,
iterations, payload sizes, and how the app/sidecar resources are measured),
see the [Workflows perf README](../../workflows/README.md).

### Chart types

For each test you’ll see the following averaged charts:

#### 1. Duration breakdown (full + low latency)

- Files:
  - `*_avg_duration_breakdown.png`
  - `*_avg_duration_low.png`
- X-axis: percentiles `min, med, avg, p90, p95, max`
- Y-axis: seconds
- Lines (from k6 metrics, all in seconds):
  - `http_req_connecting`
  - `http_req_tls_handshaking`
  - `http_req_sending`
  - `http_req_receiving`
  - `http_req_blocked`
  - `http_req_waiting`
  - `http_req_duration`
  - `iteration_duration`
  - `http_req_failed`

The full-range chart shows the whole picture, including the long
`http_req_waiting` and `iteration_duration` times (for delayed workflows).
Whereas, the low-latency chart zooms into a dynamic 0–N second window to 
make the shorter phases metrics viewable.

Interpretation:

- Compare how much time is spent waiting on the workflow runtime versus
  networking overheads (connect/TLS/send/receive).
- See how the tail percentiles (p90/p95) behave relative to the median.

#### 2. Performance summary

- File: `*_avg_summary.png`
- Bars:
  - Success (%) – `checks.rate * 100`
  - Failed (%) – `100 - Success`
  - VUs – `vus_max.values.max`

Interpretation:

- Did this scenario pass? & How many virtual users did we need to drive the test?

#### 3. Throughput

- File: `*_avg_throughput.png`\
- Iterations/sec in title
- Bars:
  - Data received (KB/s) – `data_received.values.rate / 1024`
  - Data sent (KB/s) – `data_sent.values.rate / 1024`

Interpretation:

- Overall throughput for that test (how many workflow executions per second)
- Network throughput required to support that load

#### 4. Data volume

- File: `*_avg_data_volume.png`
- Units auto-scale by total size per chart:
  - < 1 MB: shown in KB
  - ≥ 1 GB: shown in GB
  - else: shown in MB
- Bars:
  - Received (chosen unit)
  - Sent (chosen unit)
- Y-axis:
  - Labeled with the chosen unit (KB/MB/GB)
  - Integer tick labels for readability

Interpretation:

- Total bytes sent/received over the lifetime of the test, useful when
  comparing scenarios with different payload sizes or durations

#### 5. Duration comparison across runs

- File: `*_duration_comparison.png`
- X-axis: Run 1, Run 2, Run 3, ...
- Y-axis: Latency (ms)
- Lines per run, from `http_req_duration.values`:
  - p50 (median) – typical request latency
  - p95 – tail latency (95% of requests complete in <= this time)

Interpretation:

- Shows run stability:
  - Flat lines → consistent performance
  - Spikes or trends → variance between runs

This chart focuses on core latency p50 (median) and tail latency (p95).

---

### Future work:

This will be extended to other APIs in the future.

### Where the data and charts live

The source of truth for perf results is the `gotestsum` JSON report produced
by a perf CI run, not the rendered PNGs. Per released version:

- The compressed report is committed to this repo under
  `tests/perf/report/data/<version>/test_report_perf.json.gz` (a few hundred KB).
- The rendered charts are published as assets on the matching GitHub release
  (`perf-charts-<version>.zip`), via the `dapr-perf-publish-charts` workflow.

Full chart sets should not be committed for new versions: a full set is
several MB of immutable binary per release, and PNGs cannot be regenerated or
corrected later, whereas the JSON report can re-render charts at any time with
current chart code. The chart folders already in this directory predate this
policy and are kept for reference.

### How charts are generated in CI

- Every `dapr-perf` run generates charts from its own report and uploads them
  as the `perf_charts` workflow artifact (90 day retention).
- For a release, trigger the `dapr-perf-publish-charts` workflow with the
  `dapr-perf` run ID and the release tag. It downloads the run's report,
  renders the charts, and uploads both the charts zip and the compressed
  report to the GitHub release. Then commit the compressed report under
  `data/<version>/` in a PR.

### How to run locally

Charts are produced by `charts.go` (from `tests/perf/report/`). It reads a
`gotestsum` JSON report (plain or gzipped) and writes PNGs to
`charts/<version>/<api>/`:

```bash
cd tests/perf/report
go run . -input data/v1.18.0/test_report_perf.json.gz -version v1.18.0
```

Flags:

- `-input`: path to the report, `.gz` is decompressed transparently
  (default `./test_report_perf.json`)
- `-version`: output subdirectory under `charts/` (default `master`)
- `-infra`: description of the infrastructure the run executed on, shown at
  the top of generated READMEs. CI passes the AKS node pool description from
  `tests/test-infra/perf-infra-description.txt` (kept in sync with the node
  pool in `tests/test-infra/azure-aks.bicep`); set it to your own cluster spec
  for local runs so results stay comparable.

Each generated per-API README starts with a "Throughput per resource" table:
iterations/sec for each scenario alongside the app and sidecar CPU/memory it
consumed, plus derived iterations/sec per CPU core and per GB of memory
(app + sidecar combined). This is the headline efficiency metric for
comparing scenarios and versions on the same infrastructure.

Set `CHARTS_DEBUG=1` for verbose output on how tests are classified.