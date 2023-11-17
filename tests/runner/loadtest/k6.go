/*
Copyright 2022 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	guuid "github.com/google/uuid"

	"github.com/dapr/dapr/pkg/injector/annotations"
	testplatform "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"

	k6api "github.com/grafana/k6-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	k6ConfigMapPrefix       = "k6-tests"
	scriptName              = "test.js"
	defaultK6ServiceAccount = "k6-sa"
	// pollInterval is how frequently will poll for updates.
	pollInterval = 5 * time.Second
	// pollTimeout is how long the test should took.
	pollTimeout = 20 * time.Minute
)

// k6ConfigMapFor builds a unique config map for each test being executed.
func k6ConfigMapFor(testName string) string {
	return fmt.Sprintf("%s-%s", k6ConfigMapPrefix, testName)
}

// K6GaugeMetric is the representation of a k6 gauge
type K6GaugeMetric struct {
	Type     string `json:"type"`
	Contains string `json:"contains"`
	Values   struct {
		Max int `json:"max"`
		P90 int `json:"p(90)"`
		P95 int `json:"p(95)"`
		Avg int `json:"avg"`
		Min int `json:"min"`
		Med int `json:"med"`
	} `json:"values"`
}

// K6TrendMetric is the representation of a k6 trendmetric
type K6TrendMetric struct {
	Type     string `json:"type"`
	Contains string `json:"contains"`
	Values   struct {
		Max float64 `json:"max"`
		P90 float64 `json:"p(90)"`
		P95 float64 `json:"p(95)"`
		Avg float64 `json:"avg"`
		Min float64 `json:"min"`
		Med float64 `json:"med"`
	} `json:"values"`
}

// K6CounterMetric is a metric that has only count and rate
type K6CounterMetric struct {
	Type     string `json:"type"`
	Contains string `json:"contains"`
	Values   struct {
		Count float64 `json:"count"`
		Rate  float64 `json:"rate"`
	} `json:"values"`
}

// K6Rate metric is the representation of a k6 rate metric
type K6RateMetric struct {
	Type     string `json:"type"`
	Contains string `json:"contains"`
	Values   struct {
		Rate   float64 `json:"rate"`
		Passes int     `json:"passes"`
		Fails  int     `json:"fails"`
	} `json:"values"`
}

// K6RunnerMetricsSummary represents a single unit of testing result.
type K6RunnerMetricsSummary struct {
	Iterations                         K6CounterMetric `json:"iterations"`
	HTTPReqConnecting                  K6TrendMetric   `json:"http_req_connecting"`
	HTTPReqTLSHandshaking              K6TrendMetric   `json:"http_req_tls_handshaking"`
	HTTPReqReceiving                   K6TrendMetric   `json:"http_req_receiving"`
	HTTPReqWaiting                     K6TrendMetric   `json:"http_req_waiting"`
	HTTPReqSending                     K6TrendMetric   `json:"http_req_sending"`
	Checks                             K6RateMetric    `json:"checks"`
	DataReceived                       K6CounterMetric `json:"data_received"`
	VusMax                             K6GaugeMetric   `json:"vus_max"`
	HTTPReqDurationExpectedResponse    *K6TrendMetric  `json:"http_req_duration{expected_response:true}"`
	HTTPReqDurationNonExpectedResponse *K6TrendMetric  `json:"http_req_duration{expected_response:false}"`
	Vus                                K6GaugeMetric   `json:"vus"`
	HTTPReqFailed                      K6RateMetric    `json:"http_req_failed"`
	IterationDuration                  K6TrendMetric   `json:"iteration_duration"`
	HTTPReqBlocked                     K6TrendMetric   `json:"http_req_blocked"`
	HTTPReqs                           K6CounterMetric `json:"http_http_reqs"`
	DataSent                           K6CounterMetric `json:"data_sent"`
	HTTPReqDuration                    K6TrendMetric   `json:"http_req_duration"`
}

// K6TestSummary is the wrapped k6 results collected for all runners.
type K6TestSummary[T any] struct {
	Pass           bool `json:"pass"`
	RunnersResults []*T `json:"runnersResults"`
}

type K6RunnerSummary[T any] struct {
	Metrics T `json:"metrics"`
}

// K6 is used for executing tests using the k6 load testing tool.
type K6 struct {
	name              string
	configName        string
	script            string
	appID             string
	parallelism       int
	runnerEnv         []corev1.EnvVar
	addDapr           bool
	runnerImage       string
	namespace         string
	kubeClient        kubernetes.Interface
	k6Client          K6Interface
	setupOnce         *sync.Once
	ctx               context.Context
	cancel            context.CancelFunc
	testMemoryLimit   string
	testMemoryRequest string
	daprMemoryLimit   string
	daprMemoryRequest string
	logEnabled        bool
}

// collectResult read the pod logs and transform into json output.
func collectResult[T any](k6 *K6, podName string) (*T, error) {
	if k6.logEnabled {
		return nil, nil
	}
	req := k6.kubeClient.CoreV1().Pods(k6.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: "k6",
	})

	podLogs, err := req.Stream(k6.ctx)
	if err != nil {
		return nil, err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return nil, fmt.Errorf("unable to copy logs from the pod: %w", err)
	}

	bts := buf.Bytes()

	if len(bts) == 0 {
		return nil, nil
	}

	var k6Result K6RunnerSummary[T]
	if err := json.Unmarshal(bts, &k6Result); err != nil {
		// this shouldn't normally happen but if it does, let's log output by default
		return nil, fmt.Errorf("unable to marshal `%s`: %w", string(bts), err)
	}

	return &k6Result.Metrics, nil
}

// setupClient setup the kubernetes client
func (k6 *K6) setupClient(k8s *runner.KubeTestPlatform) (err error) {
	k6.kubeClient = k8s.KubeClient.ClientSet
	crdConfig := *k8s.KubeClient.GetClientConfig()
	k6.k6Client, err = newK6Client(&crdConfig, k6.namespace)
	return
}

func (k6 *K6) createConfig(ctx context.Context) error {
	scriptContent, err := os.ReadFile(k6.script)
	if err != nil {
		return err
	}
	configClient := k6.kubeClient.CoreV1().ConfigMaps(k6.namespace)
	// ignore not found
	if err = configClient.Delete(ctx, k6.configName, v1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	cm := corev1.ConfigMap{
		TypeMeta:   v1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{Name: k6.configName, Namespace: k6.namespace},
		Data: map[string]string{
			scriptName: string(scriptContent),
		},
	}

	_, err = configClient.Create(ctx, &cm, v1.CreateOptions{})
	return err
}

// k8sRun run the load test for the given kubernetes platform.
func (k6 *K6) k8sRun(k8s *runner.KubeTestPlatform) error {
	var err error
	k6.setupOnce.Do(func() {
		err = k6.setupClient(k8s)
	})
	if err != nil {
		return err
	}

	if err = k6.createConfig(k6.ctx); err != nil {
		return err
	}

	if err = k6.Dispose(); err != nil {
		return err
	}

	runnerAnnotations := make(map[string]string)
	if k6.addDapr {
		runnerAnnotations[annotations.KeyEnabled] = "true"
		runnerAnnotations[annotations.KeyAppID] = k6.appID
		runnerAnnotations[annotations.KeyMemoryLimit] = k6.daprMemoryLimit
		runnerAnnotations[annotations.KeyMemoryRequest] = k6.daprMemoryRequest
	}

	args := "--include-system-env-vars"
	if !k6.logEnabled {
		args += " --log-output=none"
	}
	labels := map[string]string{
		testplatform.TestAppLabelKey: k6.name,
	}
	k6Test := k6api.K6{
		TypeMeta: v1.TypeMeta{
			Kind:       "K6",
			APIVersion: "k6.io/v1alpha1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      k6.name,
			Namespace: k6.namespace,
		},
		Spec: k6api.K6Spec{
			Script: k6api.K6Script{
				ConfigMap: k6api.K6Configmap{
					Name: k6.configName,
					File: scriptName,
				},
			},
			Parallelism: int32(k6.parallelism),
			Arguments:   args,
			Starter: k6api.Pod{
				Metadata: k6api.PodMetadata{
					Labels: labels,
				},
			},
			Runner: k6api.Pod{
				ServiceAccountName: defaultK6ServiceAccount,
				Env: append(k6.runnerEnv, corev1.EnvVar{
					Name:  "TEST_NAMESPACE",
					Value: k6.namespace,
				}),
				Image: runner.BuildTestImageName(k6.runnerImage),
				Metadata: k6api.PodMetadata{
					Annotations: runnerAnnotations,
					Labels:      labels,
				},
				Resources: corev1.ResourceRequirements{
					Limits: map[corev1.ResourceName]resource.Quantity{
						"memory": {
							Format: resource.Format(k6.testMemoryLimit),
						},
					},
					Requests: map[corev1.ResourceName]resource.Quantity{
						"memory": {
							Format: resource.Format(k6.daprMemoryRequest),
						},
					},
				},
			},
		},
	}

	_, err = k6.k6Client.Create(k6.ctx, &k6Test)
	if err != nil {
		return err
	}

	if err = k6.waitForCompletion(); err != nil {
		return err
	}
	return k6.streamLogs()
}

func (k6 *K6) streamLogs() error {
	return testplatform.StreamContainerLogsToDisk(k6.ctx, k6.name, k6.kubeClient.CoreV1().Pods(k6.namespace))
}

// selector return the label selector for the k6 running pods and jobs.
func (k6 *K6) selector() string {
	return fmt.Sprintf("k6_cr=%s,runner=true", k6.name)
}

// hasPassed returns true if all k6 related jobs has been succeeded
func (k6 *K6) hasPassed() (bool, error) {
	jobsClient := k6.kubeClient.BatchV1().Jobs(k6.namespace)
	ctx, cancel := context.WithTimeout(k6.ctx, time.Minute)
	jobList, err := jobsClient.List(ctx, v1.ListOptions{
		LabelSelector: k6.selector(),
	})
	cancel()
	if err != nil {
		return false, err
	}

	hasPassed := true
	for _, job := range jobList.Items {
		hasPassed = hasPassed && job.Status.Succeeded == 1
		if !hasPassed {
			return false, nil
		}
	}

	return hasPassed, nil
}

// K6ResultDefault exports results to k6 default metrics.
func K6ResultDefault(k6 *K6) (*K6TestSummary[K6RunnerMetricsSummary], error) {
	return K6Result[K6RunnerMetricsSummary](k6)
}

// K6Result extract the test summary results from pod logs.
func K6Result[T any](k6 *K6) (*K6TestSummary[T], error) {
	pods, podErr := k6.kubeClient.CoreV1().Pods(k6.namespace).List(k6.ctx, v1.ListOptions{
		LabelSelector: k6.selector(),
	})
	if podErr != nil {
		return nil, podErr
	}
	runnersResults := make([]*T, 0)
	for _, pod := range pods.Items {
		runnerResult, err := collectResult[T](k6, pod.Name)
		if err != nil {
			return nil, err
		}
		runnersResults = append(runnersResults, runnerResult)
	}

	pass, err := k6.hasPassed()
	if err != nil {
		return nil, err
	}

	return &K6TestSummary[T]{
		Pass:           pass,
		RunnersResults: runnersResults,
	}, nil
}

// Run based on platform.
func (k6 *K6) Run(platform runner.PlatformInterface) error {
	switch p := platform.(type) {
	case *runner.KubeTestPlatform:
		return k6.k8sRun(p)
	default:
		return fmt.Errorf("platform %T not supported", p)
	}
}

// isJobCompleted returns true if job object is complete.
func isJobCompleted(job batchv1.Job) bool {
	return job.Status.Succeeded+job.Status.Failed >= 1 && job.Status.Active == 0
}

// waitUntilJobsState wait until all jobs achieves the expected state.
func (k6 *K6) waitUntilJobsState(isState func(*batchv1.JobList, error) bool) error {
	jobsClient := k6.kubeClient.BatchV1().Jobs(k6.namespace)

	ctx, cancel := context.WithTimeout(k6.ctx, pollTimeout)
	defer cancel()

	waitErr := wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		jobList, err := jobsClient.List(ctx, v1.ListOptions{
			LabelSelector: k6.selector(),
		})

		done := isState(jobList, err)
		if done {
			return true, nil
		}
		return false, err
	})

	if waitErr != nil {
		return fmt.Errorf("k6 %q is not in desired state, received: %s", k6.name, waitErr)
	}

	return nil
}

// waitForDeletion wait until all pods are deleted.
func (k6 *K6) waitForDeletion() error {
	return k6.waitUntilJobsState(func(jobList *batchv1.JobList, err error) bool {
		return (err != nil && apierrors.IsNotFound(err)) || (jobList != nil && len(jobList.Items) == 0)
	})
}

// waitForCompletion for the tests until it finish.
func (k6 *K6) waitForCompletion() error {
	return k6.waitUntilJobsState(func(jobList *batchv1.JobList, err error) bool {
		if err != nil || jobList == nil || len(jobList.Items) < k6.parallelism {
			return false
		}

		for _, job := range jobList.Items {
			if !isJobCompleted(job) {
				return false
			}
		}

		return true
	})
}

// Dispose deletes the test resource.
func (k6 *K6) Dispose() error {
	if k6.k6Client == nil {
		return nil
	}
	if err := k6.k6Client.Delete(k6.ctx, k6.name, v1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return k6.waitForDeletion()
}

type K6Opt = func(*K6)

// WithMemoryLimits set app and dapr memory limits
func WithMemoryLimits(daprRequest, daprLimit, appRequest, appLimit string) K6Opt {
	return func(k *K6) {
		k.addDapr = true
		k.daprMemoryLimit = daprLimit
		k.daprMemoryRequest = daprRequest
		k.testMemoryLimit = appLimit
		k.testMemoryRequest = appRequest
	}
}

// WithName sets the test name.
func WithName(name string) K6Opt {
	return func(k *K6) {
		k.name = name
	}
}

// WithAppID sets the appID when dapr is enabled.
func WithAppID(appID string) K6Opt {
	return func(k *K6) {
		k.appID = appID
	}
}

// WithScript set the test script.
func WithScript(script string) K6Opt {
	return func(k *K6) {
		k.script = script
	}
}

// WithParallelism configures the number of parallel runners at once.
func WithParallelism(p int) K6Opt {
	return func(k *K6) {
		k.parallelism = p
	}
}

// DisableDapr disable dapr on runner.
func DisableDapr() K6Opt {
	return func(k *K6) {
		k.addDapr = false
	}
}

// WithRunnerImage sets the runner image, defaults to k6-custom.
func WithRunnerImage(image string) K6Opt {
	return func(k *K6) {
		k.runnerImage = image
	}
}

// WithRunnerEnvVar adds a new env variable to the runner.
func WithRunnerEnvVar(name, value string) K6Opt {
	return func(k *K6) {
		k.runnerEnv = append(k.runnerEnv, corev1.EnvVar{
			Name:  name,
			Value: value,
		})
	}
}

// WithNamespace sets the test namespace.
func WithNamespace(namespace string) K6Opt {
	return func(k *K6) {
		k.namespace = namespace
	}
}

// WithCtx sets the test context.
func WithCtx(ctx context.Context) K6Opt {
	return func(k *K6) {
		mCtx, cancel := context.WithCancel(ctx)
		k.ctx = mCtx
		k.cancel = cancel
	}
}

// EnableLog enables the console output debugging. This should be deactivated when running in production
// to avoid errors when parsing the test result.
func EnableLog() K6Opt {
	return func(k *K6) {
		k.logEnabled = true
	}
}

// NewK6 creates a new k6 load testing with the given options.
func NewK6(scriptPath string, opts ...K6Opt) *K6 {
	ctx, cancel := context.WithCancel(context.Background())
	uniqueTestID := guuid.New().String()[:6] // to avoid name clash when a clean up is happening
	log.Printf("starting %s k6 test", uniqueTestID)

	k6Tester := &K6{
		name:              fmt.Sprintf("k6-test-%s", uniqueTestID),
		appID:             "k6-tester",
		script:            scriptPath,
		parallelism:       1,
		addDapr:           true,
		runnerImage:       "perf-k6-custom",
		namespace:         testplatform.DaprTestNamespace,
		setupOnce:         &sync.Once{},
		ctx:               ctx,
		cancel:            cancel,
		daprMemoryLimit:   "512Mi",
		daprMemoryRequest: "256Mi",
		testMemoryLimit:   "1024Mi",
		testMemoryRequest: "256Mi",
		runnerEnv:         []corev1.EnvVar{},
	}

	for _, opt := range opts {
		opt(k6Tester)
	}

	k6Tester.configName = k6ConfigMapFor(k6Tester.name)

	return k6Tester
}
