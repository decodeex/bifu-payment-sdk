package chippay

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
)

type TradeStatus = string

const (
	TradeStatusFailed      TradeStatus = "0"
	TradeStatusSuccess     TradeStatus = "1"
	TradeStatusBatchFailed TradeStatus = "2"
)

type rawBuyCoinCallbackPayload struct {
	// 订单币种数量 精度最多至小数点后4位
	CoinAmount string `json:"coinAmount"`
	// 数字货币标识：usdt
	CoinSign string `json:"coinSign"`
	// 商户订单号
	CompanyOrderNum string `json:"companyOrderNum"`
	// ChipPay平台订单号
	OtcOrderNum string `json:"otcOrderNum"`
	// 快捷买卖方向(1.Express buy, 2.Express sell)
	OrderType OrderType `json:"orderType"`
	// 交易状态(0:交易失败， 1:交易成功 ，2：快捷批量卖单生成失败)
	TradeStatus TradeStatus `json:"tradeStatus"`
	// 卖单取消原因(当 orderType=2, tradeStatus=0 时才会返回此参数)
	CancelReason string `json:"cancelReason"`
	// 订单交易时间 (北京时间)
	TradeOrderTime string `json:"tradeOrderTime"`
	// 数字货币单价
	UnitPrice string `json:"unitPrice"`
	// 用户付款的法币实际到账金额
	Total string `json:"total"`
	// 数字货币到账数量
	SuccessAmount string `json:"successAmount"`
	// 参数签名
	Sign string `json:"sign"`
}

func (payload *rawBuyCoinCallbackPayload) VerifySignature(publicKey *rsa.PublicKey) error {
	return signer{}.Verify(publicKey, payload.serializeToMap(), payload.Sign)
}

func (payload *rawBuyCoinCallbackPayload) serializeToMap() map[string]string {
	params := make(map[string]string)
	params["coinAmount"] = payload.CoinAmount
	params["coinSign"] = payload.CoinSign
	params["companyOrderNum"] = payload.CompanyOrderNum
	params["otcOrderNum"] = payload.OtcOrderNum
	params["orderType"] = payload.OrderType
	params["tradeStatus"] = payload.TradeStatus
	params["tradeOrderTime"] = payload.TradeOrderTime
	params["unitPrice"] = payload.UnitPrice
	params["total"] = payload.Total
	params["successAmount"] = payload.SuccessAmount
	return params
}

type BuyCoinCallbackRequest struct {
	data *rawBuyCoinCallbackPayload

	total decimal.Decimal
}

func (req *BuyCoinCallbackRequest) MerchantOrderID() string {
	return req.data.CompanyOrderNum
}

func (req *BuyCoinCallbackRequest) Amount() decimal.Decimal {
	return req.total
}

func (req *BuyCoinCallbackRequest) Status() TradeStatus {
	return req.data.TradeStatus
}

func (req *BuyCoinCallbackRequest) SupplierOrderCode() string {
	return req.data.OtcOrderNum
}
func (req *BuyCoinCallbackRequest) VerifySignature(conf *Config) error {
	if conf == nil {
		return fmt.Errorf("config is nil")
	}
	if req == nil || req.data == nil {
		return fmt.Errorf("raw payload is nil")
	}

	return req.data.VerifySignature(conf.PublicKey())
}

func (req *BuyCoinCallbackRequest) IsSuccess() bool {
	return req.data.TradeStatus == TradeStatusSuccess
}

func ParseBuyCoinCallbackRequest(ree *http.Request) (*BuyCoinCallbackRequest, error) {
	payload := &rawBuyCoinCallbackPayload{}
	if err := json.NewDecoder(ree.Body).Decode(payload); err != nil {
		return nil, fmt.Errorf("failed to decode request body: %w", err)
	}

	total, err := decimal.NewFromString(payload.Total)
	if err != nil {
		return nil, fmt.Errorf("failed to parse total amount: %w", err)
	}

	return &BuyCoinCallbackRequest{
		data:  payload,
		total: total,
	}, nil
}

type rawBuyCoinCallbackResponseData struct {
	OtcOrderNum     string `json:"otcOrderNum"`
	CompanyOrderNum string `json:"companyOrderNum"`
}

type rawCallbackResponse[T any] struct {
	Code    StatusCode `json:"code"`
	Msg     string     `json:"msg"`
	Data    *T         `json:"data,omitempty"`
	Success bool       `json:"success"`
}

type BuyCoinCallbackReply struct {
	data *rawCallbackResponse[rawBuyCoinCallbackResponseData]
}

func (req *BuyCoinCallbackRequest) GenerateReply() *BuyCoinCallbackReply {
	return &BuyCoinCallbackReply{
		data: &rawCallbackResponse[rawBuyCoinCallbackResponseData]{
			Code:    StatusCodeSuccess,
			Msg:     "success",
			Success: true,
			Data: &rawBuyCoinCallbackResponseData{
				OtcOrderNum:     req.data.OtcOrderNum,
				CompanyOrderNum: req.data.CompanyOrderNum,
			},
		},
	}
}

