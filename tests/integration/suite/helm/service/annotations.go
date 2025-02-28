/*
Copyright 2024 The Dapr Authors
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

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(annotations))
}

type annotations struct {
	base             *helm.Helm
	withCustom       *helm.Helm
	withNoProm       *helm.Helm
	withCustomNoProm *helm.Helm
}

func (a *annotations) Setup(t *testing.T) []framework.Option {
	a.base = helm.New(t,
		helm.WithShowOnlyServices(t),
	)

	a.withCustom = helm.New(t,
		helm.WithShowOnlyServices(t),
		helm.WithValues(
			"dapr_sentry.service.annotations.foo=sentry",
			"dapr_sentry.service.annotations.123=456",
			"dapr_operator.apiService.annotations.foo=operator",
			"dapr_operator.apiService.annotations.123=456",
			"dapr_operator.webhookService.annotations.foo=operator",
			"dapr_operator.webhookService.annotations.123=456",
			"dapr_scheduler.service.annotations.foo=scheduler",
			"dapr_scheduler.service.annotations.123=456",
			"dapr_placement.service.annotations.foo=placement",
			"dapr_placement.service.annotations.123=456",
			"dapr_sidecar_injector.service.annotations.foo=sidecar_injector",
			"dapr_sidecar_injector.service.annotations.123=456",
		),
	)

	a.withCustomNoProm = helm.New(t,
		helm.WithShowOnlyServices(t),
		helm.WithGlobalValues(
			"prometheus.enabled=false",
		),
		helm.WithValues(
			"dapr_sentry.service.annotations.foo=sentry",
			"dapr_sentry.service.annotations.123=456",
			"dapr_operator.apiService.annotations.foo=operator",
			"dapr_operator.apiService.annotations.123=456",
			"dapr_operator.webhookService.annotations.foo=operator",
			"dapr_operator.webhookService.annotations.123=456",
			"dapr_scheduler.service.annotations.foo=scheduler",
			"dapr_scheduler.service.annotations.123=456",
			"dapr_placement.service.annotations.foo=placement",
			"dapr_placement.service.annotations.123=456",
			"dapr_sidecar_injector.service.annotations.foo=sidecar_injector",
			"dapr_sidecar_injector.service.annotations.123=456",
		),
	)

	a.withNoProm = helm.New(t,
		helm.WithShowOnlyServices(t),
		helm.WithGlobalValues(
			"prometheus.enabled=false",
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.base, a.withCustom, a.withCustomNoProm, a.withNoProm),
	}
}

func (a *annotations) Run(t *testing.T, ctx context.Context) {
	type nameAnnotations struct {
		name        string
		annotations map[string]string
	}

	t.Run("default", func(t *testing.T) {
		svcs := helm.UnmarshalStdout[corev1.Service](t, a.base)
		assert.Len(t, svcs, 7)
		for _, svc := range svcs {
			if svc.Name == "dapr-webhook" {
				assert.Empty(t, svc.Annotations, svc.Name)
			} else {
				assert.Equal(t, map[string]string{
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				}, svc.Annotations, svc.Name)
			}
		}
	})

	t.Run("custom", func(t *testing.T) {
		exp := []nameAnnotations{
			{
				name: "dapr-sentry",
				annotations: map[string]string{
					"foo":                  "sentry",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
			{
				name: "dapr-api",
				annotations: map[string]string{
					"foo":                  "operator",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
			{
				name: "dapr-scheduler-server",
				annotations: map[string]string{
					"foo":                  "scheduler",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
			{
				name: "dapr-placement-server",
				annotations: map[string]string{
					"foo":                  "placement",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
			{
				name: "dapr-sidecar-injector",
				annotations: map[string]string{
					"foo":                  "sidecar_injector",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
			{
				name: "dapr-webhook",
				annotations: map[string]string{
					"foo": "operator",
					"123": "456",
				},
			},
			{
				name: "dapr-scheduler-server-a",
				annotations: map[string]string{
					"foo":                  "scheduler",
					"123":                  "456",
					"prometheus.io/path":   "/",
					"prometheus.io/port":   "9090",
					"prometheus.io/scrape": "true",
				},
			},
		}
		svcs := helm.UnmarshalStdout[corev1.Service](t, a.withCustom)
		assert.Len(t, svcs, 7)
		got := make([]nameAnnotations, 0, len(svcs))
		for _, svc := range svcs {
			got = append(got, nameAnnotations{
				name:        svc.Name,
				annotations: svc.Annotations,
			})
		}
		assert.ElementsMatch(t, exp, got)
	})

	t.Run("custom without prometheus", func(t *testing.T) {
		exp := []nameAnnotations{
			{
				name: "dapr-sentry",
				annotations: map[string]string{
					"foo": "sentry",
					"123": "456",
				},
			},
			{
				name: "dapr-api",
				annotations: map[string]string{
					"foo": "operator",
					"123": "456",
				},
			},
			{
				name: "dapr-scheduler-server",
				annotations: map[string]string{
					"foo": "scheduler",
					"123": "456",
				},
			},
			{
				name: "dapr-placement-server",
				annotations: map[string]string{
					"foo": "placement",
					"123": "456",
				},
			},
			{
				name: "dapr-sidecar-injector",
				annotations: map[string]string{
					"foo": "sidecar_injector",
					"123": "456",
				},
			},
			{
				name: "dapr-webhook",
				annotations: map[string]string{
					"foo": "operator",
					"123": "456",
				},
			},
			{
				name: "dapr-scheduler-server-a",
				annotations: map[string]string{
					"foo": "scheduler",
					"123": "456",
				},
			},
		}

		svcs := helm.UnmarshalStdout[corev1.Service](t, a.withCustomNoProm)
		assert.Len(t, svcs, 7)
		got := make([]nameAnnotations, 0, len(svcs))
		for _, svc := range svcs {
			got = append(got, nameAnnotations{
				name:        svc.Name,
				annotations: svc.Annotations,
			})
		}
		assert.ElementsMatch(t, exp, got)
	})

	t.Run("without prometheus", func(t *testing.T) {
		exp := []nameAnnotations{
			{
				name:        "dapr-sentry",
				annotations: nil,
			},
			{
				name:        "dapr-api",
				annotations: nil,
			},
			{
				name:        "dapr-scheduler-server",
				annotations: nil,
			},
			{
				name:        "dapr-placement-server",
				annotations: nil,
			},
			{
				name:        "dapr-sidecar-injector",
				annotations: nil,
			},
			{
				name:        "dapr-webhook",
				annotations: nil,
			},
			{
				name:        "dapr-scheduler-server-a",
				annotations: nil,
			},
		}

		svcs := helm.UnmarshalStdout[corev1.Service](t, a.withNoProm)
		assert.Len(t, svcs, 7)
		got := make([]nameAnnotations, 0, len(svcs))
		for _, svc := range svcs {
			got = append(got, nameAnnotations{
				name:        svc.Name,
				annotations: svc.Annotations,
			})
		}
		assert.ElementsMatch(t, exp, got)
	})
}
