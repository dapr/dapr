package transaction

import (
	"context"
	"fmt"

	transactionComponent "github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	fasthttp "github.com/valyala/fasthttp"
	codes "google.golang.org/grpc/codes"
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

func ConfirmTransaction(transactionInstance transactionComponent.Transaction, directMessaging messaging.DirectMessaging, reqParam TransactionConfirmRequest) error {
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

			if responseStatusCode != 200 {
				return fmt.Errorf("transaction")
			}

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

func RequestServiceInovde(directMessaging messaging.DirectMessaging, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, action string) (int, error) {
	req := invokev1.NewInvokeMethodRequest(bunchTransactionReqsParam.InvokeMethodName+action).WithHTTPExtension(bunchTransactionReqsParam.Verb, bunchTransactionReqsParam.QueryArgs)

	resp, err := directMessaging.Invoke(context.Background(), bunchTransactionReqsParam.TargetID, req)

	if err != nil {
		return 0, err
	}

	// Construct response
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
		if statusCode != fasthttp.StatusOK {
			if _, err := invokev1.ProtobufToJSON(resp.Status()); err != nil {
				statusCode = fasthttp.StatusInternalServerError
				return 0, err
			}
		}
	} else if statusCode != fasthttp.StatusOK {
		return 0, fmt.Errorf("Received non-successful status code: %d", statusCode)
	}
	return statusCode, nil
}

func RequestActor(bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam) {

}