func (reply *BuyCoinCallbackReply) WriteTo(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(reply.data)
}

type rawIntentOrderCallbackPayload struct {
	CoinAmount      string      `json:"coinAmount"`      // 订单币种数量, bigDecimal to string type
	CoinSign        string      `json:"coinSign"`        // 数字货币标识：5.usdt
	CompanyOrderNum string      `json:"companyOrderNum"` // 商户订单号
	IntentOrderNo   string      `json:"intentOrderNo"`   // 平台自选订单号
	Sign            string      `json:"sign"`            // 参数签名
	SuccessAmount   string      `json:"successAmount"`   // 数字货币到账数量, bigDecimal to string type
	Total           string      `json:"total"`           // 用户付款的法币实际到账金额, bigDecimal to string type
	TradeOrderTime  string      `json:"tradeOrderTime"`  // 自选订单创建时间 (北京时间)
	TradeStatus     TradeStatus `json:"tradeStatus"`     // 交易状态(0:交易失败1:交易成功)
	UnitPrice       string      `json:"unitPrice"`       // 数字货币单价, bigDecimal to string type
}

func (payload *rawIntentOrderCallbackPayload) VerifySignature(publicKey *rsa.PublicKey) error {
	return signer{}.Verify(publicKey, payload.serializeToMap(), payload.Sign)
}

func (payload *rawIntentOrderCallbackPayload) serializeToMap() map[string]string {
	params := make(map[string]string)
	params["coinAmount"] = payload.CoinAmount
	params["coinSign"] = payload.CoinSign
	params["companyOrderNum"] = payload.CompanyOrderNum
	params["intentOrderNo"] = payload.IntentOrderNo
	params["tradeStatus"] = payload.TradeStatus
	params["tradeOrderTime"] = payload.TradeOrderTime
	params["unitPrice"] = payload.UnitPrice
	params["total"] = payload.Total
	params["successAmount"] = payload.SuccessAmount
	return params
}

type IntentOrderCallbackRequest struct {
	data *rawIntentOrderCallbackPayload

	total decimal.Decimal
}

func (req *IntentOrderCallbackRequest) MerchantOrderID() string {
	return req.data.CompanyOrderNum
}

func (req *IntentOrderCallbackRequest) Amount() decimal.Decimal {
	return req.total
}

func (req *IntentOrderCallbackRequest) Status() TradeStatus {
	return req.data.TradeStatus
}

func (req *IntentOrderCallbackRequest) SupplierOrderCode() string {
	return req.data.IntentOrderNo
}
func (req *IntentOrderCallbackRequest) VerifySignature(conf interface {
	PublicKey() *rsa.PublicKey
}) error {
	if conf == nil {
		return fmt.Errorf("config is nil")
	}
	if req == nil || req.data == nil {
		return fmt.Errorf("raw payload is nil")
	}

	return req.data.VerifySignature(conf.PublicKey())
}

func (req *IntentOrderCallbackRequest) IsSuccess() bool {
	return req.data.TradeStatus == TradeStatusSuccess
}

func ParseIntentOrderCallbackRequest(ree *http.Request) (*IntentOrderCallbackRequest, error) {
	payload := &rawIntentOrderCallbackPayload{}
	if err := json.NewDecoder(ree.Body).Decode(payload); err != nil {
		return nil, fmt.Errorf("failed to decode request body: %w", err)
	}

	total, err := decimal.NewFromString(payload.Total)
	if err != nil {
		return nil, fmt.Errorf("failed to parse total amount: %w", err)
	}

	return &IntentOrderCallbackRequest{
		data:  payload,
		total: total,
	}, nil
}

type rawIntentOrderCallbackResponseData struct {
	IntentOrderNo   string `json:"intentOrderNo"`
	CompanyOrderNum string `json:"companyOrderNum"`
}

type IntentOrderCallbackReply struct {
	data *rawCallbackResponse[rawIntentOrderCallbackResponseData]
}

func (req *IntentOrderCallbackRequest) GenerateReply() *IntentOrderCallbackReply {
	return &IntentOrderCallbackReply{
		data: &rawCallbackResponse[rawIntentOrderCallbackResponseData]{
			Code:    StatusCodeSuccess,
			Msg:     "success",
			Success: true,
			Data: &rawIntentOrderCallbackResponseData{
				IntentOrderNo:   req.data.IntentOrderNo,
				CompanyOrderNum: req.data.CompanyOrderNum,
			},
		},
	}
}

func (IntentOrderCallbackRequest) GenerateErrorReply(code StatusCode, msg string) *IntentOrderCallbackReply {
	return &IntentOrderCallbackReply{
		data: &rawCallbackResponse[rawIntentOrderCallbackResponseData]{
			Code:    code,
			Msg:     msg,
			Success: false,
			Data:    nil,
		},
	}
}

func (reply *IntentOrderCallbackReply) WriteTo(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(reply.data)
}
