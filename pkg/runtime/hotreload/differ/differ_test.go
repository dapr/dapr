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

package differ

import (
	"testing"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
)

func Test_toComparableObj(t *testing.T) {
	t.Parallel()

	const numCases = 500
	components := make([]compapi.Component, numCases)
	endpoints := make([]httpendapi.HTTPEndpoint, numCases)

	fz := fuzz.New()
	for i := 0; i < numCases; i++ {
		fz.Fuzz(&components[i])
		fz.Fuzz(&endpoints[i])
	}

	for i := 0; i < numCases; i++ {
		t.Run("Component", func(t *testing.T) {
			compWithoutObject := components[i].DeepCopy()
			compWithoutObject.ObjectMeta = metav1.ObjectMeta{
				Name: components[i].Name,
			}
			compWithoutObject.TypeMeta = metav1.TypeMeta{
				Kind: "Component", APIVersion: "dapr.io/v1alpha1",
			}
			assert.Equal(t, compWithoutObject, toComparableObj[compapi.Component](components[i]))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			endpointWithoutObject := endpoints[i].DeepCopy()
			endpointWithoutObject.ObjectMeta = metav1.ObjectMeta{
				Name: endpoints[i].Name,
			}
			endpointWithoutObject.TypeMeta = metav1.TypeMeta{
				Kind: "HTTPEndpoint", APIVersion: "dapr.io/v1alpha1",
			}
			assert.Equal(t, endpointWithoutObject, toComparableObj[httpendapi.HTTPEndpoint](endpoints[i]))
		})
	}
}

func Test_areSame(t *testing.T) {
	t.Parallel()

	const numCases = 250
	components := make([]compapi.Component, numCases)
	endpoints := make([]httpendapi.HTTPEndpoint, numCases)
	componentsDiff := make([]compapi.Component, numCases)
	endpointsDiff := make([]httpendapi.HTTPEndpoint, numCases)

	fz := fuzz.New()
	for i := 0; i < numCases; i++ {
		fz.Fuzz(&components[i])
		fz.Fuzz(&endpoints[i])
		fz.Fuzz(&componentsDiff[i])
		fz.Fuzz(&endpointsDiff[i])
	}

	for i := 0; i < numCases; i++ {
		t.Run("Exact same resource should always return true", func(t *testing.T) {
			t.Run("Component", func(t *testing.T) {
				comp1 := components[i]
				comp2 := comp1.DeepCopy()
				assert.True(t, areSame[compapi.Component](comp1, *comp2))
			})
			t.Run("HTTPEndpoint", func(t *testing.T) {
				endpoint1 := endpoints[i]
				endpoint2 := endpoint1.DeepCopy()
				assert.True(t, areSame[httpendapi.HTTPEndpoint](endpoint1, *endpoint2))
			})
		})

		t.Run("Same resource but with different Object&Type meta (same name) should return true", func(t *testing.T) {
			t.Run("Component", func(t *testing.T) {
				comp1 := components[i]
				comp2 := comp1.DeepCopy()
				fz.Fuzz(&comp2.ObjectMeta)
				fz.Fuzz(&comp2.TypeMeta)
				comp2.Name = comp1.Name
				assert.True(t, areSame[compapi.Component](comp1, *comp2))
			})
			t.Run("HTTPEndpoint", func(t *testing.T) {
				endpoint1 := endpoints[i]
				endpoint2 := endpoint1.DeepCopy()
				fz.Fuzz(&endpoint2.ObjectMeta)
				fz.Fuzz(&endpoint2.TypeMeta)
				endpoint2.Name = endpoint1.Name
				assert.True(t, areSame[httpendapi.HTTPEndpoint](endpoint1, *endpoint2))
			})
		})

		t.Run("Different resources should return false", func(t *testing.T) {
			t.Run("Component", func(t *testing.T) {
				comp1 := components[i]
				comp2 := componentsDiff[i]
				assert.False(t, areSame[compapi.Component](comp1, comp2))
			})
			t.Run("HTTPEndpoint", func(t *testing.T) {
				endpoint1 := endpoints[i]
				endpoint2 := endpointsDiff[i]
				assert.False(t, areSame[httpendapi.HTTPEndpoint](endpoint1, endpoint2))
			})
		})
	}
}

