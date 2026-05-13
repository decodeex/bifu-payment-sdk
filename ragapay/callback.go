package ragapay

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

var ErrInvalidSign = errors.New("invalid sign")

type OrderStatus = string

const (
	OrderStatus_Prepare OrderStatus = "prepare"
	OrderStatus_Settled OrderStatus = "settled"
	OrderStatus_Pending OrderStatus = "pending"
	OrderStatus_Decline OrderStatus = "decline"
)

type Status = string

const (
	Status_Success Status = "success"
	Status_Fail    Status = "fail"
	Status_Waiting Status = "waiting"
)

type rawCallbackPayload struct {
	TransactionID              string            `json:"id"`
	OrderNumber                string            `json:"order_number"`
	OrderAmount                decimal.Decimal   `json:"order_amount"`
	OrderCurrency              string            `json:"order_currency"`
	OrderDescription           string            `json:"order_description"`
	OrderStatus                string            `json:"order_status"`
	OrderType                  string            `json:"type"`
	Status                     string            `json:"status"`
	Reason                     string            `json:"reason,omitempty"`
	PayeeName                  string            `json:"payee_name,omitempty"`
	PayeeEmail                 string            `json:"payee_email,omitempty"`
	PayeeCountry               string            `json:"payee_country,omitempty"`
	PayeeState                 string            `json:"payee_state,omitempty"`
	PayeeCity                  string            `json:"payee_city,omitempty"`
	PayeeAddress               string            `json:"payee_address,omitempty"`
	RRN                        string            `json:"rrn,omitempty"`
	ApprovalCode               string            `json:"approval_code,omitempty"`
	GatewayID                  string            `json:"gateway_id,omitempty"`
	ExtraGatewayID             string            `json:"extra_gateway_id,omitempty"`
	MerchantName               string            `json:"merchant_name,omitempty"`
	MidName                    string            `json:"mid_name,omitempty"`
	IssuerCountry              string            `json:"issuer_country,omitempty"`
	IssuerBank                 string            `json:"issuer_bank,omitempty"`
	Card                       string            `json:"card,omitempty"`
	CardExpirationDate         string            `json:"card_expiration_date,omitempty"`
	PayeeCard                  string            `json:"payee_card,omitempty"`
	CardToken                  string            `json:"card_token,omitempty"`
	CryptoNetwork              string            `json:"crypto_network,omitempty"`
	CryptoAddress              string            `json:"crypto_address,omitempty"`
	CustomerName               string            `json:"customer_name,omitempty"`
	CustomerEmail              string            `json:"customer_email,omitempty"`
	CustomerCountry            string            `json:"customer_country,omitempty"`
	CustomerState              string            `json:"customer_state,omitempty"`
	CustomerCity               string            `json:"customer_city,omitempty"`
	CustomerAddress            string            `json:"customer_address,omitempty"`
	CustomerIP                 string            `json:"customer_ip,omitempty"`
	Date                       time.Time         `json:"date,omitempty"`
	RecurringInitTransactionID string            `json:"recurring_init_trans_id,omitempty"`
	RecurringToken             string            `json:"recurring_token,omitempty"`
	ScheduleID                 string            `json:"schedule_id,omitempty"`
	ExchangeRate               decimal.Decimal   `json:"exchange_rate,omitempty"`
	ExchangeRateBase           decimal.Decimal   `json:"exchange_rate_base,omitempty"`
	ExchangeCurrency           string            `json:"exchange_currency,omitempty"`
	ExchangeAmount             decimal.Decimal   `json:"exchange_amount,omitempty"`
	VATAmount                  decimal.Decimal   `json:"vat_amount,omitempty"`
	CustomData                 map[string]string `json:"custom_data,omitempty"`
	Hash                       string            `json:"hash"`

	orderAmountStr string
}

func (payload *rawCallbackPayload) VerifySignature(publicID, password string) error {
	const (
		SignatureContent = "{PublicID}{OrderNumber}{OrderAmount}{OrderCurrency}{OrderDescription}{MerchantPassword}"
	)
	formater := strings.NewReplacer(
		"{PublicID}", publicID,
		"{OrderNumber}", payload.OrderNumber,
		"{OrderAmount}", payload.orderAmountStr,
		"{OrderCurrency}", payload.OrderCurrency,
		"{OrderDescription}", payload.OrderDescription,
		"{MerchantPassword}", password,
	)
	content := strings.ToUpper(formater.Replace(SignatureContent))
	s1 := md5.Sum(strings2.ToBytesNoAlloc(content))
	s1Hex := hex.EncodeToString(s1[:])
	s2 := sha1.Sum(strings2.ToBytesNoAlloc(s1Hex))
	expect := hex.EncodeToString(s2[:])

	if strings.EqualFold(expect, payload.Hash) {
		return nil
	}
	return fmt.Errorf("%w, expect %s, got %s", ErrInvalidSign, expect, payload.Hash)
}

// UnmarshalForm unmarshal form data to payload
// 没有支持 go-playground/form, 而是自己写了一个简单的unmarshal, 主要原因有这么几个:
// 1. go-playground/form 不支持像json.Unmarshal那样的自定义反序列化
// 2. 支持RegisterCustomTypeFunc, 但是这个是decoder的方法, 在kratos这个大架子中, 并拿不到decoder
// 3. 性能.
func (payload *rawCallbackPayload) UnmarshalForm(query string) error {
	values, err := url.ParseQuery(query)
	if err != nil {
		return err
	}
	return payload.UnmarshalValues(values)
}

