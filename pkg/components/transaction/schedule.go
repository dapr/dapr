package transaction

import (
	"context"
	"fmt"

	transactionComponent "github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	fasthttp "github.com/valyala/fasthttp"
	codes "google.golang.org/grpc/codes"
)

const (
	defaultState                      = 0
	stateForTrySuccess                = 10
	stateForTryFailure                = 1
	stateForConfirmSuccess            = 20
	stateForConfirmFailure            = 2
	stateForRollBackSuccess           = 30
	stateForRollBackFailure           = 3
	requestStatusOK                   = 1
	bunchTransactionServiceInvokeType = "service-invoke"
	bunchTransactionActorType         = "actore"
	transactionConfirm                = "confirm"
	transactionRollBack               = "rollback"
)

func ConfirmTransaction(scheduleTransactionRequest ScheduleTransactionRequest) error {
	transactionInstance := scheduleTransactionRequest.TransactionInstance
	reqs, err := transactionInstance.GetBunchTransactions(
		transactionComponent.GetBunchTransactionsRequest{
			TransactionId: scheduleTransactionRequest.TransactionId,
		},
	)
	if err != nil {
		fmt.Print(err)
		return err
	}
	bunchTransactions := reqs.BunchTransactions
	retryTimes := transactionInstance.GetRetryTimes()
	schema := transactionInstance.GetTransactionSchema()

	fmt.Printf("disrtibute transaction schema is %s :", schema)

	for bunchTransactionId, bunchTransaction := range bunchTransactions {
		state := bunchTransaction.StatusCode
		// pointer of the origin request param
		bunchTransactionReqsParam := bunchTransaction.TryRequestParam
		if state == stateForTrySuccess {
			// try to confirm a bunch transaction
			responseStatusCode := Confirm(scheduleTransactionRequest, bunchTransactionReqsParam, schema, retryTimes)

			if responseStatusCode != 200 {
				return fmt.Errorf("transaction")
			}

			transactionInstance.Confirm(transactionComponent.BunchTransactionConfirmRequest{
				TransactionId:      scheduleTransactionRequest.TransactionId,
				BunchTransactionId: bunchTransactionId,
				StatusCode:         stateForConfirmSuccess,
			})
		}
	}
	return nil

}

func Confirm(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, schema string, retryTimes int) int {
	fmt.Print("switch to a Confrim action with : ", bunchTransactionReqsParam)
	responseStatusCode := 0
	switch schema {
	case "tcc":
		responseStatusCode = ConfirmTcc(scheduleTransactionRequest, bunchTransactionReqsParam, retryTimes)
	}
	return responseStatusCode
}

func ConfirmTcc(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, retryTimes int) int {
	responseStatusCode := 0
	if bunchTransactionReqsParam.Type == bunchTransactionServiceInvokeType {
		responseStatusCode, err := RequestServiceInovde(scheduleTransactionRequest.DirectMessaging, bunchTransactionReqsParam, "Confirm", retryTimes)

		if err != nil {
			fmt.Print(err)
		}
		return responseStatusCode
	} else if bunchTransactionReqsParam.Type == bunchTransactionActorType {
		_, _ = RequestActor(scheduleTransactionRequest.Actor, bunchTransactionReqsParam, "Confirm", retryTimes)
	}

	return responseStatusCode
}

func RequestServiceInovde(directMessaging messaging.DirectMessaging, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, action string, retryTimes int) (int, error) {
	req := invokev1.NewInvokeMethodRequest(bunchTransactionReqsParam.InvokeMethodName+action).WithHTTPExtension(bunchTransactionReqsParam.Verb, bunchTransactionReqsParam.QueryArgs)

	req.WithRawData(bunchTransactionReqsParam.Data, bunchTransactionReqsParam.ContentType)
	// Save headers to internal metadata
	req.WithFastHTTPHeaders(bunchTransactionReqsParam.Header)

	ctx := context.Background()
	i := 1
	for i <= retryTimes {
		i++
		resp, err := directMessaging.Invoke(ctx, bunchTransactionReqsParam.TargetID, req)

		if err != nil {
			return 0, err
		}
		// Construct response
		statusCode := int(resp.Status().Code)
		if !resp.IsHTTPResponse() {
			statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
			if statusCode != fasthttp.StatusOK {
				continue
			}
		}
		if statusCode == fasthttp.StatusOK {
			return statusCode, nil
		} else {
			continue
		}

	}
	return 0, nil
}

func RequestActor(actor actors.Actors, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, action string, retryTimes int) (int, error) {

	req := invokev1.NewInvokeMethodRequest(bunchTransactionReqsParam.InvokeMethodName + action)
	req.WithActor(bunchTransactionReqsParam.ActorType, bunchTransactionReqsParam.ActorID)
	req.WithHTTPExtension(bunchTransactionReqsParam.Verb, bunchTransactionReqsParam.QueryArgs)
	req.WithRawData(bunchTransactionReqsParam.Data, bunchTransactionReqsParam.ContentType)

	ctx := context.Background()
	i := 1
	for i <= retryTimes {
		i++
		resp, err := actor.Call(ctx, req)
		if err != nil {
			return 0, err
		}
		statusCode := int(resp.Status().Code)

		if !resp.IsHTTPResponse() {
			statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
		}
		if statusCode == fasthttp.StatusOK {
			return statusCode, nil
		} else {
			continue
		}
	}

	return 200, nil
}
