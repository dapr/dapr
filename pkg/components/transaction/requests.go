package transaction

import (
	transactionComponent "github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messaging"
)

type TransactionConfirmRequest struct {
	TransactionId string `json:"transactionId"`
}

type TransactionRequestHeader struct {
	TransactionId        string `json:"transactionId"`
	BunchTransactionId   string `json:"bunchTransactionId"`
	TransactionStoreName string `json:"transactionStoreName"`
}

type ScheduleTransactionRequest struct {
	TransactionId       string                           `json:"transactionId"`
	TransactionInstance transactionComponent.Transaction `json:"transactionInstance"`
	DirectMessaging     messaging.DirectMessaging        `json:"directMessaging"`
	Actor               actors.Actors                    `json:"actor"`
}