func Test_detectDiff(t *testing.T) {
	t.Parallel()

	const numCases = 100
	components := make([]compapi.Component, numCases)
	endpoints := make([]httpendapi.HTTPEndpoint, numCases)
	componentsDiff := make([]compapi.Component, numCases)
	endpointsDiff := make([]httpendapi.HTTPEndpoint, numCases)

	fz := fuzz.New()
	for i := 0; i < numCases; i++ {
		fz.Fuzz(&components[i])
		fz.Fuzz(&endpoints[i])
		fz.Fuzz(&componentsDiff[i])
		fz.Fuzz(&endpointsDiff[i])
	}

	t.Run("If resources are the same then expect a map of the same resources returned", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			expSameComponents := make(map[string]compapi.Component)
			assert.Equal(t, expSameComponents, detectDiff[compapi.Component](components, components, nil))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			expSameEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			assert.Equal(t, expSameEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, endpoints, nil))
		})
	})

	t.Run("If resources are the same with a check returning false then expect a map of the same resources returned", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			expSameComponents := make(map[string]compapi.Component)
			assert.Equal(t, expSameComponents, detectDiff[compapi.Component](components, components, func(compapi.Component) bool { return false }))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			expSameEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			assert.Equal(t, expSameEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, endpoints, func(httpendapi.HTTPEndpoint) bool { return false }))
		})
	})

	t.Run("If resources are the same with a check returning true then expect a map of the same resources returned", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			expSameComponents := make(map[string]compapi.Component)
			assert.Equal(t, expSameComponents, detectDiff[compapi.Component](components, components, func(compapi.Component) bool { return true }))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			expSameEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			assert.Equal(t, expSameEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, endpoints, func(httpendapi.HTTPEndpoint) bool { return true }))
		})
	})

	t.Run("Should return the different resources which don't exist in the target", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			expDiffComponents := make(map[string]compapi.Component)
			for i := 0; i < numCases; i++ {
				expDiffComponents[componentsDiff[i].Name] = componentsDiff[i]
			}
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](components, append(components, componentsDiff...), nil))
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](components, append(componentsDiff, components...), nil))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			expDiffEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			for i := 0; i < numCases; i++ {
				expDiffEndpoints[endpointsDiff[i].Name] = endpointsDiff[i]
			}
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, append(endpoints, endpointsDiff...), nil))
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, append(endpointsDiff, endpoints...), nil))
		})
	})

	t.Run("Should not return resources if they exist in base, but not the target", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			expDiffComponents := make(map[string]compapi.Component)
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](append(components, componentsDiff...), components, nil))
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](append(componentsDiff, components...), components, nil))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			expDiffEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](append(endpoints, endpointsDiff...), endpoints, nil))
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](append(endpointsDiff, endpoints...), endpoints, nil))
		})
	})

	t.Run("Should not return resources if they exist in the target and not base, but are skipped on check", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			t.Parallel()

			expDiffComponents := make(map[string]compapi.Component)
			var j int
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](components, append(components, componentsDiff...), func(c compapi.Component) bool {
				if j < len(components) {
					assert.Equal(t, components[j], c)
				} else {
					assert.Equal(t, componentsDiff[j-len(components)], c)
				}
				j++
				return true
			}))
			j = 0
			assert.Equal(t, expDiffComponents, detectDiff[compapi.Component](components, append(componentsDiff, components...), func(c compapi.Component) bool {
				if j < len(components) {
					assert.Equal(t, componentsDiff[j], c)
				} else {
					assert.Equal(t, components[j-len(components)], c)
				}
				j++
				return true
			}))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			t.Parallel()

			expDiffEndpoints := make(map[string]httpendapi.HTTPEndpoint)
			var j int
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, append(endpoints, endpointsDiff...), func(e httpendapi.HTTPEndpoint) bool {
				if j < len(endpoints) {
					assert.Equal(t, endpoints[j], e)
				} else {
					assert.Equal(t, endpointsDiff[j-len(endpoints)], e)
				}
				j++
				return true
			}))
			j = 0
			assert.Equal(t, expDiffEndpoints, detectDiff[httpendapi.HTTPEndpoint](endpoints, append(endpointsDiff, endpoints...), func(e httpendapi.HTTPEndpoint) bool {
				if j < len(endpoints) {
					assert.Equal(t, endpointsDiff[j], e)
				} else {
					assert.Equal(t, endpoints[j-len(endpoints)], e)
				}
				j++
				return true
			}))
		})
	})
}

