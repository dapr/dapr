## Highlights

**Configuration Get (HTTP)**:
- **23,326 iterations/sec** throughput | p50: **8.27 ms** | p90: **18.29 ms** | p95: **23.14 ms**
- 210,368 total requests — **100% success rate**

At over 23,000 reads per second, the HTTP configuration Get API delivers strong throughput with a consistent 8.27 ms median. The slightly lower throughput versus gRPC (23,326 vs. 26,810 iterations/sec) reflects HTTP/1.1 protocol overhead. Across 210,368 total requests, every single one succeeded.

**Configuration Subscribe (HTTP)**:
- **981 iterations/sec** | p50: **268 ms** | p95: **473 ms**
- 9,313 subscription checks — **100% success rate**

Subscribe measures how long a client waits to receive a configuration change notification. The ~470 ms p95 is driven by the config store's polling interval, not Dapr — the same as the gRPC subscribe path. HTTP subscribe handles 981 subscription checks per second with 100% delivery success. All 9,313 subscription events were received correctly.

---

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
