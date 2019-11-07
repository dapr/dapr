// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
)

const testNamespace = "apputil-test"

func newTestKubeClient() *KubeClient {
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

func testAppDescription() AppDescription {
	return AppDescription{
		AppName:        "testapp",
		DaprEnabled:    true,
		ImageName:      "helloworld",
		RegistryName:   "dapriotest",
		Replicas:       1,
		IngressEnabled: true,
	}
}

func TestDeployApp(t *testing.T) {
	client := newTestKubeClient()
	appUtil := NewAppUtils(client, testNamespace)

	testApp := testAppDescription()

	// act
	err := appUtil.DeployApp(testApp)
	assert.NoError(t, err)

	// assert
	deploymentClient := client.Deployments(testNamespace)
	deployment, _ := deploymentClient.Get(testApp.AppName, metav1.GetOptions{})
	assert.NotNil(t, deployment)
	assert.Equal(t, deployment.ObjectMeta.Name, testApp.AppName)
	assert.Equal(t, deployment.ObjectMeta.Namespace, testNamespace)
	assert.Equal(t, *deployment.Spec.Replicas, int32(1))
	assert.Equal(t, deployment.Spec.Selector.MatchLabels["testapp"], testApp.AppName)
	assert.Equal(t, deployment.Spec.Template.ObjectMeta.Annotations["dapr.io/enabled"], "true")
	assert.Equal(t, deployment.Spec.Template.Spec.Containers[0].Name, testApp.AppName)
	assert.Equal(t, deployment.Spec.Template.Spec.Containers[0].Image, "dapriotest/helloworld")
}

func TestWaitUntilDeploymentReady(t *testing.T) {
	client := newFakeKubeClient()
	testApp := testAppDescription()
	var createdDeploymentObj *appsv1.Deployment
	getVerbCalled := 0

	// Set up reactor to fake verb
	client.ClientSet.(*fake.Clientset).AddReactor(
		"*",
		"deployments",
		func(action core.Action) (bool, runtime.Object, error) {
			ns := action.GetNamespace()
			assert.Equal(t, ns, testNamespace)

			switch action.GetVerb() {
			case "create":
				// return the same deployment object
				createdDeploymentObj = action.(core.CreateAction).GetObject().(*appsv1.Deployment)
				break
			case "get":
				// set 1 to ReadyReplicas when WaitUntilDeploymentReady called get deployments 3 times
				if getVerbCalled == 3 {
					createdDeploymentObj.Status.ReadyReplicas = testApp.Replicas
				} else {
					getVerbCalled = getVerbCalled + 1
				}
				break
			}
			return true, createdDeploymentObj, nil
		})

	appUtil := NewAppUtils(client, testNamespace)

	// act
	err := appUtil.DeployApp(testApp)
	assert.NoError(t, err)

	// assert
	d, err := appUtil.WaitUntilDeploymentReady(testApp)
	assert.NoError(t, err)
	assert.Equal(t, testApp.Replicas, d.Status.ReadyReplicas)
	assert.Equal(t, 3, getVerbCalled)
}

func TestValdiateDaprSideCar(t *testing.T) {
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
				assert.Equal(t, ns, testNamespace)

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

		appUtil := NewAppUtils(client, testNamespace)
		found, err := appUtil.ValdiateDaprSideCar(testApp)

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
				assert.Equal(t, ns, testNamespace)

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

		appUtil := NewAppUtils(client, testNamespace)
		found, err := appUtil.ValdiateDaprSideCar(testApp)
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
				assert.Equal(t, ns, testNamespace)

				podList := &apiv1.PodList{
					Items: []apiv1.Pod{},
				}

				return true, podList, nil
			})

		appUtil := NewAppUtils(client, testNamespace)
		found, err := appUtil.ValdiateDaprSideCar(testApp)
		assert.False(t, found)
		assert.Error(t, err)
	})
}

