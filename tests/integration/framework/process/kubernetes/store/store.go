/*
Copyright 2023 The Dapr Authors
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

package store

import (
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Store is a fake Kubernetes store for a resource type.
type Store struct {
	lock sync.RWMutex
	objs map[string]client.Object
	gvk  metav1.GroupVersionKind
}

func New(gvk metav1.GroupVersionKind) *Store {
	return &Store{
		gvk:  gvk,
		objs: make(map[string]client.Object),
	}
}

func (s *Store) Add(objs ...client.Object) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.objs == nil {
		s.objs = make(map[string]client.Object)
	}
	for _, obj := range objs {
		s.objs[obj.GetNamespace()+"/"+obj.GetName()] = obj
	}
}

func (s *Store) Set(objs ...client.Object) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.objs = make(map[string]client.Object)
	for _, obj := range objs {
		s.objs[obj.GetNamespace()+"/"+obj.GetName()] = obj
	}
}

func (s *Store) Objects() map[string]any {
	s.lock.RLock()
	defer s.lock.RUnlock()
	objs := make([]client.Object, 0, len(s.objs))
	for _, obj := range s.objs {
		objs = append(objs, obj)
	}

	if len(objs) == 0 {
		objs = nil
	}

	return map[string]any{
		"apiVersion": s.gvk.Group + "/" + s.gvk.Version,
		"kind":       s.gvk.Kind + "List",
		"items":      objs,
	}
}
