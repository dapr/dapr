## Highlights

**HTTP service invocation** (1 KB payload, 16 connections, 1,000 QPS):
- Median (p50): **1.59 ms** | p90: **2.38 ms** | p99: **3.89 ms** | p99.9: **6.61 ms**
- Dapr overhead vs. direct: **+1.06 ms** at p50, **+1.48 ms** at p90, **+2.90 ms** at p99
- 60,000 requests — **100% success rate** (all HTTP 200), 0 pod restarts
- Sidecar: 202 mCPU, 51 MB memory at sustained 1,000 QPS

The 1.59 ms median is the complete HTTP request cycle through Dapr: client to sidecar, sidecar to app, app response, and sidecar back to client. Dapr adds 1.06 ms at the median — the cost of HTTP header parsing, routing, and forwarding. The p99 of 3.89 ms represents the worst 1 in 100 requests, still well under 4 ms. Even at the extreme p99.9 (1 in 1,000 requests), latency stays below 7 ms. The sidecar uses 202 mCPU and 51 MB — a light resource footprint at 1,000 sustained QPS.

---

### TestServiceInvocationHTTPPerformance

<img src="TestServiceInvocationHTTPPerformance_connection_stats.png" alt="TestServiceInvocationHTTPPerformance_connection_stats.png" />
<img src="TestServiceInvocationHTTPPerformance_data_volume.png" alt="TestServiceInvocationHTTPPerformance_data_volume.png" />
<img src="TestServiceInvocationHTTPPerformance_duration_breakdown.png" alt="TestServiceInvocationHTTPPerformance_duration_breakdown.png" />
<img src="TestServiceInvocationHTTPPerformance_duration_requested_vs_actual.png" alt="TestServiceInvocationHTTPPerformance_duration_requested_vs_actual.png" />
<img src="TestServiceInvocationHTTPPerformance_header_size.png" alt="TestServiceInvocationHTTPPerformance_header_size.png" />
<img src="TestServiceInvocationHTTPPerformance_histogram_count.png" alt="TestServiceInvocationHTTPPerformance_histogram_count.png" />
<img src="TestServiceInvocationHTTPPerformance_histogram_percent.png" alt="TestServiceInvocationHTTPPerformance_histogram_percent.png" />
<img src="TestServiceInvocationHTTPPerformance_payload_size.png" alt="TestServiceInvocationHTTPPerformance_payload_size.png" />
<img src="TestServiceInvocationHTTPPerformance_qps.png" alt="TestServiceInvocationHTTPPerformance_qps.png" />
<img src="TestServiceInvocationHTTPPerformance_resource_cpu.png" alt="TestServiceInvocationHTTPPerformance_resource_cpu.png" />
<img src="TestServiceInvocationHTTPPerformance_resource_memory.png" alt="TestServiceInvocationHTTPPerformance_resource_memory.png" />
<img src="TestServiceInvocationHTTPPerformance_summary.png" alt="TestServiceInvocationHTTPPerformance_summary.png" />
<img src="TestServiceInvocationHTTPPerformance_tail_latency.png" alt="TestServiceInvocationHTTPPerformance_tail_latency.png" />
<img src="TestServiceInvocationHTTPPerformance_throughput.png" alt="TestServiceInvocationHTTPPerformance_throughput.png" />
