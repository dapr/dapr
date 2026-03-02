## Highlights

Dapr Workflows in v1.17 demonstrate reliable execution across a wide range of concurrency levels and workflow patterns, with **100% success rate across all test configurations** and zero pod restarts.

**How to read these numbers:** "VUs" (virtual users) are concurrent workflow instances active at the same time. Duration percentiles (p50/p90/p95) describe how long individual workflow instances took to complete from start to finish — p50 is the typical workflow, p95 shows the slowest 5%. Throughput (workflows/sec) measures how many complete per second across the entire run.

**Parallel workflow execution** (110 VUs):
- 440 workflows completed at **97.91 workflows/sec**
- Median duration: **1.06 s** | p90: **1.27 s** | p95: **1.31 s** — tight, consistent distribution

With 110 workflows running simultaneously, each fanning out parallel activities, the typical end-to-end completion was 1.06 s. The tight p90–p95 spread (1.27 s to 1.31 s) shows that concurrency does not introduce meaningful tail latency — Dapr handles the parallel fan-out consistently.

**Series workflow execution** (350 VUs):
- 1,400 workflows completed at **57.43 workflows/sec**
- Median: **6.06 s** | p90: **6.19 s** | p95: **6.22 s** — extremely narrow latency band, indicating predictable execution time regardless of concurrency

Series workflows execute activities one after another, so each step adds to total duration. The extremely narrow p90–p95 band (6.19 s to 6.22 s) means execution time is highly predictable regardless of concurrency — 350 workflows running simultaneously produce nearly identical individual runtimes.

**Constant VU throughput** (30 VUs, averaged across 3 runs):
- ~300 workflows at **~28 workflows/sec**
- Median: **~1.04 s** | p95: **~1.18 s** — consistent results run-over-run

Three independent runs at 30 VUs produced nearly identical results, demonstrating consistent throughput with no degradation across repeated test iterations. This repeatability is important for production predictability.

**Scale test** (500 VUs, delay-based workflow):
- **10,000 workflows** completed at **35.45 workflows/sec**
- Median: **11.11 s** | p90: **19.97 s** | p95: **29.94 s**

At 500 simultaneous workflow instances, Dapr sustained 35.45 completions per second across 10,000 total runs. The wider p90–p95 spread at this scale reflects natural queuing — individual workflows may wait briefly before being scheduled — while still completing without a single failure.

**Multi-payload workflows** (30 VUs, 3 different payload sizes):
- ~300 workflows at **~8.92 workflows/sec** per run
- Median: **~3.27 s** | p95: **~3.67 s** — stable across payload variations

Across three different payload sizes, median and p95 latencies remained stable, confirming that varying message sizes do not meaningfully impact workflow execution time.

---

### TestDelayWorkflowsAtScale

<img src="TestDelayWorkflowsAtScale_data_volume.png" alt="TestDelayWorkflowsAtScale_data_volume.png" />
<img src="TestDelayWorkflowsAtScale_duration_breakdown.png" alt="TestDelayWorkflowsAtScale_duration_breakdown.png" />
<img src="TestDelayWorkflowsAtScale_duration_low.png" alt="TestDelayWorkflowsAtScale_duration_low.png" />
<img src="TestDelayWorkflowsAtScale_resource_cpu.png" alt="TestDelayWorkflowsAtScale_resource_cpu.png" />
<img src="TestDelayWorkflowsAtScale_resource_memory.png" alt="TestDelayWorkflowsAtScale_resource_memory.png" />
<img src="TestDelayWorkflowsAtScale_summary.png" alt="TestDelayWorkflowsAtScale_summary.png" />
<img src="TestDelayWorkflowsAtScale_tail_latency.png" alt="TestDelayWorkflowsAtScale_tail_latency.png" />
<img src="TestDelayWorkflowsAtScale_throughput.png" alt="TestDelayWorkflowsAtScale_throughput.png" />

### TestParallelWorkflowWithMaxVUs

<img src="TestParallelWorkflowWithMaxVUs_data_volume.png" alt="TestParallelWorkflowWithMaxVUs_data_volume.png" />
<img src="TestParallelWorkflowWithMaxVUs_duration_breakdown.png" alt="TestParallelWorkflowWithMaxVUs_duration_breakdown.png" />
<img src="TestParallelWorkflowWithMaxVUs_duration_low.png" alt="TestParallelWorkflowWithMaxVUs_duration_low.png" />
<img src="TestParallelWorkflowWithMaxVUs_resource_cpu.png" alt="TestParallelWorkflowWithMaxVUs_resource_cpu.png" />
<img src="TestParallelWorkflowWithMaxVUs_resource_memory.png" alt="TestParallelWorkflowWithMaxVUs_resource_memory.png" />
<img src="TestParallelWorkflowWithMaxVUs_summary.png" alt="TestParallelWorkflowWithMaxVUs_summary.png" />
<img src="TestParallelWorkflowWithMaxVUs_tail_latency.png" alt="TestParallelWorkflowWithMaxVUs_tail_latency.png" />
<img src="TestParallelWorkflowWithMaxVUs_throughput.png" alt="TestParallelWorkflowWithMaxVUs_throughput.png" />

### TestSeriesWorkflowWithMaxVUs

