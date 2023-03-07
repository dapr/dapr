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

package validation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The consts and vars beginning with dns* were taken from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
const (
	dns1123LabelFmt       string = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
	dns1123LabelErrMsg    string = "a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character"
	dns1123LabelMaxLength int    = 63
)

var dns1123LabelRegexp = regexp.MustCompile("^" + dns1123LabelFmt + "$")

// ValidateKubernetesAppID returns an error if the Dapr app id is not valid for the Kubernetes platform.
func ValidateKubernetesAppID(appID string) error {
	if appID == "" {
		return errors.New("value for the dapr.io/app-id annotation is empty")
	}
	err := isDNS1123Label(serviceName(appID))
	if err != nil {
		return fmt.Errorf("invalid app id (input: '%s', service: '%s'): %w", appID, serviceName(appID), err)
	}
	return nil
}

// ValidateSelfHostedAppID returns an error if the Dapr app id is not valid for self-hosted.
func ValidateSelfHostedAppID(appID string) error {
	if appID == "" {
		return errors.New("parameter app-id cannot be empty")
	}
	if strings.Contains(appID, ".") {
		return errors.New("parameter app-id cannot contain dots")
	}
	return nil
}

func serviceName(appID string) string {
	return fmt.Sprintf("%s-dapr", appID)
}

// The function was adapted from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
func isDNS1123Label(value string) error {
	var errs []error
	if len(value) > dns1123LabelMaxLength {
		errs = append(errs, fmt.Errorf("must be no more than %d characters", dns1123LabelMaxLength))
	}
	if !dns1123LabelRegexp.MatchString(value) {
		errs = append(errs, regexError(dns1123LabelErrMsg, dns1123LabelFmt, "my-name", "123-abc"))
	}
	return errors.Join(errs...)
}

// The function was adapted from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
func regexError(msg string, fmt string, examples ...string) error {
	if len(examples) == 0 {
		return errors.New(msg + " (regex used for validation is '" + fmt + "')")
	}
	msg += " (e.g. "
	for i := range examples {
		if i > 0 {
			msg += " or "
		}
		msg += "'" + examples[i] + "', "
	}
	msg += "regex used for validation is '" + fmt + "')"
	return errors.New(msg)
}
