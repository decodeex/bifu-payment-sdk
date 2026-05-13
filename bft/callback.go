package bft

import (
	"encoding/json"
	"fmt"

	"github.com/decodeex/bifu-payment-sdk/bft/internal"
	"github.com/shopspring/decimal"
)

type CallbackRequest struct {
	raw *internal.CheckoutCallbackPayload
}

func (req *CallbackRequest) UnmarshalJSON(payload []byte) error {
	if string(payload) == "null" {
		return nil
	}

	var raw internal.CheckoutCallbackPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	req.raw = &raw
	return nil
}

func (req *CallbackRequest) MerchantOrderID() string {
	return req.raw.ApiOrderNo
}

func (req *CallbackRequest) Amount() decimal.Decimal {
	return req.raw.Money
}

func (req *CallbackRequest) Currency() string {
	return "CNY"
}

type TradeStatus = internal.TradeStatus

func (req *CallbackRequest) Status() TradeStatus {
	return req.raw.TradeStatus
}

func (req *CallbackRequest) SupplierOrderCode() string {
	return req.raw.TradeID
}

func (req *CallbackRequest) VerifySignature(conf *Config) error {
	if conf == nil {
		return fmt.Errorf("config is nil")
	}
	if req == nil || req.raw == nil {
		return fmt.Errorf("raw payload is nil")
	}

	return req.raw.VerifySignature(conf.PublicKey)
}

func (req *CallbackRequest) IsSuccess() bool {
	return req.raw.IsSuccess()
}

func (req *CallbackRequest) UniqueCode() string {
	return req.raw.UniqueCode
}

type ResponseCode = internal.ResponseCode

const (
	ResponseCodeSuccess ResponseCode = internal.ResponseCodeSuccess // 1: 成功
)

type CallbackReply struct {
	inner *internal.CheckoutCallbackReply
}

func (reply *CallbackReply) MarshalJSON() ([]byte, error) {
	if reply.inner == nil {
		return nil, fmt.Errorf("checkout callback reply is nil")
	}
	return json.Marshal(reply.inner)
}

func NewCallbackReply(code ResponseCode, message string, success bool) *CallbackReply {
	return &CallbackReply{
		inner: &internal.CheckoutCallbackReply{
			Code:    code,
			Message: message,
			Data:    nil,
			Success: success,
		},
	}
}
