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
	"os"
	"reflect"
	"testing"
)

func TestAppDescription_MarshalJSON(t *testing.T) {
	t.Run("no secret", func(t *testing.T) {
		a := AppDescription{
			AppName:     "testapp",
			ImageSecret: "",
		}
		want := `{"AppName":"testapp"}`
		res, err := a.MarshalJSON()
		if err != nil {
			t.Errorf("AppDescription.MarshalJSON() error = %v", err)
			return
		}
		if !reflect.DeepEqual(string(res), want) {
			t.Errorf("AppDescription.MarshalJSON() = %v, want %v", string(res), want)
		}
	})
	t.Run("hide secret", func(t *testing.T) {
		a := AppDescription{
			AppName:     "testapp",
			ImageSecret: "SECRETVALUE",
		}
		want := `{"ImageSecret":"***","AppName":"testapp"}`
		res, err := a.MarshalJSON()
		if err != nil {
			t.Errorf("AppDescription.MarshalJSON() error = %v", err)
			return
		}
		if !reflect.DeepEqual(string(res), want) {
			t.Errorf("AppDescription.MarshalJSON() = %v, want %v", string(res), want)
		}
	})

	t.Run("use service internal ip", func(t *testing.T) {
		defer os.Clearenv()
		app := AppDescription{
			IngressEnabled: true,
		}
		os.Setenv(useServiceInternalIP, "false")

		if !app.ShouldBeExposed() {
			t.Error("AppDescription.ShouldBeExposed() should evaluate to true when ingress is enabled and internal ip should not be used")
		}

		os.Setenv(useServiceInternalIP, "true")

		if app.ShouldBeExposed() {
			t.Error("AppDescription.ShouldBeExposed() should evaluate to false when internal ip should be used")
		}
	})
}
