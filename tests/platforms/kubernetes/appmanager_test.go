// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/utils"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
)

const testNamespace = "apputil-test"

func newDefaultFakeClient() *KubeClient {
	fakeclient := fake.NewSimpleClientset()
	return &KubeClient{
		ClientSet: fakeclient,
	}
}

func newFakeKubeClient() *KubeClient {
	return &KubeClient{
		ClientSet: &fake.Clientset{},
	}
}

func testAppDescription() utils.AppDescription {
	return utils.AppDescription{
		AppName:        "testapp",
		DaprEnabled:    true,
		ImageName:      "helloworld",
		RegistryName:   "dapriotest",
		Replicas:       1,
		IngressEnabled: true,
	}
}

func TestDeployApp(t *testing.T) {
	client := newDefaultFakeClient()
	appManager := NewAppManager(client, testNamespace)

	testApp := testAppDescription()

	// act
	_, err := appManager.Deploy(testApp)
	assert.NoError(t, err)

	// assert
	deploymentClient := client.Deployments(testNamespace)
	deployment, _ := deploymentClient.Get(testApp.AppName, metav1.GetOptions{})
	assert.NotNil(t, deployment)
	assert.Equal(t, testApp.AppName, deployment.ObjectMeta.Name)
	assert.Equal(t, testNamespace, deployment.ObjectMeta.Namespace)
	assert.Equal(t, int32(1), *deployment.Spec.Replicas)
	assert.Equal(t, testApp.AppName, deployment.Spec.Selector.MatchLabels["testapp"])
	assert.Equal(t, "true", deployment.Spec.Template.ObjectMeta.Annotations["dapr.io/enabled"])
	assert.Equal(t, testApp.AppName, deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "dapriotest/helloworld", deployment.Spec.Template.Spec.Containers[0].Image)
}

func TestWaitUntilDeploymentState(t *testing.T) {
	testApp := testAppDescription()
	var createdDeploymentObj *appsv1.Deployment

	t.Run("deployment is in done state", func(t *testing.T) {
		client := newFakeKubeClient()
		getVerbCalled := 0

		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"*",
			"deployments",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				switch action.GetVerb() {
				case "create":
					// return the same deployment object
					createdDeploymentObj = action.(core.CreateAction).GetObject().(*appsv1.Deployment)
					createdDeploymentObj.Status.ReadyReplicas = 0

				case "get":
					// set 1 to ReadyReplicas when WaitUntilDeploymentState called get deployments 2 times
					if getVerbCalled == 2 {
						createdDeploymentObj.Status.ReadyReplicas = testApp.Replicas
					} else {
						getVerbCalled++
					}
				}
				return true, createdDeploymentObj, nil
			})

		appManager := NewAppManager(client, testNamespace)

		// act
		_, err := appManager.Deploy(testApp)
		assert.NoError(t, err)

		// assert
		d, err := appManager.WaitUntilDeploymentState(testApp, appManager.IsDeploymentDone)

		assert.NoError(t, err)
		assert.Equal(t, testApp.Replicas, d.Status.ReadyReplicas)
		assert.Equal(t, 2, getVerbCalled)
	})

	t.Run("deployment is in deleted state - replica 0", func(t *testing.T) {
		client := newFakeKubeClient()
		getVerbCalled := 0

		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"*",
			"deployments",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				switch action.GetVerb() {
				case "create":
					// return the same deployment object
					createdDeploymentObj = action.(core.CreateAction).GetObject().(*appsv1.Deployment)
					createdDeploymentObj.Status.Replicas = testApp.Replicas
				case "get":
					// set 0 to ReadyReplicas when WaitUntilDeploymentState called get deployments 2 times
					if getVerbCalled == 2 {
						createdDeploymentObj.Status.Replicas = 0
					} else {
						getVerbCalled++
					}
				}
				return true, createdDeploymentObj, nil
			})

		appManager := NewAppManager(client, testNamespace)

		// act
		_, err := appManager.Deploy(testApp)
		assert.NoError(t, err)

		// assert
		d, err := appManager.WaitUntilDeploymentState(testApp, appManager.IsDeploymentDeleted)

		assert.NoError(t, err)
		assert.Equal(t, int32(0), d.Status.ReadyReplicas)
		assert.Equal(t, 2, getVerbCalled)
	})

	t.Run("deployment is in deleted state - NotFound", func(t *testing.T) {
		client := newFakeKubeClient()
		getVerbCalled := 0

		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"*",
			"deployments",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				switch action.GetVerb() {
				case "create":
					// return the same deployment object
					createdDeploymentObj = action.(core.CreateAction).GetObject().(*appsv1.Deployment)
					createdDeploymentObj.Status.ReadyReplicas = testApp.Replicas

				case "get":
					// return notfound error when WaitUntilDeploymentState called get deployments 2 times
					if getVerbCalled == 2 {
						err := errors.NewNotFound(
							schema.GroupResource{
								Group:    "fakeGroup",
								Resource: "fakeResource",
							},
							"deployments")

						return true, nil, err
					}

					getVerbCalled++
				}
				return true, createdDeploymentObj, nil
			})

		appManager := NewAppManager(client, testNamespace)

		// act
		_, err := appManager.Deploy(testApp)
		assert.NoError(t, err)

		// assert
		d, err := appManager.WaitUntilDeploymentState(testApp, appManager.IsDeploymentDeleted)

		assert.NoError(t, err)
		assert.Nil(t, d)
		assert.Equal(t, 2, getVerbCalled)
	})
}

