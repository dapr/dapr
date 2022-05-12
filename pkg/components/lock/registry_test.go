package lock

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/components-contrib/lock"
)

const (
	compName   = "mock"
	compNameV2 = "mock/v2"
	fullName   = "lock." + compName
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(
		New(compName, func() lock.Store {
			return nil
		}),
		New(compNameV2, func() lock.Store {
			return nil
		}),
	)
	if _, err := r.Create(fullName, "v1"); err != nil {
		t.Fatalf("create mock store failed: %v", err)
	}
	if _, err := r.Create(fullName, "v2"); err != nil {
		t.Fatalf("create mock store failed: %v", err)
	}
	if _, err := r.Create("not exists", "v1"); !strings.Contains(err.Error(), "couldn't find lock store") {
		t.Fatalf("create mock store failed: %v", err)
	}
}

func TestNewFactory(t *testing.T) {
	f := New("", nil)
	assert.NotNil(t, f)
}
