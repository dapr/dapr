**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestDelayWorkflowsAtScale | 35.58 | 402 | 939 | 162 | 101 | 63.1 | 35.0 |
| TestParallelWorkflowWithMaxVUs | 110.24 | 14 | 608 | 2 | 36 | 6890.0 | 175.3 |
| TestSeriesWorkflowWithMaxVUs | 92.97 | 13 | 610 | 2 | 39 | 6197.7 | 146.7 |
| TestWorkflowWithConstantIterations | 62.62 | 14 | 608 | 2 | 37 | 3834.1 | 99.5 |
| TestWorkflowWithConstantVUs | 53.41 | 58 | 626 | 3 | 49 | 875.6 | 81.0 |
| TestWorkflowWithDifferentPayloads | 9.29 | 19 | 616 | 2 | 35 | 442.6 | 14.6 |

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
