## Highlights

State management in v1.17 delivers sub-millisecond median latency on both transports at 1,000 QPS sustained load — among the lowest overhead of any Dapr API surface.

**How to read these numbers:** Latency percentiles (p50/p90/p99/p99.9) show the distribution of individual request times across 60,000 requests — p50 is what most users experience, p99.9 is the extreme tail (1 in 1,000 requests). "Dapr overhead" is measured by comparing the Dapr-proxied result to a direct call to the state store with no sidecar involved.

**gRPC** (16 connections, 1,000 QPS target):
- Median (p50): **0.57 ms** | p90: **0.93 ms** | p99: **1.65 ms** | p99.9: **2.66 ms**
- Dapr adds just **+0.12 ms** at p50 vs. direct store access
- 60,000 requests — **100% success rate**, 0 pod restarts

Sub-millisecond median at 1,000 QPS means state reads through Dapr are effectively instant in any real-world context. The sidecar added just 0.12 ms on top of going directly to the state store — Dapr's proxy overhead here is negligible. Even at the 99.9th percentile (the worst 1 in 1,000 requests), latency stays under 3 ms.

**HTTP** (16 connections, 1,000 QPS target):
- Median (p50): **0.73 ms** | p90: **1.60 ms** | p99: **1.97 ms** | p99.9: **2.84 ms**
- Dapr adds just **+0.28 ms** at p50 vs. direct store access
- 60,000 requests — **100% success rate**, 0 pod restarts

HTTP state access is similarly fast with a median under 1 ms. The slightly higher overhead (0.28 ms vs. 0.12 ms for gRPC) reflects the additional work of HTTP/1.1 header parsing compared to gRPC's binary framing — both remain well within sub-millisecond territory for the typical request.

Both transports hit their QPS targets precisely (999.97–999.98 actual vs. 1,000 requested).

---

## HTTP

### TestStateGetHTTPPerformance

<img src="http/TestStateGetHTTPPerformance_connection_stats.png" alt="TestStateGetHTTPPerformance_connection_stats.png" />
<img src="http/TestStateGetHTTPPerformance_data_volume.png" alt="TestStateGetHTTPPerformance_data_volume.png" />
<img src="http/TestStateGetHTTPPerformance_duration_breakdown.png" alt="TestStateGetHTTPPerformance_duration_breakdown.png" />
<img src="http/TestStateGetHTTPPerformance_duration_requested_vs_actual.png" alt="TestStateGetHTTPPerformance_duration_requested_vs_actual.png" />
<img src="http/TestStateGetHTTPPerformance_header_size.png" alt="TestStateGetHTTPPerformance_header_size.png" />
<img src="http/TestStateGetHTTPPerformance_histogram_count.png" alt="TestStateGetHTTPPerformance_histogram_count.png" />
<img src="http/TestStateGetHTTPPerformance_histogram_percent.png" alt="TestStateGetHTTPPerformance_histogram_percent.png" />
<img src="http/TestStateGetHTTPPerformance_payload_size.png" alt="TestStateGetHTTPPerformance_payload_size.png" />
<img src="http/TestStateGetHTTPPerformance_qps.png" alt="TestStateGetHTTPPerformance_qps.png" />
<img src="http/TestStateGetHTTPPerformance_resource_cpu.png" alt="TestStateGetHTTPPerformance_resource_cpu.png" />
<img src="http/TestStateGetHTTPPerformance_resource_memory.png" alt="TestStateGetHTTPPerformance_resource_memory.png" />
<img src="http/TestStateGetHTTPPerformance_summary.png" alt="TestStateGetHTTPPerformance_summary.png" />
<img src="http/TestStateGetHTTPPerformance_tail_latency.png" alt="TestStateGetHTTPPerformance_tail_latency.png" />
<img src="http/TestStateGetHTTPPerformance_throughput.png" alt="TestStateGetHTTPPerformance_throughput.png" />

---------------

## gRPC

### TestStateGetGrpcPerformance

<img src="grpc/TestStateGetGrpcPerformance_duration_breakdown.png" alt="TestStateGetGrpcPerformance_duration_breakdown.png" />
<img src="grpc/TestStateGetGrpcPerformance_duration_requested_vs_actual.png" alt="TestStateGetGrpcPerformance_duration_requested_vs_actual.png" />
<img src="grpc/TestStateGetGrpcPerformance_histogram_count.png" alt="TestStateGetGrpcPerformance_histogram_count.png" />
<img src="grpc/TestStateGetGrpcPerformance_histogram_percent.png" alt="TestStateGetGrpcPerformance_histogram_percent.png" />
<img src="grpc/TestStateGetGrpcPerformance_qps.png" alt="TestStateGetGrpcPerformance_qps.png" />
<img src="grpc/TestStateGetGrpcPerformance_resource_cpu.png" alt="TestStateGetGrpcPerformance_resource_cpu.png" />
<img src="grpc/TestStateGetGrpcPerformance_resource_memory.png" alt="TestStateGetGrpcPerformance_resource_memory.png" />
<img src="grpc/TestStateGetGrpcPerformance_summary.png" alt="TestStateGetGrpcPerformance_summary.png" />
<img src="grpc/TestStateGetGrpcPerformance_tail_latency.png" alt="TestStateGetGrpcPerformance_tail_latency.png" />
