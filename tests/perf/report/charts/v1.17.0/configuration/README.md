## Highlights

Configuration API in v1.17 handles high throughput for Get operations and adds minimal overhead for Subscribe. All test runs completed with **100% success rate** and zero pod restarts.

**How to read these numbers:** "Iterations/sec" for Get tests measures how many complete read operations the system processed per second across all concurrent clients. For Subscribe tests, "iterations/sec" counts how many subscription checks completed per second — each check is the client waiting to receive a configuration change notification. The ~500 ms Subscribe latencies are driven by the config store's polling interval, not Dapr.

**Configuration Get — gRPC**:
- **26,810 iterations/sec** throughput | p50: **6.67 ms** | p90: **16.35 ms** | p95: **21.53 ms**
- 241,601 total requests — 100% success

The configuration Get API handled over 26,000 requests per second via gRPC, with the typical request completing in 6.67 ms. Across 241,601 total requests, every single one succeeded. The p95 of 21.53 ms shows that even the slower requests complete quickly.

**Configuration Get — HTTP**:
- **23,326 iterations/sec** throughput | p50: **8.27 ms** | p90: **18.29 ms** | p95: **23.14 ms**
- 210,368 total requests — 100% success

HTTP configuration Get is similarly fast, handling 23,326 requests per second with a median of 8.27 ms and p95 under 24 ms. The slightly lower throughput compared to gRPC reflects HTTP/1.1 protocol overhead.

**Configuration Subscribe — gRPC**:
- **968 iterations/sec** | p95: **510.58 ms** — near-zero Dapr overhead vs. baseline (**+0.36%**)

Subscribe is event-driven: the test measures how long a subscribed client waits to receive a configuration change notification. The ~500 ms p95 is driven by the polling interval of the config store, not Dapr — Dapr itself added only +1.82 ms (+0.36%) over going directly to the store, which is effectively zero overhead for a subscription workload.

**Configuration Subscribe — HTTP**:
- **981 iterations/sec** | p95: **473.20 ms** — 100% success

HTTP subscribe shows similar notification delivery characteristics, with 981 subscription checks per second and consistent delivery latency. As with gRPC, the latency is dominated by the config store's polling interval rather than Dapr's processing time.

---

## HTTP

### TestConfigurationGetHTTPPerformance

<img src="http/TestConfigurationGetHTTPPerformance_data_volume.png" alt="TestConfigurationGetHTTPPerformance_data_volume.png" />
<img src="http/TestConfigurationGetHTTPPerformance_duration_breakdown.png" alt="TestConfigurationGetHTTPPerformance_duration_breakdown.png" />
<img src="http/TestConfigurationGetHTTPPerformance_duration_low.png" alt="TestConfigurationGetHTTPPerformance_duration_low.png" />
<img src="http/TestConfigurationGetHTTPPerformance_resource_cpu.png" alt="TestConfigurationGetHTTPPerformance_resource_cpu.png" />
<img src="http/TestConfigurationGetHTTPPerformance_resource_memory.png" alt="TestConfigurationGetHTTPPerformance_resource_memory.png" />
<img src="http/TestConfigurationGetHTTPPerformance_summary.png" alt="TestConfigurationGetHTTPPerformance_summary.png" />
<img src="http/TestConfigurationGetHTTPPerformance_tail_latency.png" alt="TestConfigurationGetHTTPPerformance_tail_latency.png" />
<img src="http/TestConfigurationGetHTTPPerformance_throughput.png" alt="TestConfigurationGetHTTPPerformance_throughput.png" />

### TestConfigurationSubscribeHTTPPerformance

<img src="http/TestConfigurationSubscribeHTTPPerformance_data_volume.png" alt="TestConfigurationSubscribeHTTPPerformance_data_volume.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_duration_breakdown.png" alt="TestConfigurationSubscribeHTTPPerformance_duration_breakdown.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_duration_low.png" alt="TestConfigurationSubscribeHTTPPerformance_duration_low.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_resource_cpu.png" alt="TestConfigurationSubscribeHTTPPerformance_resource_cpu.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_resource_memory.png" alt="TestConfigurationSubscribeHTTPPerformance_resource_memory.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_summary.png" alt="TestConfigurationSubscribeHTTPPerformance_summary.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_tail_latency.png" alt="TestConfigurationSubscribeHTTPPerformance_tail_latency.png" />
<img src="http/TestConfigurationSubscribeHTTPPerformance_throughput.png" alt="TestConfigurationSubscribeHTTPPerformance_throughput.png" />

---------------

## gRPC

### TestConfigurationGetGRPCPerformance

<img src="grpc/TestConfigurationGetGRPCPerformance_data_volume.png" alt="TestConfigurationGetGRPCPerformance_data_volume.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_duration_breakdown.png" alt="TestConfigurationGetGRPCPerformance_duration_breakdown.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_duration_low.png" alt="TestConfigurationGetGRPCPerformance_duration_low.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_resource_cpu.png" alt="TestConfigurationGetGRPCPerformance_resource_cpu.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_resource_memory.png" alt="TestConfigurationGetGRPCPerformance_resource_memory.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_summary.png" alt="TestConfigurationGetGRPCPerformance_summary.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_tail_latency.png" alt="TestConfigurationGetGRPCPerformance_tail_latency.png" />
<img src="grpc/TestConfigurationGetGRPCPerformance_throughput.png" alt="TestConfigurationGetGRPCPerformance_throughput.png" />

### TestConfigurationSubscribeGRPCPerformance

<img src="grpc/TestConfigurationSubscribeGRPCPerformance_data_volume.png" alt="TestConfigurationSubscribeGRPCPerformance_data_volume.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_duration_breakdown.png" alt="TestConfigurationSubscribeGRPCPerformance_duration_breakdown.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_duration_low.png" alt="TestConfigurationSubscribeGRPCPerformance_duration_low.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_resource_cpu.png" alt="TestConfigurationSubscribeGRPCPerformance_resource_cpu.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_resource_memory.png" alt="TestConfigurationSubscribeGRPCPerformance_resource_memory.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_summary.png" alt="TestConfigurationSubscribeGRPCPerformance_summary.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_tail_latency.png" alt="TestConfigurationSubscribeGRPCPerformance_tail_latency.png" />
<img src="grpc/TestConfigurationSubscribeGRPCPerformance_throughput.png" alt="TestConfigurationSubscribeGRPCPerformance_throughput.png" />
