# Writing a perf test

Before writing a perf test, make sure that you have a configured [local test environment](./running-perf-tests.md) ready to run the perf tests.

## The test target

In order to create a performance test for a given functionality you must ensure that the functionality is testable in some way. There are functionalities that might need an intermediate app to act as the user application, others don't, as an example, an outputbinding may not require an app to be tested, you can make calls to daprd and collect the results. But if you have to test a functionality that requires an application, e.g any actors-related feature, you may need a custom app as well.

Follow the same as described in our [e2e-tests docs](./running-e2e-test.md#writing-e2e-test.md) to create custom test apps, make sure that none of the already created apps would serve as your test app, they can be reusable without further problems.

## Types of testing

Now, its time to decide which is the shape of the load you want to create against the target. The load shape defines not only which is the type of performance test that you'll execute but also the best option to be used as a perf tool as in dapr we currently have two, Fortio and k6.

Read more about the types of performance test and how this relates to the load shape [here](https://en.wikipedia.org/wiki/Software_performance_testing).

Once you have enough knowledge to decide your desired test load shape, let's go through the differences between the tools used internally by dapr to test the performance of our functionalities.

## Fortio

Fortio is a QPS(Query per Second) type of testing which generally is used to create a synthetic load without many nuances on it, the typical case of a stress testing.

From Fortio README:

"Fortio runs at a specified query per second (qps) and records an histogram of execution time and calculates percentiles (e.g. p99 ie the response time such as 99% of the requests take less than that number (in seconds, SI unit)). It can run for a set duration, for a fixed number of calls, or until interrupted (at a constant target QPS, or max speed/load per connection/thread)."

On dapr we have our fortio patched version which is built together with the performance test apps. The fortio app is called `perf-tester` and you'll probably see this name when deploying the test infrastructure.

### Testing with Fortio

Once you have decided that Fortio is the right fit for the tests that you're planning to create, let's go through an example.

All our performance tests live in the `./tests/perf` folder, create a new folder with the name of the test as well a `.go` file, do not forget to add the build tags to make sure that the dapr binary will not include those files

```go
//go:build perf
// +build perf

```

The performance test uses the same infrastructure as [our e2e tests](./running-e2e-test.md), the first thing is to create our `TestMain` which will be responsible for deploying the target apps and wait for those to be healthy.

```go
var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("my_test")
    serviceApplicationName := "target-app"
    clientApplicationName := "fortio-tester"

	testApps := []kube.AppDescription{
		{
			AppName:           serviceApplicationName,
			DaprEnabled:       true,
			ImageName:         "perf-actorjava",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			Labels: map[string]string{
				"daprtest": serviceApplicationName,
			},
		},
		{
			AppName:           clientApplicationName,
			DaprEnabled:       true,
			ImageName:         "perf-tester",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3001,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			Labels: map[string]string{
				"daprtest": clientApplicationName,
			},
			PodAffinityLabels: map[string]string{
				"daprtest": serviceApplicationName,
			},
		},
	}

	tr = runner.NewTestRunner("mytest", testApps, nil, nil)
	os.Exit(tr.Start(m))
}
```

We'll not go through many details on each field but you can assume that the test main is responsible for deploying at least two apps in that case, the `target-app` and the `fortio-tester`. Now its time to create our test driver, which will issue commands to the fortio tester app that will forward them to the target test app.

The first thing you need to do is to configure the test parameters

```go
func TestActorActivate(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(500), // the desired Query Per Second
		perf.WithConnections(8), // the number of http connections
		perf.WithDuration("1m"), // the test duration
		perf.WithPayload("{}"), // the test payload
	)
}
```

They are essential to make sure you have the right load shape for the functionality you want to test.
The next step is to call the perf tester with the serialized body parameters.

```go
endpoint := "target-app-endpoint"
p.TargetEndpoint = endpoint
p.StdClient = false
body, err := json.Marshal(&p)
daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", "fortio-tester-app-endpoint"), body)
```

The last thing we need to do is to collect the results,

```go
var daprResult perf.TestResult
err = json.Unmarshal(daprResp, &daprResult)
```

Then you can do any assertion based on the actual QPS, latency percetiles and others metrics that is collected during the test.

### K6

The main difference between k6 and fortio is because k6 is a scriptable load testing tool, so you can actually write scripts using its DSL, in javascript, and execute them on the cloud managed by the k6 operator. So you can easily create a different types of shapes using the same javascript test file and you can add custom metrics aside from the default ones.

I recommend the following links before starting to work with k6.

- [Executors](https://k6.io/docs/using-k6/scenarios/executors/)
- [Scenarios](https://k6.io/docs/using-k6/scenarios/)
- [Virtual users](https://k6.io/docs/misc/glossary/#virtual-user)
- [Extensions](https://k6.io/docs/extensions/)
- [Metrics](https://k6.io/docs/using-k6/metrics/)
- [Checks](https://k6.io/docs/using-k6/checks/)
- [Thresholds](https://k6.io/docs/using-k6/thresholds/)

### Testing with k6

You can use the full power of k6 on dapr and the only difference is that we kept our test driver as is being used for the QPS with Fortio load testing tool. In addition, the k6 load tester also contains a dapr sidecar container within the same pod. To start a new k6 load test create a simple js file inside the same folder as your test driver `.go` with your desired name (`test.js` is preferable).

The dapr specifities:

For k6 loadtesting on dapr perf test at least two methods are required, they are

- teardown - used to shutdown the dapr sidecar ensuring the job will complete with success.
- handleSummary - used to print the result as a JSON to be consumed later by the test framework

```js
const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`; // the dapr env are automatically injected

export function teardown(_) {
  const shutdownResult = http.post(`${DAPR_ADDRESS}/shutdown`);
  check(shutdownResult, {
    "shutdown response status code is 2xx":
      shutdownResult.status >= 200 && shutdownResult.status < 300,
  });
}

export function handleSummary(data) {
  return {
    stdout: JSON.stringify(data),
  };
}
```

> Warning: The javascript runtime used by k6 is very limited, they uses: https://github.com/dop251/goja, that currently follows ECMAScript 5.1(+), things like spread operation (...) does not work, you can't use npm directly, if you want to, you must bundle your module as a single js file using webpack or other js bundling tool.

In the test driver side there are a range of parameters you can set, from level of parallelism to disable logging. Use parallelism to set the number of pods will be used for the test, keep in mind that the test loadshape will continue the same, including the number of virtual users that are divided by the level of parallelism. So let's say you have a target of 1000 request/s and parallelism is set to 2, so you're going to have two pods issuing 500 requests/s each.

> The k6 pod logs are disabled by default, enabling it would be useful to investigate any running issue that you might have, to do that use `loadtest.EnableLog()` option, this will make the test summary break, and should be removed as soon as the test is being merged to master.

Next step is to create the load tester struct and then ask for the platform to run the test,
the platform.LoadTest(test) will stay busy until the test succeeds so it may take a while depending on your test scenarios.

```go
k6Test := loadtest.NewK6("./test.js", loadtest.WithParallelism(1))
require.NoError(t, tr.Platform.LoadTest(k6Test))
```

Now, it's time to collect the test results,

```go
summary, err := loadtest.K6ResultDefault(k6Test)
```

for each pod used (level of parallelism) there are one struct of metrics, you can access them using the `.RunnerResults` property, your test should fail based on the thresholds so, along with the runner results we also provide a flag `.Pass` which should be used to make assertions.

> When using custom metrics you can use the `loadtet.K6Result[T](k6test)` which provides a signature to Unmarshalling the k6 results on any arbitrary struct that you can provide.

### Add test disruption

Test disruptions can be added using the [xk6-disruptor](https://github.com/grafana/xk6-disruptor/tree/main/examples), you can use the full power of the disruptions.

### Prometheus Metrics

If you set the `K6_PROMETHEUS_REMOTE_URL`, `K6_PROMETHEUS_USER`, and `K6_PROMETHEUS_PASSWORD` environments variable, you should be able to send the k6 metrics to a remote prometheus server which might be helpful to have comparisons over time.

### Troubleshooting

When running k6, the errors are reported as exit codes, use the table below to help identify them.

| Error | Reason |
|-------|--------|
| 255   | Compile Error |
| 137   | OOM (out of memory) |
| 107   | Runtime Error |