<img src="TestSeriesWorkflowWithMaxVUs_data_volume.png" alt="TestSeriesWorkflowWithMaxVUs_data_volume.png" />
<img src="TestSeriesWorkflowWithMaxVUs_duration_breakdown.png" alt="TestSeriesWorkflowWithMaxVUs_duration_breakdown.png" />
<img src="TestSeriesWorkflowWithMaxVUs_duration_low.png" alt="TestSeriesWorkflowWithMaxVUs_duration_low.png" />
<img src="TestSeriesWorkflowWithMaxVUs_resource_cpu.png" alt="TestSeriesWorkflowWithMaxVUs_resource_cpu.png" />
<img src="TestSeriesWorkflowWithMaxVUs_resource_memory.png" alt="TestSeriesWorkflowWithMaxVUs_resource_memory.png" />
<img src="TestSeriesWorkflowWithMaxVUs_summary.png" alt="TestSeriesWorkflowWithMaxVUs_summary.png" />
<img src="TestSeriesWorkflowWithMaxVUs_tail_latency.png" alt="TestSeriesWorkflowWithMaxVUs_tail_latency.png" />
<img src="TestSeriesWorkflowWithMaxVUs_throughput.png" alt="TestSeriesWorkflowWithMaxVUs_throughput.png" />

### TestWorkflowWithConstantIterations

<img src="TestWorkflowWithConstantIterations_avg_data_volume.png" alt="TestWorkflowWithConstantIterations_avg_data_volume.png" />
<img src="TestWorkflowWithConstantIterations_avg_duration_breakdown.png" alt="TestWorkflowWithConstantIterations_avg_duration_breakdown.png" />
<img src="TestWorkflowWithConstantIterations_avg_duration_comparison.png" alt="TestWorkflowWithConstantIterations_avg_duration_comparison.png" />
<img src="TestWorkflowWithConstantIterations_avg_duration_low.png" alt="TestWorkflowWithConstantIterations_avg_duration_low.png" />
<img src="TestWorkflowWithConstantIterations_avg_resource_cpu.png" alt="TestWorkflowWithConstantIterations_avg_resource_cpu.png" />
<img src="TestWorkflowWithConstantIterations_avg_resource_memory.png" alt="TestWorkflowWithConstantIterations_avg_resource_memory.png" />
<img src="TestWorkflowWithConstantIterations_avg_summary.png" alt="TestWorkflowWithConstantIterations_avg_summary.png" />
<img src="TestWorkflowWithConstantIterations_avg_tail_latency.png" alt="TestWorkflowWithConstantIterations_avg_tail_latency.png" />
<img src="TestWorkflowWithConstantIterations_avg_throughput.png" alt="TestWorkflowWithConstantIterations_avg_throughput.png" />

### TestWorkflowWithConstantVUs

<img src="TestWorkflowWithConstantVUs_avg_data_volume.png" alt="TestWorkflowWithConstantVUs_avg_data_volume.png" />
<img src="TestWorkflowWithConstantVUs_avg_duration_breakdown.png" alt="TestWorkflowWithConstantVUs_avg_duration_breakdown.png" />
<img src="TestWorkflowWithConstantVUs_avg_duration_comparison.png" alt="TestWorkflowWithConstantVUs_avg_duration_comparison.png" />
<img src="TestWorkflowWithConstantVUs_avg_duration_low.png" alt="TestWorkflowWithConstantVUs_avg_duration_low.png" />
<img src="TestWorkflowWithConstantVUs_avg_resource_cpu.png" alt="TestWorkflowWithConstantVUs_avg_resource_cpu.png" />
<img src="TestWorkflowWithConstantVUs_avg_resource_memory.png" alt="TestWorkflowWithConstantVUs_avg_resource_memory.png" />
<img src="TestWorkflowWithConstantVUs_avg_summary.png" alt="TestWorkflowWithConstantVUs_avg_summary.png" />
<img src="TestWorkflowWithConstantVUs_avg_tail_latency.png" alt="TestWorkflowWithConstantVUs_avg_tail_latency.png" />
<img src="TestWorkflowWithConstantVUs_avg_throughput.png" alt="TestWorkflowWithConstantVUs_avg_throughput.png" />

### TestWorkflowWithDifferentPayloads

<img src="TestWorkflowWithDifferentPayloads_avg_data_volume.png" alt="TestWorkflowWithDifferentPayloads_avg_data_volume.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_duration_breakdown.png" alt="TestWorkflowWithDifferentPayloads_avg_duration_breakdown.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_duration_comparison.png" alt="TestWorkflowWithDifferentPayloads_avg_duration_comparison.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_duration_low.png" alt="TestWorkflowWithDifferentPayloads_avg_duration_low.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_resource_cpu.png" alt="TestWorkflowWithDifferentPayloads_avg_resource_cpu.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_resource_memory.png" alt="TestWorkflowWithDifferentPayloads_avg_resource_memory.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_summary.png" alt="TestWorkflowWithDifferentPayloads_avg_summary.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_tail_latency.png" alt="TestWorkflowWithDifferentPayloads_avg_tail_latency.png" />
<img src="TestWorkflowWithDifferentPayloads_avg_throughput.png" alt="TestWorkflowWithDifferentPayloads_avg_throughput.png" />
