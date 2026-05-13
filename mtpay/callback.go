package mtpay

import (
	"encoding/json"

	"github.com/decodeex/bifu-payment-sdk/mtpay/internal"
	"github.com/shopspring/decimal"
)

type CallbackRequest struct {
	raw *internal.WebHookRequest
}

func (req *CallbackRequest) UnmarshalJSON(payload []byte) error {
	if string(payload) == "null" {
		return nil
	}

	var raw internal.WebHookRequest
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	req.raw = &raw
	return nil
}

func (req *CallbackRequest) VerifySignature(accessKey, secretKey string) bool {
	if req.raw == nil {
		return false
	}
	return req.raw.VerifySignature(accessKey, secretKey)
}

func (req *CallbackRequest) GetMerchantOrderNo() string {
	return req.raw.Data.MerchantOrderNo
}

func (req *CallbackRequest) GetSupplierOrderID() string {
	return req.raw.Data.InternalOrderNo
}

func (req *CallbackRequest) IsSuccess() bool {
	return req.raw.Data.RequestStatus == internal.TradeStatusFinished
}

func (req *CallbackRequest) IsFailed() bool {
	return req.raw.Data.RequestStatus == internal.TradeStatusFailed || req.raw.Data.RequestStatus == internal.TradeStatusCancelled
}

func (req *CallbackRequest) GetPaidAmount() decimal.Decimal {
	return req.raw.Data.TransactionAmount
}

func (req *CallbackRequest) GetPayCurrency() string {
	return req.raw.Data.FiatCurrency
}

func (req *CallbackRequest) GetStatus() string {
	return req.raw.Data.RequestStatus
}
