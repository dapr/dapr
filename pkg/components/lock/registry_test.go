package lock

import (
	"github.com/dapr/components-contrib/lock"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

const (
	compName = "mock"
	fullName = "lock." + compName
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(New(compName, func() lock.Store {
		return nil
	}),
	)
	if _, err := r.Create(fullName, "v1"); err != nil {
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
