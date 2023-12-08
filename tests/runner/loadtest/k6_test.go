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
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/runner"

	v1 "github.com/grafana/k6-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	clientBatchv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	clientCoreV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/rest/fake"
)

type fakeK8sClient struct {
	kubernetes.Interface
	batchv1 clientBatchv1.BatchV1Interface
	corev1  clientCoreV1.CoreV1Interface
}

func (f *fakeK8sClient) BatchV1() clientBatchv1.BatchV1Interface {
	return f.batchv1
}

func (f *fakeK8sClient) CoreV1() clientCoreV1.CoreV1Interface {
	return f.corev1
}

type fakeCoreV1Client struct {
	clientCoreV1.CoreV1Interface
	pods       clientCoreV1.PodInterface
	configMaps clientCoreV1.ConfigMapInterface
}

func (f *fakeCoreV1Client) Pods(namespace string) clientCoreV1.PodInterface {
	return f.pods
}

func (f *fakeCoreV1Client) ConfigMaps(namespace string) clientCoreV1.ConfigMapInterface {
	return f.configMaps
}

type fakeConfigMapClient struct {
	clientCoreV1.ConfigMapInterface
	mock.Mock
	resp *corev1.ConfigMap
}

func (f *fakeConfigMapClient) Create(ctx context.Context, configMap *corev1.ConfigMap, opts metav1.CreateOptions) (*corev1.ConfigMap, error) {
	args := f.Called(ctx, configMap, opts)
	return f.resp, args.Error(0)
}

func (f *fakeConfigMapClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	args := f.Called(ctx, name, opts)
	return args.Error(0)
}

type fakeBatchV1Client struct {
	clientBatchv1.BatchV1Interface
	jobs clientBatchv1.JobInterface
}

func (f *fakeBatchV1Client) Jobs(namespace string) clientBatchv1.JobInterface {
	return f.jobs
}

type fakeJobClient struct {
	clientBatchv1.JobInterface
	mock.Mock
	jobsResult  *batchv1.JobList
	jobsResultF func() *batchv1.JobList
}

func (f *fakeJobClient) List(ctx context.Context, opts metav1.ListOptions) (*batchv1.JobList, error) {
	args := f.Called(ctx, opts)
	result := f.jobsResult
	if result == nil {
		result = f.jobsResultF()
	}
	return result, args.Error(0)
}

type fakePodClient struct {
	clientCoreV1.PodInterface
	mock.Mock
	listResult *corev1.PodList
	request    *rest.Request
}

func (f *fakePodClient) List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error) {
	args := f.Called(ctx, opts)
	return f.listResult, args.Error(0)
}

func (f *fakePodClient) GetLogs(name string, opts *corev1.PodLogOptions) *rest.Request {
	return f.request
}

type fakeK6Client struct {
	K6Interface
	mock.Mock
}

func (f *fakeK6Client) Create(ctx context.Context, k6 *v1.K6) (*v1.K6, error) {
	args := f.Called(ctx, k6)
	return &v1.K6{}, args.Error(0)
}

func (f *fakeK6Client) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	args := f.Called(ctx, name, opts)
	return args.Error(0)
}

func (f *fakeK6Client) Get(ctx context.Context, name string) (*v1.K6, error) {
	args := f.Called(ctx, name)
	return &v1.K6{}, args.Error(0)
}

func (f *fakeK6Client) List(ctx context.Context, opts metav1.ListOptions) (result *v1.K6List, err error) {
	args := f.Called(ctx, opts)
	return &v1.K6List{}, args.Error(0)
}

