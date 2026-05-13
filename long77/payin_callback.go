package long77

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

var ErrInvalidSign = errors.New("invalid sign")

const (
	PayInCallbackResponse = "success"
)

type rawPayInCallbackPayload struct {
	PartnerID        string      `json:"partner_id"`
	SystemOrderCode  string      `json:"system_order_code"`
	PartnerOrderCode string      `json:"partner_order_code"`
	ChannelCode      string      `json:"channel_code"`
	Amount           string      `json:"amount"`
	RequestTime      json.Number `json:"request_time"`
	ExtraData        string      `json:"extra_data"`
	Payment          struct {
		PaymentID       string      `json:"payment_id"`
		PaidAmount      string      `json:"paid_amount"`
		Fees            json.Number `json:"fees"`
		PaymentTime     json.Number `json:"payment_time"`
		BankCode        string      `json:"bank_code"`
		BankAccountNo   string      `json:"bank_account_no"`
		BankAccountName string      `json:"bank_account_name"`
		CallbackTime    json.Number `json:"callback_time"`
		Status          json.Number `json:"status"`
	} `json:"payment"`
	Sign string `json:"sign"`
}

func (raw *rawPayInCallbackPayload) VerifySignature(secret string) error {
	actual := raw.Sign
	expected := raw.GenerateSign(secret)
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("%w, expect %s, got %s", ErrInvalidSign, expected, actual)
	}
	return nil
}

func (raw *rawPayInCallbackPayload) GenerateSign(secret string) string {
	const (
		SignatureContent = "{partner_id}:{system_order_code}:{partner_order_code}:{channel_code}:{amount}:{request_time}:{extra_data}:{payment_id}:{paid_amount}:{fees}:{payment_time}:{bank_code}:{bank_account_no}:{bank_account_name}:{callback_time}:{status}:{partner_secret}"
	)
	formater := strings.NewReplacer(
		"{partner_id}", raw.PartnerID,
		"{system_order_code}", raw.SystemOrderCode,
		"{partner_order_code}", raw.PartnerOrderCode,
		"{channel_code}", raw.ChannelCode,
		"{amount}", raw.Amount,
		"{request_time}", raw.RequestTime.String(),
		"{extra_data}", raw.ExtraData,
		"{payment_id}", raw.Payment.PaymentID,
		"{paid_amount}", raw.Payment.PaidAmount,
		"{fees}", raw.Payment.Fees.String(),
		"{payment_time}", raw.Payment.PaymentTime.String(),
		"{bank_code}", raw.Payment.BankCode,
		"{bank_account_no}", raw.Payment.BankAccountNo,
		"{bank_account_name}", raw.Payment.BankAccountName,
		"{callback_time}", raw.Payment.CallbackTime.String(),
		"{status}", raw.Payment.Status.String(),
		"{partner_secret}", secret,
	)
	signature := formater.Replace(SignatureContent)
	sign := md5.Sum(strings2.ToBytesNoAlloc(signature))
	return hex.EncodeToString(sign[:])
}

func (raw *rawPayInCallbackPayload) IsSuccess() bool {
	return raw.Payment.Status == "4"
}

type PayInCallbackRequest struct {
	raw    *rawPayInCallbackPayload
	amount decimal.Decimal
}

func (payload *PayInCallbackRequest) UnmarshalJSON(data []byte) error {
	raw := new(rawPayInCallbackPayload)
	if err := json.Unmarshal(data, raw); err != nil {
		return err
	}
	amount, err := decimal.NewFromString(raw.Amount)
	if err != nil {
		return err
	}

	payload.raw = raw
	payload.amount = amount

	return nil
}

func (payload *PayInCallbackRequest) SupplierOrderID() string {
	return payload.raw.SystemOrderCode
}

func (payload *PayInCallbackRequest) MerchantOrderID() string {
	return payload.raw.PartnerOrderCode
}

func (payload *PayInCallbackRequest) IsSuccess() bool {
	return payload.raw.IsSuccess()
}

func (payload *PayInCallbackRequest) VerifySignature(conf *Config) error {
	if conf == nil {
		return errors.New("config is nil")
	}
	if payload == nil || payload.raw == nil {
		return errors.New("payload is nil")
	}

	if conf.PartnerID != payload.raw.PartnerID {
		return fmt.Errorf("invalid appID, expect %s, got %s", conf.PartnerID, payload.raw.PartnerID)
	}
	return payload.raw.VerifySignature(conf.Secret)
}

func (payload *PayInCallbackRequest) Amount() decimal.Decimal {
	if payload.amount.IsZero() {
		payload.amount, _ = decimal.NewFromString(payload.raw.Payment.PaidAmount)
	}
	return payload.amount
}

func (payload *PayInCallbackRequest) ClientCurrency() string {
	return "VND"
}

func (payload *PayInCallbackRequest) Status() string {
	return payload.raw.Payment.Status.String()
}

func ParsePayInCallbackRequest(req *http.Request) (*PayInCallbackRequest, error) {
	if req.Method != http.MethodPost {
		return nil, fmt.Errorf("invalid method: %s", req.Method)
	}
	var payload PayInCallbackRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

type PayInCallbackReply struct{}

func (req *PayInCallbackRequest) GenerateReply() *PayInCallbackReply {
	return &PayInCallbackReply{}
}

func (reply *PayInCallbackReply) WriteTo(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("success"))
	return err
}