func TestValidiateSideCar(t *testing.T) {
	testApp := testAppDescription()

	objMeta := metav1.ObjectMeta{
		Name:      testApp.AppName,
		Namespace: testNamespace,
		Labels: map[string]string{
			TestAppLabelKey: testApp.AppName,
		},
	}

	t.Run("Sidecar is injected", func(t *testing.T) {
		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"list",
			"pods",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				singlePod := apiv1.Pod{
					ObjectMeta: objMeta,
					Spec: apiv1.PodSpec{
						Containers: []apiv1.Container{
							{
								Name:  "daprd",
								Image: "daprio/daprd:latest",
							},
							{
								Name:  testApp.AppName,
								Image: fmt.Sprintf("%s/%s", testApp.RegistryName, testApp.ImageName),
							},
						},
					},
				}

				podList := &apiv1.PodList{
					Items: []apiv1.Pod{singlePod},
				}

				return true, podList, nil
			})

		appManager := NewAppManager(client, testNamespace)
		found, err := appManager.ValidiateSideCar(testApp)

		assert.NoError(t, err)
		assert.True(t, found)
	})

	t.Run("Sidecar is not injected", func(t *testing.T) {
		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"list",
			"pods",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				singlePod := apiv1.Pod{
					ObjectMeta: objMeta,
					Spec: apiv1.PodSpec{
						Containers: []apiv1.Container{
							{
								Name:  testApp.AppName,
								Image: fmt.Sprintf("%s/%s", testApp.RegistryName, testApp.ImageName),
							},
						},
					},
				}

				podList := &apiv1.PodList{
					Items: []apiv1.Pod{singlePod},
				}

				return true, podList, nil
			})

		appManager := NewAppManager(client, testNamespace)
		found, err := appManager.ValidiateSideCar(testApp)
		assert.False(t, found)
		assert.Error(t, err)
	})

	t.Run("Pod is not found", func(t *testing.T) {
		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"list",
			"pods",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				podList := &apiv1.PodList{
					Items: []apiv1.Pod{},
				}

				return true, podList, nil
			})

		appManager := NewAppManager(client, testNamespace)
		found, err := appManager.ValidiateSideCar(testApp)
		assert.False(t, found)
		assert.Error(t, err)
	})
}

func TestCreateIngressService(t *testing.T) {
	testApp := testAppDescription()

	t.Run("Ingress is disabled", func(t *testing.T) {
		client := newDefaultFakeClient()
		appManager := NewAppManager(client, testNamespace)

		testApp.IngressEnabled = false
		_, err := appManager.CreateIngressService(testApp)
		assert.NoError(t, err)
		// assert
		serviceClient := client.Services(testNamespace)
		obj, _ := serviceClient.Get(testApp.AppName, metav1.GetOptions{})
		assert.NotNil(t, obj)
		assert.Equal(t, testApp.AppName, obj.ObjectMeta.Name)
		assert.Equal(t, testNamespace, obj.ObjectMeta.Namespace)
		assert.Equal(t, apiv1.ServiceTypeClusterIP, obj.Spec.Type)
	})

	t.Run("Ingress is enabled", func(t *testing.T) {
		client := newDefaultFakeClient()
		appManager := NewAppManager(client, testNamespace)

		testApp.IngressEnabled = true
		_, err := appManager.CreateIngressService(testApp)
		assert.NoError(t, err)
		// assert
		serviceClient := client.Services(testNamespace)
		obj, _ := serviceClient.Get(testApp.AppName, metav1.GetOptions{})
		assert.NotNil(t, obj)
		assert.Equal(t, testApp.AppName, obj.ObjectMeta.Name)
		assert.Equal(t, testNamespace, obj.ObjectMeta.Namespace)
		assert.Equal(t, apiv1.ServiceTypeLoadBalancer, obj.Spec.Type)
	})
}