func Test_Diff(t *testing.T) {
	t.Parallel()

	const numCases = 100

	components := make([]compapi.Component, numCases)
	componentsDiff1 := make([]compapi.Component, numCases)
	componentsDiff2 := make([]compapi.Component, numCases)

	endpoints := make([]httpendapi.HTTPEndpoint, numCases)
	endpointsDiff1 := make([]httpendapi.HTTPEndpoint, numCases)
	endpointsDiff2 := make([]httpendapi.HTTPEndpoint, numCases)

	takenNames := make(map[string]bool)
	forCh := func(name string) bool {
		ok := len(name) == 0 || takenNames[name]
		takenNames[name] = true
		return ok
	}

	fz := fuzz.New()
	for i := 0; i < numCases; i++ {
		for forCh(components[i].Name) {
			fz.Fuzz(&components[i])
		}
		for forCh(componentsDiff1[i].Name) {
			fz.Fuzz(&componentsDiff1[i])
		}
		for forCh(componentsDiff2[i].Name) {
			fz.Fuzz(&componentsDiff2[i])
		}
		for forCh(endpoints[i].Name) {
			fz.Fuzz(&endpoints[i])
		}
		for forCh(endpointsDiff1[i].Name) {
			fz.Fuzz(&endpointsDiff1[i])
		}
		for forCh(endpointsDiff2[i].Name) {
			fz.Fuzz(&endpointsDiff2[i])
		}
	}

	t.Run("if no resources given, return nil", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			assert.Nil(t, Diff[compapi.Component](nil))
			assert.Nil(t, Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  nil,
				Remote: nil,
			}))
		})

		t.Run("HTTPEndpoint", func(t *testing.T) {
			assert.Nil(t, Diff[httpendapi.HTTPEndpoint](nil))
			assert.Nil(t, Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  nil,
				Remote: nil,
			}))
		})
	})

	t.Run("if no remote, expect all local to be deleted", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  components,
				Remote: nil,
			})

			assert.ElementsMatch(t, components, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, nil, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  endpoints,
				Remote: nil,
			})
			assert.ElementsMatch(t, endpoints, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, nil, resp.Created)
		})
	})

	t.Run("if no local, expect all remote to be created", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  nil,
				Remote: components,
			})

			assert.ElementsMatch(t, nil, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, components, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  nil,
				Remote: endpoints,
			})
			assert.ElementsMatch(t, nil, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, endpoints, resp.Created)
		})
	})

	t.Run("if local and remote completely different, expect both created and deleted", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  componentsDiff1,
				Remote: components,
			})

			assert.ElementsMatch(t, componentsDiff1, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, components, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  endpointsDiff1,
				Remote: endpoints,
			})
			assert.ElementsMatch(t, endpointsDiff1, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, endpoints, resp.Created)
		})
	})

	t.Run("if local and remote share some resources, they should be omitted from the result", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  append(componentsDiff2, componentsDiff1...),
				Remote: append(components, componentsDiff2...),
			})

			assert.ElementsMatch(t, componentsDiff1, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, components, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  append(endpointsDiff2, endpointsDiff1...),
				Remote: append(endpoints, endpointsDiff2...),
			})
			assert.ElementsMatch(t, endpointsDiff1, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, endpoints, resp.Created)
		})
	})

	t.Run("should not mark components as deleted if they are in the reserved skipped set", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local: append(append(componentsDiff2, componentsDiff1...), []compapi.Component{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "kubernetes"},
						Spec:       compapi.ComponentSpec{Type: "secretstores.kubernetes"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "dapr"},
						Spec:       compapi.ComponentSpec{Type: "workflow.dapr"},
					},
				}...),
				Remote: append(components, componentsDiff2...),
			})

			assert.ElementsMatch(t, componentsDiff1, resp.Deleted)
			assert.ElementsMatch(t, nil, resp.Updated)
			assert.ElementsMatch(t, components, resp.Created)
		})
	})

	t.Run("if local and remote share the same names, then should be updated with remote", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			remote := make([]compapi.Component, len(components))
			for i := 0; i < len(components); i++ {
				comp := componentsDiff1[i].DeepCopy()
				comp.Name = components[i].Name
				remote[i] = *comp
			}

			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local:  components,
				Remote: remote,
			})

			assert.ElementsMatch(t, nil, resp.Deleted)
			assert.ElementsMatch(t, remote, resp.Updated)
			assert.ElementsMatch(t, nil, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			remote := make([]httpendapi.HTTPEndpoint, len(endpoints))
			for i := 0; i < len(endpoints); i++ {
				end := endpointsDiff1[i].DeepCopy()
				end.Name = endpoints[i].Name
				remote[i] = *end
			}

			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  endpoints,
				Remote: remote,
			})
			assert.ElementsMatch(t, nil, resp.Deleted)
			assert.ElementsMatch(t, remote, resp.Updated)
			assert.ElementsMatch(t, nil, resp.Created)
		})
	})

	t.Run("has deleted, updated, created, with reserved skipped resources", func(t *testing.T) {
		t.Parallel()

		t.Run("Component", func(t *testing.T) {
			remote := make([]compapi.Component, len(components)/2)
			for i := 0; i < len(components)/2; i++ {
				comp := componentsDiff1[i].DeepCopy()
				comp.Name = components[i].Name
				remote[i] = *comp
			}

			resp := Diff[compapi.Component](&LocalRemoteResources[compapi.Component]{
				Local: append(components, []compapi.Component{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "kubernetes"},
						Spec:       compapi.ComponentSpec{Type: "secretstores.kubernetes"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "dapr"},
						Spec:       compapi.ComponentSpec{Type: "workflow.dapr"},
					},
				}...),
				Remote: append(remote, componentsDiff2...),
			})

			assert.ElementsMatch(t, components[len(components)/2:], resp.Deleted)
			assert.ElementsMatch(t, remote, resp.Updated)
			assert.ElementsMatch(t, componentsDiff2, resp.Created)
		})
		t.Run("HTTPEndpoint", func(t *testing.T) {
			remote := make([]httpendapi.HTTPEndpoint, len(endpoints)/2)
			for i := 0; i < len(endpoints)/2; i++ {
				end := endpointsDiff1[i].DeepCopy()
				end.Name = endpoints[i].Name
				remote[i] = *end
			}

			resp := Diff[httpendapi.HTTPEndpoint](&LocalRemoteResources[httpendapi.HTTPEndpoint]{
				Local:  endpoints,
				Remote: append(remote, endpointsDiff2...),
			})
			assert.ElementsMatch(t, endpoints[len(endpoints)/2:], resp.Deleted)
			assert.ElementsMatch(t, remote, resp.Updated)
			assert.ElementsMatch(t, endpointsDiff2, resp.Created)
		})
	})
}
