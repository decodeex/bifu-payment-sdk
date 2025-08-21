package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

type PayType = int

const (
	PayTypeUnknown  PayType = 0 // 未知
	PayTypeUnionPay PayType = 1 // 银联
	PayTypeAlipay   PayType = 2 // 支付宝
	PayTypeWeChat   PayType = 3 // 微信支付
)

var ErrorInvalidData = errors.New("invalid data")

type CheckoutPayload struct {
	// 商户UID,对应商户后台的“商户编码"
	Uid string `json:"uid"`
	// 商户具有代表性的唯一标识。例如：用户ID，业务ID等
	UniqueCode string `json:"uniqueCode"`
	// 金额为整数。单位：人民币
	Money decimal.Decimal `json:"money"`
	// 支付类型。1：银联
	PayType PayType `json:"payType"`
	// 商户订单号。商户平台自己生成的单号
	OrderID string `json:"orderId"`
	// 付款人名字
	PayerName string `json:"payerName"`
	// 签名字符串
	Signature string `json:"signature"`
	// 跳转地址
	JumpUrl string `json:"jumpUrl,omitempty"`
}

func (payload *CheckoutPayload) genrateSignature(key string) string {
	signer := signer{}
	entries := []signEntry{
		{"uid", payload.Uid},
		{"uniqueCode", payload.UniqueCode},
		{"money", payload.Money.StringFixed(0)},
		{"payType", strconv.Itoa(payload.PayType)},
		{"orderId", payload.OrderID},
		{"payerName", payload.PayerName},
	}
	if payload.JumpUrl != "" {
		entries = append(entries, signEntry{"jumpUrl", payload.JumpUrl})
	}
	signature := signer.Sign(key, entries...)
	return signature
}

func (payload *CheckoutPayload) Validate() error {
	if payload.Uid == "" || len(payload.Uid) > 11 {
		return fmt.Errorf("invalid merchant ID")
	}
	if payload.UniqueCode == "" || len(payload.UniqueCode) > 64 {
		return fmt.Errorf("invalid unique code")
	}

	trunc := payload.Money.Truncate(0)
	if trunc.IsNegative() || trunc.IsZero() {
		return fmt.Errorf("invalid money amount")
	}
	if !trunc.Equal(payload.Money) {
		return fmt.Errorf("money must be an integer")
	}
	if payload.PayType != PayTypeUnionPay && payload.PayType != PayTypeAlipay && payload.PayType != PayTypeWeChat {
		return fmt.Errorf("invalid pay type")
	}
	if payload.OrderID == "" || len(payload.OrderID) > 64 {
		return fmt.Errorf("invalid order ID")
	}
	if payload.PayerName == "" || len(payload.PayerName) > 32 {
		return fmt.Errorf("invalid payer name")
	}
	if payload.JumpUrl != "" {
		if !strings.HasPrefix(payload.JumpUrl, "http://") && !strings.HasPrefix(payload.JumpUrl, "https://") {
			return fmt.Errorf("invalid jump URL")
		}
	}

	return nil
}

func (payload *CheckoutPayload) GenerateSignedRequest(ctx context.Context, key string) (*http.Request, error) {
	const (
		Path        = "/coin/pay/order/pay/checkout/counter"
		Method      = http.MethodPost
		ContentType = "application/json"
	)

	payload.Signature = payload.genrateSignature(key)
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, Method, Path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)

	return req, nil
}

type ResponseCode = int

const (
	ResponseCodeSuccess ResponseCode = 1
)

type CheckoutResponse struct {
	// 接口调用状态，1:成功，其他值：失败
	Code ResponseCode `json:"code"`
	// 结果说明，如果接口调用出错，那么返回错误描述，成功返回“成功”
	Message string `json:"message"`
	// 接口返回结果。值为URL地址，拿到这个URL可以跳转到下单页面
	Data string `json:"data"`
	// true:成功，false:失败
	Success bool `json:"success"`
}

func (resp *CheckoutResponse) IsSuccess() bool {
	return resp.Code == ResponseCodeSuccess && resp.Success
}