func (payload *rawCallbackPayload) UnmarshalValues(values url.Values) error {
	payload.TransactionID = values.Get("id")
	payload.OrderNumber = values.Get("order_number")
	payload.orderAmountStr = values.Get("order_amount")
	payload.OrderCurrency = values.Get("order_currency")
	payload.OrderDescription = values.Get("order_description")
	payload.OrderStatus = values.Get("order_status")
	payload.OrderType = values.Get("type")
	payload.Status = values.Get("status")
	payload.Reason = values.Get("reason")
	payload.PayeeName = values.Get("payee_name")
	payload.PayeeEmail = values.Get("payee_email")
	payload.PayeeCountry = values.Get("payee_country")
	payload.PayeeState = values.Get("payee_state")
	payload.PayeeCity = values.Get("payee_city")
	payload.PayeeAddress = values.Get("payee_address")
	payload.RRN = values.Get("rrn")
	payload.ApprovalCode = values.Get("approval_code")
	payload.GatewayID = values.Get("gateway_id")
	payload.ExtraGatewayID = values.Get("extra_gateway_id")
	payload.MerchantName = values.Get("merchant_name")
	payload.MidName = values.Get("mid_name")
	payload.IssuerCountry = values.Get("issuer_country")
	payload.IssuerBank = values.Get("issuer_bank")
	payload.Card = values.Get("card")
	payload.CardExpirationDate = values.Get("card_expiration_date")
	payload.PayeeCard = values.Get("payee_card")
	payload.CardToken = values.Get("card_token")
	payload.CryptoNetwork = values.Get("crypto_network")
	payload.CryptoAddress = values.Get("crypto_address")
	payload.CustomerName = values.Get("customer_name")
	payload.CustomerEmail = values.Get("customer_email")
	payload.CustomerCountry = values.Get("customer_country")
	payload.CustomerState = values.Get("customer_state")
	payload.CustomerCity = values.Get("customer_city")
	payload.CustomerAddress = values.Get("customer_address")
	payload.CustomerIP = values.Get("customer_ip")

	if dateStr := values.Get("date"); dateStr != "" {
		date, err := time.Parse(time.DateTime, dateStr)
		if err != nil {
			return err
		}
		payload.Date = date
	}

	payload.RecurringInitTransactionID = values.Get("recurring_init_trans_id")
	payload.RecurringToken = values.Get("recurring_token")
	payload.ScheduleID = values.Get("schedule_id")

	if exchangeRateStr := values.Get("exchange_rate"); exchangeRateStr != "" {
		exchangeRate, err := decimal.NewFromString(exchangeRateStr)
		if err != nil {
			return err
		}
		payload.ExchangeRate = exchangeRate
	}

	if exchangeRateBaseStr := values.Get("exchange_rate_base"); exchangeRateBaseStr != "" {
		exchangeRateBase, err := decimal.NewFromString(exchangeRateBaseStr)
		if err != nil {
			return err
		}
		payload.ExchangeRateBase = exchangeRateBase
	}

	payload.ExchangeCurrency = values.Get("exchange_currency")

	if exchangeAmountStr := values.Get("exchange_amount"); exchangeAmountStr != "" {
		exchangeAmount, err := decimal.NewFromString(exchangeAmountStr)
		if err != nil {
			return err
		}
		payload.ExchangeAmount = exchangeAmount
	}

	if vatAmountStr := values.Get("vat_amount"); vatAmountStr != "" {
		vatAmount, err := decimal.NewFromString(vatAmountStr)
		if err != nil {
			return err
		}
		payload.VATAmount = vatAmount
	}

	// MAYBUG: 格式
	payload.CustomData = make(map[string]string)
	if customDataStr := values.Get("custom_data"); customDataStr != "" {
		err := json.Unmarshal(strings2.ToBytesNoAlloc(customDataStr), &payload.CustomData)
		if err != nil {
			return err
		}
	}

	payload.Hash = values.Get("hash")
	return nil
}

func (payload *rawCallbackPayload) IsSucess() bool {
	return payload.Status == Status_Success
}

type CallbackRequest struct {
	data *rawCallbackPayload
}

func ParseCallbackRequest(req *http.Request) (*CallbackRequest, error) {
	var payload rawCallbackPayload
	if err := payload.UnmarshalForm(req.URL.RawQuery); err != nil {
		return nil, err
	}
	return &CallbackRequest{
		data: &payload,
	}, nil
}

func (req *CallbackRequest) MerchantOrderID() string {
	return req.data.OrderNumber
}

func (req *CallbackRequest) SupplierOrderCode() string {
	return req.data.TransactionID
}

func (req *CallbackRequest) Amount() decimal.Decimal {
	return req.data.OrderAmount
}

func (req *CallbackRequest) Currency() string {
	return req.data.OrderCurrency
}

func (req *CallbackRequest) Status() string {
	return req.data.OrderStatus
}

func (req *CallbackRequest) IsSuccess() bool {
	return req.data.IsSucess()
}

func (req *CallbackRequest) VerifySignature(conf *Config) error {
	if conf == nil {
		return fmt.Errorf("config is nil")
	}
	if req == nil || req.data == nil {
		return fmt.Errorf("payload is nil")
	}

	return req.data.VerifySignature(conf.PublicID, conf.Password)
}

type CallbackReply struct{}

func (req *CallbackRequest) GenerateReply() *CallbackReply {
	return &CallbackReply{}
}

func (reply *CallbackReply) WriteTo(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("success"))
	return err
}
