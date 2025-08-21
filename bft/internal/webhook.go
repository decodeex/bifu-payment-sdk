package internal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

var ErrInvalidSign = errors.New("invalid sign")

type TradeStatus = string

const (
	TradeStatusSuccess TradeStatus = "1" // 成功
)

type CheckoutCallbackPayload struct {
	ApiOrderNo  string          `json:"apiOrderNo"`  // 商户订单号
	Money       decimal.Decimal `json:"money"`       // 订单金额
	TradeStatus TradeStatus     `json:"tradeStatus"` // 交易状态。1：成功，其它为失败
	TradeID     string          `json:"tradeId"`     // Exlink订单号
	UniqueCode  string          `json:"uniqueCode"`  // 商户具有代表性的唯一标识
	Signature   string          `json:"signature"`   // 签名字符串
}

func (payload *CheckoutCallbackPayload) generateSignature(key string) string {
	signer := signer{}
	entries := []signEntry{
		{"apiOrderNo", payload.ApiOrderNo},
		{"money", payload.Money.StringFixed(0)},
		{"tradeStatus", payload.TradeStatus},
		{"tradeId", payload.TradeID},
		{"uniqueCode", payload.UniqueCode},
	}
	signature := signer.Sign(key, entries...)
	return signature
}

func (payload *CheckoutCallbackPayload) VerifySignature(key string) error {

	expect := payload.generateSignature(key)
	if !strings.EqualFold(expect, payload.Signature) {
		return fmt.Errorf("%w, expect %s, got %s", ErrInvalidSign, expect, payload.Signature)
	}
	return nil
}

func (payload *CheckoutCallbackPayload) IsSuccess() bool {
	return payload.TradeStatus == TradeStatusSuccess
}

type CheckoutCallbackReply struct {
	Code    ResponseCode `json:"code"`    // 交易状态。1：成功，其它为失败
	Message string       `json:"message"` // 结果说明
	Data    any          `json:"data"`    // 接口返回结果
	Success bool         `json:"success"` // true:成功，false:失败
}
