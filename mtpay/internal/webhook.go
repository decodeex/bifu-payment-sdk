package internal

import "github.com/shopspring/decimal"

type TradeType = string

const (
	TradeTypeDeposit  TradeType = "Deposit"
	TradeTypeWithdraw TradeType = "Withdraw"
)

type TradeStatus = string

const (
	// for deposit: The deposit order has been marked as paid by the client. Payment confirmation is currently in progress.
	// for withdraw: The withdrawal order is in progress. The system is currently processing the payout to the client.
	TradeStatusInProgress TradeStatus = "InProgress"
	// for deposit: The deposit order has been successfully completed.
	// for withdraw: The withdrawal order has been successfully completed.
	TradeStatusFinished TradeStatus = "Finished"
	// for deposit: The deposit order payment has failed. The failure reason can be retrieved from the message field.
	// for withdraw: The withdrawal order payment has failed. The failure reason can be retrieved from the message field.
	TradeStatusFailed TradeStatus = "Failed"
	// for deposit: The deposit order has been cancelled. The cancellation reason can be retrieved from the message field.
	// for withdraw: The withdrawal order has been cancelled. The cancellation reason can be retrieved from the message field.
	TradeStatusCancelled TradeStatus = "Cancelled"
)

type SourceFrom = string
type PaymentMethodOption = string

type WebHookRequestData struct {
	TradeType         TradeType           `json:"tradeType"`         // Transaction type: Deposit or Withdraw
	RequestCode       string              `json:"requestCode"`       // Unique system-generated request code
	MerchantOrderNo   string              `json:"merchantOrderNo"`   // Merchant's unique order number
	InternalOrderNo   string              `json:"internalOrderNo"`   // Internal system order number
	Source            SourceFrom          `json:"source"`            // Request source: MerchantBackend, API_V1, API_V2
	ClientName        string              `json:"clientName"`        // Client's name
	RequestAmount     decimal.Decimal     `json:"requestAmount"`     // Requested transaction amount
	RequestCurrency   string              `json:"requestCurrency"`   // Currency of the transaction (e.g., MTC/CNY/HKD)
	Message           string              `json:"message"`           // Status message or additional information
	PaymentMethod     PaymentMethodOption `json:"paymentMethod"`     // Payment method used, if applicable
	RequestStatus     TradeStatus         `json:"requestStatus"`     // Current status of the transaction
	UnitPrice         decimal.Decimal     `json:"unitPrice"`         // Exchange rate used (fiat / MTC)
	TransactionAmount decimal.Decimal     `json:"transactionAmount"` // Transaction Amount (MTC)
	ReceivedAmount    decimal.Decimal     `json:"receivedAmount"`    // Merchant received/deducted by the client
	TransactionFee    decimal.Decimal     `json:"transactionFee"`    // Transaction fee charged
	PaymentAmount     decimal.Decimal     `json:"paymentAmount"`     // Final payment amount in fiat
	FiatCurrency      CurrencyCode        `json:"fiatCurrency"`      // Fiat currency code: CNY, HKD
}

type WebHookRequest struct {
	Data      WebHookRequestData `json:"data"`      // Data of the webhook request
	Signature string             `json:"signature"` // Signature of the webhook request
	Timestamp int64              `json:"timestamp"` // Timestamp of the webhook request
}

func (req *WebHookRequest) VerifySignature(accessKey, secretKey string) bool {
	return VerifySignature(accessKey, secretKey, req.Timestamp, req.Signature)
}
