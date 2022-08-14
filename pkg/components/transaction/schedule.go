package transaction

import (
	"fmt"

	transactionComponent "github.com/dapr/components-contrib/transaction"
)

const (
	defaultState            = 0
	stateForTrySuccess      = 10
	stateForTryFailure      = 1
	stateForConfirmSuccess  = 20
	stateForConfirmFailure  = 2
	stateForRollBackSuccess = 30
	stateForRollBackFailure = 3
	requestStatusOK         = 1
)

func ConfirmTransaction(transactionInstance transactionComponent.Transaction, reqParam TransactionConfirmRequest) error {
	reqs, err := transactionInstance.GetBunchTransactions(
		transactionComponent.GetBunchTransactionsRequest{
			TransactionId: reqParam.TransactionId,
		},
	)
	if err != nil {
		fmt.Print(err)
		return err
	}
	bunchTransactions := reqs.BunchTransactions
	retryTimes := transactionInstance.GetRetryTimes()
	schema := transactionInstance.GetTransactionSchema()

	fmt.Printf("disrtibute transaction schema is %s", schema)

	for bunchTransactionId, bunchTransaction := range bunchTransactions {
		//state, _ := bunchTransaction[bunchTransactionStateParam].(int)
		state := bunchTransaction.StatusCode
		// pointer of the origin request param
		bunchTransactionReqsParam := bunchTransaction.TryRequestParam
		if state != stateForConfirmSuccess {
			// try to confirm a bunch transaction
			responseStatusCode := Confirm(bunchTransactionReqsParam, schema, retryTimes)

			transactionInstance.Confirm(transactionComponent.BunchTransactionConfirmRequest{
				TransactionId:      reqParam.TransactionId,
				BunchTransactionId: bunchTransactionId,
				StatusCode:         stateForConfirmSuccess,
			})
		}
	}
	return nil

}

func Confirm(bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, schema string, retryTimes int) int {
	fmt.Print(bunchTransactionReqsParam)
	responseStatusCode := 0
	switch schema {
	case "tcc":
		responseStatusCode = ConfirmTcc(bunchTransactionReqsParam, retryTimes)
	}
	return responseStatusCode
}

func ConfirmTcc(bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, retryTimes int) int {
	responseStatusCode := 200
	return responseStatusCode
}

func RequestServiceInovde(bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam) {

}

func RequestActor(bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam) {

}
