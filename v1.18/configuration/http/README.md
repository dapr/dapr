**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestConfigurationGetHTTPPerformance | 9697.99 | 1 | 39 | 2 | 35 | 3232664.4 | 132479.0 |
| TestConfigurationSubscribeHTTPPerformance | 2074.52 | 1 | 73 | 57 | 87 | 35767.6 | 13307.4 |

### TestConfigurationGetHTTPPerformance

<img src="TestConfigurationGetHTTPPerformance_data_volume.png" alt="TestConfigurationGetHTTPPerformance_data_volume.png" />
<img src="TestConfigurationGetHTTPPerformance_duration_breakdown.png" alt="TestConfigurationGetHTTPPerformance_duration_breakdown.png" />
<img src="TestConfigurationGetHTTPPerformance_duration_low.png" alt="TestConfigurationGetHTTPPerformance_duration_low.png" />
<img src="TestConfigurationGetHTTPPerformance_resource_cpu.png" alt="TestConfigurationGetHTTPPerformance_resource_cpu.png" />
<img src="TestConfigurationGetHTTPPerformance_resource_memory.png" alt="TestConfigurationGetHTTPPerformance_resource_memory.png" />
<img src="TestConfigurationGetHTTPPerformance_summary.png" alt="TestConfigurationGetHTTPPerformance_summary.png" />
<img src="TestConfigurationGetHTTPPerformance_tail_latency.png" alt="TestConfigurationGetHTTPPerformance_tail_latency.png" />
<img src="TestConfigurationGetHTTPPerformance_throughput.png" alt="TestConfigurationGetHTTPPerformance_throughput.png" />

### TestConfigurationSubscribeHTTPPerformance

<img src="TestConfigurationSubscribeHTTPPerformance_data_volume.png" alt="TestConfigurationSubscribeHTTPPerformance_data_volume.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_duration_breakdown.png" alt="TestConfigurationSubscribeHTTPPerformance_duration_breakdown.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_duration_low.png" alt="TestConfigurationSubscribeHTTPPerformance_duration_low.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_resource_cpu.png" alt="TestConfigurationSubscribeHTTPPerformance_resource_cpu.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_resource_memory.png" alt="TestConfigurationSubscribeHTTPPerformance_resource_memory.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_summary.png" alt="TestConfigurationSubscribeHTTPPerformance_summary.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_tail_latency.png" alt="TestConfigurationSubscribeHTTPPerformance_tail_latency.png" />
<img src="TestConfigurationSubscribeHTTPPerformance_throughput.png" alt="TestConfigurationSubscribeHTTPPerformance_throughput.png" />
