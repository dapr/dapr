// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"

	"github.com/dapr/dapr/tests/utils"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// DaprTestKubeNameSpace is the default Kubernetes namespace for e2e test
	DaprTestKubeNameSpace = "dapr-tests"

	// TestAppLabelKey is the label key for Kubernetes label selector
	TestAppLabelKey = "testapp"
	// DaprSideCarName is the Pod name of Dapr side car
	DaprSideCarName = "daprd"

	// DefaultContainerPort is the default container port exposed from test app
	DefaultContainerPort = 3000
	// DefaultExternalPort is the default external port exposed by load balancer ingress
	DefaultExternalPort = 3000
)

// BuildDeploymentObject creates the Kubernetes Deployment object for dapr test app
func BuildDeploymentObject(namespace string, appDesc utils.AppDescription) *appsv1.Deployment {
	annotationObject := map[string]string{}

	if appDesc.DaprEnabled {
		annotationObject = map[string]string{
			"dapr.io/enabled": "true",
			"dapr.io/id":      appDesc.AppName,
			"dapr.io/port":    fmt.Sprintf("%d", DefaultContainerPort),
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appDesc.AppName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(appDesc.Replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					TestAppLabelKey: appDesc.AppName,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						TestAppLabelKey: appDesc.AppName,
					},
					Annotations: annotationObject,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  appDesc.AppName,
							Image: fmt.Sprintf("%s/%s", appDesc.RegistryName, appDesc.ImageName),
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: DefaultContainerPort,
								},
							},
						},
					},
				},
			},
		},
	}
}

// BuildServiceObject creates the Kubernetes Service Object for dapr test app
func BuildServiceObject(namespace string, appDesc utils.AppDescription) *apiv1.Service {
	serviceType := apiv1.ServiceTypeClusterIP

	if appDesc.IngressEnabled {
		serviceType = apiv1.ServiceTypeLoadBalancer
	}

	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appDesc.AppName,
			Namespace: namespace,
			Labels: map[string]string{
				TestAppLabelKey: appDesc.AppName,
			},
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				TestAppLabelKey: appDesc.AppName,
			},
			Ports: []apiv1.ServicePort{
				{
					Protocol:   apiv1.ProtocolTCP,
					Port:       DefaultExternalPort,
					TargetPort: intstr.IntOrString{IntVal: DefaultContainerPort},
				},
			},
			Type: serviceType,
		},
	}
}

func int32Ptr(i int32) *int32 {
	return &i
}
