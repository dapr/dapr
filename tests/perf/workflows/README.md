# Dapr Workflows Performance Tests

### Objective:
This project is designed to assess the performance of Dapr workflows utilizing the chaining pattern. The selection of the chaining pattern is grounded in data derived from durable functions, where it stands out as the most widely adopted pattern. Aligning our approach with industry practices, we opt for the chaining pattern to conduct performance tests on Dapr workflows.

### Rationale:
Durable functions employ the chaining pattern extensively for performance testing, establishing it as a reliable choice for evaluating Dapr workflows. The emphasis in our performance tests is on scrutinizing the workflow logic; therefore, we intentionally maintain simplicity in the workflow activities.

## Test scenarios

There are two major testing scenarios in this test suit. 

### Baisc load tests

1. Average load test
```js
    average_load: {
        executor: 'constant-vus',
        vus: 500, 
        duration: '10m',
    },
```
The average load test runs for a duration of 10 minutes, involving 500 clients that individually send requests to schedule a new workflow. It's worth noting that the 500-client threshold is intentionally set to be slightly aggressive for several reasons. Firstly, it aims to encompass real-world production scenarios, acknowledging that the majority of customers typically won't have up to 500 clients scheduling workflows simultaneously. Secondly, this load level serves as a robust measure of the workflow's throughput capabilities. Lastly, the choice aligns with benchmarks established by Temporal.

In the test code, we implement a monitoring mechanism to check CPU and memory usage every 30 seconds. This data serves as a robust indicator, allowing us to promptly identify any potential memory leaks.

2. Comprehensive load test
```js
    comprehensive_load: {
        executor: 'ramping-vus',
        stages: [
            // average load: 500 VUs run for 2 mins
          { duration: '10s', target: 500 },
          { duration: '2m', target: 500 },
          // stress load: 1000 VUs run for 2 mins
          { duration: '10s', target: 1000 },
          { duration: '2m', target: 1000 },
          { duration: '20s', target: 0 },
          // Spike load: 1000 VUs in 10s
          { duration: '10s', target: 1000 },
          { duration: '5s', target: 0 },
        ],
        gracefulRampDown: '10s',
    },
```

The comprehensive test comprises distinct scenarios aimed at thoroughly assessing the system under various conditions:

- Quick Ramp-Up to 500 Clients (Burst Test):
Rapidly increase the number of clients to 500, simulating a burst of requests within a short timeframe. This scenario is designed to evaluate the system's responsiveness to sudden spikes in demand.

- Stable Load Test with 500 Clients (Average Load):
Maintain a stable load with 500 clients for 2 minutes. This phase represents an average load test, allowing us to gauge the system's sustained performance under typical operating conditions.

- Ramp-Up to 1000 Clients (Stress Test):
Continue by ramping up the client count to 1000, sustaining this load for an additional 2 minutes. This stress load test is designed to push the system beyond average usage, identifying potential performance bottlenecks and stress points.

- Spike Test (Rapid Ramp-Up and Down):
Commence with a rapid ramp-up from 0 clients to 1000 clients in 10 seconds, followed by an immediate ramp-down to 0 clients in 5 seconds. This scenario, known as a spike test, assesses the system's ability to handle sudden and extreme fluctuations in load.

The decision to combine these scenarios into a single comprehensive test is driven by practical considerations. Local testing revealed that a significant portion of the test duration is consumed by the initialization and termination of k6 pods. By consolidating scenarios into a single test, we circumvent the need for repeated creation and destruction of k6 pods, resulting in substantial time savings.


### Edge scenario tests
coming soon...
