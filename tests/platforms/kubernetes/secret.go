/*
Copyright 2021 The Dapr Authors
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

package kubernetes

import (
	"context"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretDescription struct {
	Name      string
	Namespace string
	Data      map[string][]byte
}

// Secret holds kubernetes client and component information.
type Secret struct {
	name       string
	namespace  string
	kubeClient *KubeClient
	data       map[string][]byte
}

// NewSecret creates Secret instance.
func NewSecret(client *KubeClient, ns, name string, data map[string][]byte) *Secret {
	return &Secret{
		name:       name,
		namespace:  ns,
		kubeClient: client,
		data:       data,
	}
}

func (s *Secret) Init(ctx context.Context) error {
	log.Printf("Adding secret %q ...", s.name)
	if err := s.Dispose(false); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	_, err := s.kubeClient.ClientSet.CoreV1().Secrets(s.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
		},
		Data: s.data,
	}, metav1.CreateOptions{})

	return err
}

func (s *Secret) Name() string {
	return s.name
}

func (s *Secret) Dispose(wait bool) error {
	log.Printf("Delete secret %q ...", s.name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	_, err := s.kubeClient.ClientSet.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.kubeClient.ClientSet.CoreV1().Secrets(s.namespace).Delete(context.TODO(), s.name, metav1.DeleteOptions{})
}
