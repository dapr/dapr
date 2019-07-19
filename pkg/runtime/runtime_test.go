package runtime

import (
	"testing"

	"github.com/actionscore/actions/pkg/config"
)

func TestNewRuntime(t *testing.T) {
	r := NewActionsRuntime(&RuntimeConfig{}, &config.Configuration{})
	if r == nil {
		t.Error(
			"Expected success but got nil",
		)
	}
}

func TestConfig(t *testing.T) {
	c := NewRuntimeConfig("", "", "", "", "", "", "", "", 0, 0, 0)
	if c == nil {
		t.Error(
			"Expected success but got nil",
		)
	}
}
