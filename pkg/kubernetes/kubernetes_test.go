// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"testing"

	versioned "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	fake "k8s.io/client-go/kubernetes/fake"
)

const (
	name        = "test"
	namespace   = "testns"
	label       = "testlabel"
	servicename = "testservice"
)

func TestNewAPI(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	assert.True(t, kubeAPI != nil)
}

func TestGetDeployment(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)

	_, err := fakeClient.AppsV1().Deployments(d.ObjectMeta.Namespace).Create(d)
	assert.Equal(t, nil, err)

	// test
	_, err = kubeAPI.GetDeployment(name, namespace)

	assert.Equal(t, nil, err)

	_, err = fakeClient.AppsV1().Deployments(namespace).Get(name, metav1.GetOptions{})
	assert.Equal(t, nil, err)
}

func TestUpdateDeployment(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)
	fakeClient.AppsV1().Deployments(d.ObjectMeta.Namespace).Create(d)

	err := kubeAPI.UpdateDeployment(d)

	assert.Equal(t, nil, err)
}

func TestCreateService(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)
	fakeClient.AppsV1().Deployments(d.ObjectMeta.Namespace).Create(d)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: servicename,
		},
		Spec: corev1.ServiceSpec{
			Selector: d.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(3500),
					Name:       "http",
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       81,
					TargetPort: intstr.FromInt(5001),
					Name:       "grpc",
				},
			},
		},
	}

	err := kubeAPI.CreateService(service, namespace)
	assert.Equal(t, nil, err)
}

func TestDeleteService(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ds",
		},
		Spec: corev1.ServiceSpec{
			Selector: d.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(3500),
					Name:       "http",
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       81,
					TargetPort: intstr.FromInt(5001),
					Name:       "grpc",
				},
			},
		},
	}

	err := kubeAPI.CreateService(service, namespace)
	assert.Equal(t, nil, err)

	err = kubeAPI.DeleteService("ds", namespace)
	assert.Equal(t, nil, err)
}

func TestServiceExists(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "se",
		},
		Spec: corev1.ServiceSpec{
			Selector: d.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(3500),
					Name:       "http",
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       81,
					TargetPort: intstr.FromInt(5001),
					Name:       "grpc",
				},
			},
		},
	}

	err := kubeAPI.CreateService(service, namespace)
	assert.Equal(t, nil, err)

	b := kubeAPI.ServiceExists("se", namespace)
	assert.True(t, b)
}

func TestGetEndpoints(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	epname := "testep"
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name: epname,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "10.1.1.1",
					},
				},
			},
		},
	}

	_, err := fakeClient.CoreV1().Endpoints(namespace).Create(ep)

	assert.Equal(t, nil, err)
	_, err = kubeAPI.GetEndpoints(epname, namespace)
	assert.Equal(t, nil, err)
}

func TestGetDeploymentsBySelector(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := NewAPI(fakeClient, versioned.New(nil))
	d := getDeployment(name, namespace, label)
	fakeClient.AppsV1().Deployments(d.ObjectMeta.Namespace).Create(d)

	selector := metav1.LabelSelector{
		MatchLabels: map[string]string{label: label},
	}

	items, err := kubeAPI.GetDeploymentsBySelector(selector)
	assert.Equal(t, nil, err)
	assert.Equal(t, 1, len(items))
}

func getDeployment(name, namespace, label string) *appsv1.Deployment {
	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "podname",
			Labels: map[string]string{label: label},
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{label: label},
		},

		Spec: appsv1.DeploymentSpec{
			Template: podTemplateSpec,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{label: label},
			},
		},
	}

	return deployment
}