func TestWaitUntilServiceStateAndGetExternalURL(t *testing.T) {
	// fake test values
	fakeMinikubeNodeIP := "192.168.0.12"
	fakeNodePort := int32(4000)
	fakeExternalIP := "10.10.10.100"
	testApp := testAppDescription()

	// Set fake minikube node IP address
	oldMinikubeIP := os.Getenv(MiniKubeIPEnvVar)

	t.Run("Minikube environment", func(t *testing.T) {
		os.Setenv(MiniKubeIPEnvVar, fakeMinikubeNodeIP)

		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"get",
			"services",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)
				obj := &apiv1.Service{
					Spec: apiv1.ServiceSpec{
						Ports: []apiv1.ServicePort{
							{
								NodePort: fakeNodePort,
							},
						},
					},
				}
				return true, obj, nil
			})

		appManager := NewAppManager(client, testNamespace)
		svcObj, err := appManager.WaitUntilServiceState(testApp, appManager.IsServiceIngressReady)
		assert.NoError(t, err)

		externalURL := appManager.AcquireExternalURLFromService(svcObj)
		assert.Equal(t, externalURL, fmt.Sprintf("%s:%d", fakeMinikubeNodeIP, fakeNodePort))
	})

	t.Run("Kubernetes environment", func(t *testing.T) {
		getVerbCalled := 0
		os.Setenv(MiniKubeIPEnvVar, "")

		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"get",
			"services",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)

				obj := &apiv1.Service{
					Spec: apiv1.ServiceSpec{
						ExternalIPs: []string{},
					},
				}

				if getVerbCalled == 2 {
					obj.Spec.ExternalIPs = []string{fakeExternalIP}
				} else {
					getVerbCalled++
				}

				return true, obj, nil
			})

		appManager := NewAppManager(client, testNamespace)
		svcObj, err := appManager.WaitUntilServiceState(testApp, appManager.IsServiceIngressReady)
		assert.NoError(t, err)

		externalURL := appManager.AcquireExternalURLFromService(svcObj)
		assert.Equal(t, fakeExternalIP, externalURL)
		assert.Equal(t, 2, getVerbCalled)
	})

	// Recover minikube ip environment variable
	os.Setenv(MiniKubeIPEnvVar, oldMinikubeIP)
}

func TestWaitUntilServiceStateDeleted(t *testing.T) {
	// fake test values
	testApp := testAppDescription()
	client := newFakeKubeClient()
	// Set up reactor to fake verb
	client.ClientSet.(*fake.Clientset).AddReactor(
		"get",
		"services",
		func(action core.Action) (bool, runtime.Object, error) {
			ns := action.GetNamespace()
			assert.Equal(t, testNamespace, ns)
			err := errors.NewNotFound(
				schema.GroupResource{
					Group:    "fakeGroup",
					Resource: "fakeResource",
				},
				"services")

			return true, nil, err
		})

	appManager := NewAppManager(client, testNamespace)
	svcObj, err := appManager.WaitUntilServiceState(testApp, appManager.IsServiceDeleted)
	assert.NoError(t, err)
	assert.Nil(t, svcObj)
}

func TestDeleteDeployment(t *testing.T) {
	testApp := testAppDescription()

	testSets := []struct {
		tc         string
		actionFunc func(action core.Action) (bool, runtime.Object, error)
	}{
		{
			"deployment object exists",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)
				obj := &appsv1.Deployment{}
				return true, obj, nil
			},
		},
		{
			"deployment object exists",
			func(action core.Action) (bool, runtime.Object, error) {
				err := errors.NewNotFound(
					schema.GroupResource{
						Group:    "fakeGroup",
						Resource: "fakeResource",
					},
					"deployments")

				return true, nil, err
			},
		},
	}

	for _, tt := range testSets {
		t.Run(tt.tc, func(t *testing.T) {
			client := newFakeKubeClient()
			// Set up reactor to fake verb
			client.ClientSet.(*fake.Clientset).AddReactor("delete", "deployments", tt.actionFunc)
			appManager := NewAppManager(client, testNamespace)
			err := appManager.DeleteDeployment(testApp, false)
			assert.NoError(t, err)
		})
	}
}

func TestDeleteService(t *testing.T) {
	testApp := testAppDescription()

	testSets := []struct {
		tc         string
		actionFunc func(action core.Action) (bool, runtime.Object, error)
	}{
		{
			"Service object exists",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, testNamespace, ns)
				obj := &apiv1.Service{}
				return true, obj, nil
			},
		},
		{
			"Service object does not exist",
			func(action core.Action) (bool, runtime.Object, error) {
				err := errors.NewNotFound(
					schema.GroupResource{
						Group:    "fakeGroup",
						Resource: "fakeResource",
					},
					"service")

				return true, nil, err
			},
		},
	}

	for _, tt := range testSets {
		t.Run(tt.tc, func(t *testing.T) {
			client := newFakeKubeClient()
			// Set up reactor to fake verb
			client.ClientSet.(*fake.Clientset).AddReactor("delete", "services", tt.actionFunc)
			appManager := NewAppManager(client, testNamespace)
			err := appManager.DeleteService(testApp, false)

			assert.NoError(t, err)
		})
	}
}
