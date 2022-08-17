package transaction

type TransactionConfirmRequest struct {
	TransactionId string `json:"transactionId"`
}

type TransactionRequestHeader struct {
	TransactionId        string `json:"transactionId"`
	BunchTransactionId   string `json:"bunchTransactionId"`
	TransactionStoreName string `json:"transactionStoreName"`
}
