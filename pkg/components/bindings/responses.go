package bindings

import (
	"github.com/actionscore/actions/pkg/components/state"
)

type ReadResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

type WriteRequest struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

type AppResponse struct {
	Data        interface{}        `json:"data"`
	To          []string           `json:"to"`
	State       []state.SetRequest `json:"state"`
	Concurrency string             `json:"concurrency"`
}
