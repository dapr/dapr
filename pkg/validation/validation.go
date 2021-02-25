// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package validation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// The consts and vars beginning with dns* were taken from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
const (
	dns1123LabelFmt       string = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
	dns1123LabelErrMsg    string = "a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character"
	dns1123LabelMaxLength int    = 63
)

var dns1123LabelRegexp = regexp.MustCompile("^" + dns1123LabelFmt + "$")

// ValidateKubernetesAppID returns a bool that indicates whether a dapr app id is valid for the Kubernetes platform.
func ValidateKubernetesAppID(appID string) error {
	if appID == "" {
		return errors.New("value for the dapr.io/app-id annotation is empty")
	}
	r := isDNS1123Label(appID)
	if len(r) == 0 {
		return nil
	}
	s := fmt.Sprintf("invalid app id(input: %s): %s", appID, strings.Join(r, ","))
	return errors.New(s)
}

// The function was taken as-is from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
func isDNS1123Label(value string) []string {
	var errs []string
	if len(value) > dns1123LabelMaxLength {
		errs = append(errs, maxLenError(dns1123LabelMaxLength))
	}
	if !dns1123LabelRegexp.MatchString(value) {
		errs = append(errs, regexError(dns1123LabelErrMsg, dns1123LabelFmt, "my-name", "123-abc"))
	}
	return errs
}

// The function was taken as-is from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
func maxLenError(length int) string {
	return fmt.Sprintf("must be no more than %d characters", length)
}

// The function was taken as-is from: https://github.com/kubernetes/apimachinery/blob/fc49b38c19f02a58ebc476347e622142f19820b9/pkg/util/validation/validation.go
func regexError(msg string, fmt string, examples ...string) string {
	if len(examples) == 0 {
		return msg + " (regex used for validation is '" + fmt + "')"
	}
	msg += " (e.g. "
	for i := range examples {
		if i > 0 {
			msg += " or "
		}
		msg += "'" + examples[i] + "', "
	}
	msg += "regex used for validation is '" + fmt + "')"
	return msg
}
