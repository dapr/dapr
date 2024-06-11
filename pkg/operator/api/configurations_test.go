package api

import (
	"context"
	"encoding/json"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newCardinalityConfig(objectMeta v1.ObjectMeta, increaseCardinality bool) *configapi.Configuration {
	return &configapi.Configuration{
		ObjectMeta: objectMeta,
		Spec: configapi.ConfigurationSpec{
			MetricSpec: &configapi.MetricSpec{
				HTTP: &configapi.MetricHTTP{
					IncreasedCardinality: ptr.To(increaseCardinality),
				},
			},
		},
	}
}

func Test_overrideConfigDefaults(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, configapi.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	ctx := context.Background()
	defaultConfigObjKey.Namespace = "default"

	defaultObjMeta := v1.ObjectMeta{
		Name:      defaultConfig,
		Namespace: "default",
	}

	t.Run("if default configuration doesn't exist, return no error", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		a := &apiServer{Client: cl}
		assert.Nil(t, a.overrideConfigDefaults(ctx, &configapi.Configuration{}))
	})

	t.Run("if default configuration doesn't exist, and we provide a Configuration, nothing should change", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		a := &apiServer{Client: cl}
		c := newCardinalityConfig(defaultObjMeta, true)
		oldC := c.DeepCopy()
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))
		assertEqual(t, c, oldC)
	})

	t.Run("if default configuration exist, and we provide a Configuration with HTTP info, nothing should change", func(t *testing.T) {
		defaultC := newCardinalityConfig(defaultObjMeta, false)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultC).Build()
		a := &apiServer{Client: cl}
		c := &configapi.Configuration{
			Spec: configapi.ConfigurationSpec{
				MetricSpec: &configapi.MetricSpec{
					HTTP: &configapi.MetricHTTP{
						IncreasedCardinality: ptr.To(true),
					},
				},
			},
		}
		oldC := c.DeepCopy()
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))
		assertEqual(t, c, oldC)
	})

	t.Run("if default configuration exist, and we provide a Configuration with HTTP info inside Metric(s)Spec, nothing should change", func(t *testing.T) {
		defaultC := newCardinalityConfig(defaultObjMeta, false)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultC).Build()
		a := &apiServer{Client: cl}
		c := &configapi.Configuration{
			Spec: configapi.ConfigurationSpec{
				MetricsSpec: &configapi.MetricSpec{
					HTTP: &configapi.MetricHTTP{
						IncreasedCardinality: ptr.To(true),
					},
				},
			},
		}
		oldC := c.DeepCopy()
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))
		assertEqual(t, c, oldC)
	})

	t.Run("if default configuration exist, and we provide a Configuration with NO HTTP info, use default config", func(t *testing.T) {
		defaultC := newCardinalityConfig(defaultObjMeta, false)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultC).Build()
		a := &apiServer{Client: cl}
		c := &configapi.Configuration{
			Spec: configapi.ConfigurationSpec{
				MetricSpec: &configapi.MetricSpec{},
			},
		}
		expectedC := c.DeepCopy()
		expectedC.Spec.MetricSpec.HTTP = &configapi.MetricHTTP{
			IncreasedCardinality: ptr.To(false),
		}
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))

		assertEqual(t, c, expectedC)
	})

	t.Run("if default configuration exist using metric instead of plural metrics, and we provide a Configuration with NO HTTP info, use default config", func(t *testing.T) {
		defaultC := newCardinalityConfig(defaultObjMeta, false)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultC).Build()
		a := &apiServer{Client: cl}
		c := &configapi.Configuration{
			Spec: configapi.ConfigurationSpec{
				MetricSpec: &configapi.MetricSpec{},
			},
		}
		expectedC := c.DeepCopy()
		expectedC.Spec.MetricSpec.HTTP = &configapi.MetricHTTP{
			IncreasedCardinality: ptr.To(false),
		}
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))

		assertEqual(t, c, expectedC)
	})

	t.Run("if default configuration exist, and we provide a Configuration with NO HTTP info, but provide other spec attributes, use default config only for HTTP", func(t *testing.T) {
		defaultC := newCardinalityConfig(defaultObjMeta, false)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultC).Build()
		a := &apiServer{Client: cl}
		c := &configapi.Configuration{
			Spec: configapi.ConfigurationSpec{
				MetricSpec: &configapi.MetricSpec{},
				TracingSpec: &configapi.TracingSpec{
					SamplingRate: "samplingrate",
				},
			},
		}
		expectedC := c.DeepCopy()
		expectedC.Spec.MetricSpec.HTTP = &configapi.MetricHTTP{
			IncreasedCardinality: ptr.To(false),
		}
		assert.Nil(t, a.overrideConfigDefaults(ctx, c))

		assertEqual(t, c, expectedC)
	})
}

func assertEqual(t *testing.T, c, cExpected *configapi.Configuration) {
	t.Helper()
	expectedCData, err := json.Marshal(cExpected)
	require.NoError(t, err)
	cData, err := json.Marshal(c)
	require.NoError(t, err)

	assert.Equal(t, cData, expectedCData)
}
