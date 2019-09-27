package bindings

import (
	"github.com/actionscore/components-contrib/state"
)

// ReadResponse is an the return object from an actions input binding
type ReadResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

// WriteRequest is the object given to an actions output binding
type WriteRequest struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

// AppResponse is the object describing the response from user code after a bindings event
type AppResponse struct {
	Data        interface{}        `json:"data"`
	To          []string           `json:"to"`
	State       []state.SetRequest `json:"state"`
	Concurrency string             `json:"concurrency"`
}
