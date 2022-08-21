package transaction

import (
	transactionComponent "github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messaging"
)

type TransactionScheduleRequest struct {
	TransactionID string `json:"transactionID"`
}

type TransactionRequestHeader struct {
	TransactionID        string `json:"transactionID"`
	BunchTransactionID   string `json:"bunchTransactionID"`
	TransactionStoreName string `json:"transactionStoreName"`
}

type ScheduleTransactionRequest struct {
	TransactionID       string                           `json:"transactionID"`
	TransactionInstance transactionComponent.Transaction `json:"transactionInstance"`
	DirectMessaging     messaging.DirectMessaging        `json:"directMessaging"`
	Actor               actors.Actors                    `json:"actor"`
}
