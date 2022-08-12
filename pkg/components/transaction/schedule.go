package transaction

import (
	"fmt"

	transactionComponent "github.com/dapr/components-contrib/transaction"
)

const (
	defaultState                    = 0
	stateForTrySuccess              = 10
	stateForTryFailure              = 1
	stateForConfirmSuccess          = 20
	stateForConfirmFailure          = 2
	stateForRollBackSuccess         = 30
	stateForRollBackFailure         = 3
	bunchTransactionStateParam      = "state"
	bunchTransacitonTryRequestParam = "tryRequestParam"
	requestStatusOK                 = 1
	defaultTransactionSchema        = "tcc"
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
	for bunchTransactionId, bunchTransaction := range bunchTransactions {
		state, _ := bunchTransaction[bunchTransactionStateParam].(int)
		// pointer of the origin request param
		bunchTransactionReqsParam := bunchTransaction[bunchTransacitonTryRequestParam]
		if state != stateForConfirmSuccess {
			// try to confirm a bunch transaction
			Confirm(bunchTransactionReqsParam)

			transactionInstance.Confirm(transactionComponent.BunchTransactionConfirmRequest{
				TransactionId:      reqParam.TransactionId,
				BunchTransactionId: bunchTransactionId,
				StatusCode:         stateForConfirmSuccess,
			})
		}
	}
	return nil

}

func Confirm(bunchTransactionReqsParam interface{}) {

	fmt.Print(bunchTransactionReqsParam)

}