func TestK6(t *testing.T) {
	const (
		fakeNamespace = "fake"
		script        = "./fake.js"
	)
	t.Run("With-ish parameters should configure k6 tester", func(t *testing.T) {
		const envVarKey, envVarValue, fakeImg, parallelism, fakeName = "key", "value", "img", 3, "name"
		fakeCtx := context.TODO()
		tester := NewK6(
			script,
			WithCtx(fakeCtx),
			WithNamespace(fakeNamespace),
			WithRunnerEnvVar(envVarKey, envVarValue),
			WithRunnerImage(fakeImg),
			DisableDapr(),
			WithParallelism(parallelism),
			WithName(fakeName),
		)
		assert.Equal(t, script, tester.script)
		assert.Equal(t, fakeNamespace, tester.namespace)
		assert.Len(t, tester.runnerEnv, 1)
		assert.Equal(t, fakeImg, tester.runnerImage)
		assert.False(t, tester.addDapr)
		assert.Equal(t, parallelism, tester.parallelism)
		assert.Equal(t, fakeName, tester.name)
	})
	t.Run("Dispose should return nil when not client was set", func(t *testing.T) {
		require.NoError(t, NewK6("").Dispose())
	})
	t.Run("Dispose should return nil when delete does not returns an error", func(t *testing.T) {
		jobs := new(fakeJobClient)
		jobs.jobsResult = &batchv1.JobList{
			Items: []batchv1.Job{},
		}
		kubeClient := &fakeK8sClient{
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}

		jobs.On("List", mock.Anything, mock.Anything).Return(nil)
		k6 := NewK6("")
		k6.kubeClient = kubeClient
		k6Client := new(fakeK6Client)
		k6Client.On("Delete", mock.Anything, k6.name, mock.Anything).Return(nil)
		k6.k6Client = k6Client
		require.NoError(t, k6.Dispose())
		k6Client.AssertNumberOfCalls(t, "Delete", 1)
		jobs.AssertNumberOfCalls(t, "List", 1)
	})

	t.Run("Dispose should return nil when delete does returns not found", func(t *testing.T) {
		jobs := new(fakeJobClient)
		jobs.jobsResult = &batchv1.JobList{
			Items: []batchv1.Job{},
		}
		kubeClient := &fakeK8sClient{
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}

		jobs.On("List", mock.Anything, mock.Anything).Return(nil)
		k6 := NewK6("")
		k6.kubeClient = kubeClient
		k6Client := new(fakeK6Client)
		k6Client.On("Delete", mock.Anything, k6.name, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{}, "k6"))
		k6.k6Client = k6Client
		require.NoError(t, k6.Dispose())
		k6Client.AssertNumberOfCalls(t, "Delete", 1)
		jobs.AssertNumberOfCalls(t, "List", 1)
	})

	t.Run("Wait should not retry when all jobs has successful finished", func(t *testing.T) {
		k6 := NewK6("")
		jobs := new(fakeJobClient)
		jobs.jobsResult = &batchv1.JobList{
			Items: []batchv1.Job{{
				Status: batchv1.JobStatus{
					Succeeded: 1,
					Active:    0,
				},
			}},
		}
		kubeClient := &fakeK8sClient{
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}
		k6.kubeClient = kubeClient

		jobs.On("List", mock.Anything, mock.Anything).Return(nil)
		require.NoError(t, k6.waitForCompletion())
		jobs.AssertNumberOfCalls(t, "List", 1)
	})

	t.Run("Wait should not retry when all jobs has failed", func(t *testing.T) {
		k6 := NewK6("")
		jobs := new(fakeJobClient)
		jobs.jobsResult = &batchv1.JobList{
			Items: []batchv1.Job{{
				Status: batchv1.JobStatus{
					Succeeded: 0,
					Failed:    1,
					Active:    0,
				},
			}},
		}
		kubeClient := &fakeK8sClient{
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}
		k6.kubeClient = kubeClient

		jobs.On("List", mock.Anything, mock.Anything).Return(nil)
		require.NoError(t, k6.waitForCompletion())
		jobs.AssertNumberOfCalls(t, "List", 1)
	})

	t.Run("Result should return an error if pod list returns an error", func(t *testing.T) {
		fakeErr := errors.New("fake")

		k6 := NewK6("")
		pods := new(fakePodClient)
		kubeClient := &fakeK8sClient{
			corev1: &fakeCoreV1Client{
				pods: pods,
			},
		}
		k6.kubeClient = kubeClient

		pods.On("List", mock.Anything, mock.Anything).Return(fakeErr)
		_, err := K6ResultDefault(k6)
		assert.Equal(t, err, fakeErr)
		pods.AssertNumberOfCalls(t, "List", 1)
	})
	t.Run("Result should not return an error if pod get logs returns pod logs", func(t *testing.T) {
		k6 := NewK6("")
		pods := new(fakePodClient)
		called := 0
		fakeClient := fake.CreateHTTPClient(func(r *http.Request) (*http.Response, error) {
			called++
			return &http.Response{
				Body:       io.NopCloser(bytes.NewBufferString("{}")),
				StatusCode: http.StatusOK,
			}, nil
		})
		pods.request = rest.NewRequestWithClient(nil, "", rest.ClientContentConfig{}, fakeClient)
		pods.listResult = &corev1.PodList{
			Items: []corev1.Pod{{}},
		}

		jobs := new(fakeJobClient)
		jobs.jobsResult = &batchv1.JobList{
			Items: []batchv1.Job{{
				Status: batchv1.JobStatus{
					Succeeded: 1,
					Failed:    0,
					Active:    0,
				},
			}},
		}
		jobs.On("List", mock.Anything, mock.Anything).Return(nil)

		kubeClient := &fakeK8sClient{
			corev1: &fakeCoreV1Client{
				pods: pods,
			},
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}
		k6.kubeClient = kubeClient

		pods.On("List", mock.Anything, mock.Anything).Return(nil)
		summary, err := K6ResultDefault(k6)
		require.NoError(t, err)
		pods.AssertNumberOfCalls(t, "List", 1)
		jobs.AssertNumberOfCalls(t, "List", 1)
		assert.Equal(t, 1, called)
		assert.True(t, summary.Pass)
	})
	t.Run("Result should return an error if pod get logs return an error", func(t *testing.T) {
		k6 := NewK6("")
		pods := new(fakePodClient)
		called := 0
		fakeClient := fake.CreateHTTPClient(func(r *http.Request) (*http.Response, error) {
			called++
			return &http.Response{
				Body:       io.NopCloser(bytes.NewBufferString("{}")),
				StatusCode: http.StatusInternalServerError,
			}, nil
		})
		pods.request = rest.NewRequestWithClient(nil, "", rest.ClientContentConfig{}, fakeClient)
		pods.listResult = &corev1.PodList{
			Items: []corev1.Pod{{}},
		}
		kubeClient := &fakeK8sClient{
			corev1: &fakeCoreV1Client{
				pods: pods,
			},
		}
		k6.kubeClient = kubeClient

		pods.On("List", mock.Anything, mock.Anything).Return(nil)
		_, err := K6ResultDefault(k6)
		require.Error(t, err)
		pods.AssertNumberOfCalls(t, "List", 1)
		assert.Equal(t, 1, called)
	})
	t.Run("k8sRun should return an error if file not exists", func(t *testing.T) {
		const fileNotExists = "./not_exists.js"
		k6 := NewK6(fileNotExists)
		k6.setupOnce.Do(func() {}) // call once to avoid be called
		require.Error(t, k6.k8sRun(&runner.KubeTestPlatform{}))
	})

	t.Run("k8sRun should return an error if createconfig returns an error", func(t *testing.T) {
		deleteErr := errors.New("fake-delete")
		const file = "./file_exists.js"
		_, err := os.Create(file)
		require.NoError(t, err)
		defer os.RemoveAll(file)
		k6 := NewK6(file)
		k6.setupOnce.Do(func() {}) // call once to avoid be called
		configMaps := new(fakeConfigMapClient)
		configMaps.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(deleteErr)
		k6.kubeClient = &fakeK8sClient{
			corev1: &fakeCoreV1Client{
				configMaps: configMaps,
			},
		}
		assert.Equal(t, k6.k8sRun(&runner.KubeTestPlatform{}), deleteErr)
		configMaps.AssertNumberOfCalls(t, "Delete", 1)
	})

	t.Run("k8sRun should call k6client delete and create", func(t *testing.T) {
		jobs := new(fakeJobClient)
		called := 0
		jobs.jobsResultF = func() *batchv1.JobList {
			called++
			if called == 2 {
				return &batchv1.JobList{
					Items: []batchv1.Job{{
						Status: batchv1.JobStatus{
							Succeeded: 1,
							Active:    0,
						},
					}},
				}
			}
			return &batchv1.JobList{
				Items: []batchv1.Job{},
			}
		}

		jobs.On("List", mock.Anything, mock.Anything).Return(nil)

		pods := &fakePodClient{
			listResult: &corev1.PodList{
				Items: []corev1.Pod{},
			},
		}
		pods.On("List", mock.Anything, mock.Anything).Return(nil)
		const file = "./file_exists.js"
		_, err := os.Create(file)
		require.NoError(t, err)
		defer os.RemoveAll(file)
		k6 := NewK6(file)
		k6.setupOnce.Do(func() {}) // call once to avoid be called
		configMaps := new(fakeConfigMapClient)
		configMaps.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		configMaps.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		k6.kubeClient = &fakeK8sClient{
			corev1: &fakeCoreV1Client{
				configMaps: configMaps,
				pods:       pods,
			},
			batchv1: &fakeBatchV1Client{
				jobs: jobs,
			},
		}
		k6Client := new(fakeK6Client)
		k6Client.On("Delete", mock.Anything, k6.name, mock.Anything).Return(nil)
		k6Client.On("Create", mock.Anything, mock.Anything).Return(nil)
		k6.k6Client = k6Client
		require.NoError(t, k6.k8sRun(&runner.KubeTestPlatform{}))
		configMaps.AssertNumberOfCalls(t, "Delete", 1)
		configMaps.AssertNumberOfCalls(t, "Create", 1)
		k6Client.AssertNumberOfCalls(t, "Create", 1)
		k6Client.AssertNumberOfCalls(t, "Delete", 1)
		jobs.AssertNumberOfCalls(t, "List", 2)
		pods.AssertNumberOfCalls(t, "List", 1)
	})
}