func TestCreateIngressService(t *testing.T) {
	client := newTestKubeClient()

	appUtil := NewAppUtils(client, testNamespace)
	testApp := testAppDescription()

	t.Run("Ingress is disabled", func(t *testing.T) {
		testApp.IngressEnabled = false
		err := appUtil.CreateIngressService(testApp)
		assert.Error(t, err)
	})

	t.Run("Ingress is enabled", func(t *testing.T) {
		testApp.IngressEnabled = true
		err := appUtil.CreateIngressService(testApp)
		assert.NoError(t, err)
		// assert
		serviceClient := client.Services(testNamespace)
		obj, _ := serviceClient.Get(testApp.AppName, metav1.GetOptions{})
		assert.NotNil(t, obj)
		assert.Equal(t, testApp.AppName, obj.ObjectMeta.Name)
		assert.Equal(t, testNamespace, obj.ObjectMeta.Namespace)
	})
}

func TestWaitUntilIngressEndpointIsAvailable(t *testing.T) {
	// fake test values
	fakeMinikubeNodeIP := "192.168.0.12"
	fakeNodePort := int32(4000)
	fakeExternalIP := "10.10.10.100"
	testApp := testAppDescription()

	// Set fake minikube node IP address
	oldMinikubeIP := os.Getenv(MiniKubeIPEnv)

	t.Run("Minikube environment", func(t *testing.T) {
		os.Setenv(MiniKubeIPEnv, fakeMinikubeNodeIP)

		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"get",
			"services",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, ns, testNamespace)
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

		appUtil := NewAppUtils(client, testNamespace)
		externalURL, err := appUtil.WaitUntilIngressEndpointIsAvailable(testApp)

		assert.NoError(t, err)
		assert.Equal(t, externalURL, fmt.Sprintf("%s:%d", fakeMinikubeNodeIP, fakeNodePort))
	})

	t.Run("Kubernetes environment", func(t *testing.T) {
		getVerbCalled := 0
		os.Setenv(MiniKubeIPEnv, "")

		client := newFakeKubeClient()
		// Set up reactor to fake verb
		client.ClientSet.(*fake.Clientset).AddReactor(
			"get",
			"services",
			func(action core.Action) (bool, runtime.Object, error) {
				ns := action.GetNamespace()
				assert.Equal(t, ns, testNamespace)
				obj := &apiv1.Service{
					Spec: apiv1.ServiceSpec{
						ExternalIPs: []string{},
					},
				}

				if getVerbCalled == 3 {
					obj.Spec.ExternalIPs = []string{fakeExternalIP}
				} else {
					getVerbCalled = getVerbCalled + 1
				}

				return true, obj, nil
			})

		appUtil := NewAppUtils(client, testNamespace)
		externalURL, err := appUtil.WaitUntilIngressEndpointIsAvailable(testApp)

		assert.NoError(t, err)
		assert.Equal(t, externalURL, fmt.Sprintf("%s", fakeExternalIP))
		assert.Equal(t, 3, getVerbCalled)
	})

	// Recover minikube ip environment variable
	os.Setenv(MiniKubeIPEnv, oldMinikubeIP)
}

func TestDeleteDeployment(t *testing.T) {
	testApp := testAppDescription()
	client := newFakeKubeClient()
	// Set up reactor to fake verb
	client.ClientSet.(*fake.Clientset).AddReactor(
		"delete",
		"deployments",
		func(action core.Action) (bool, runtime.Object, error) {
			ns := action.GetNamespace()
			assert.Equal(t, ns, testNamespace)
			obj := &appsv1.Deployment{}
			return true, obj, nil
		})

	appUtil := NewAppUtils(client, testNamespace)
	err := appUtil.DeleteDeployment(testApp)

	assert.NoError(t, err)
}

func TestDeleteService(t *testing.T) {
	testApp := testAppDescription()
	client := newFakeKubeClient()
	// Set up reactor to fake verb
	client.ClientSet.(*fake.Clientset).AddReactor(
		"delete",
		"services",
		func(action core.Action) (bool, runtime.Object, error) {
			ns := action.GetNamespace()
			assert.Equal(t, ns, testNamespace)
			obj := &apiv1.Service{}
			return true, obj, nil
		})

	appUtil := NewAppUtils(client, testNamespace)
	err := appUtil.DeleteService(testApp)

	assert.NoError(t, err)
}
