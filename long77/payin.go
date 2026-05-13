package long77

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

var ErrInvalidAmount = errors.New("invalid amount")

type rawPayInPayload struct {
	//  Partner ID
	// varchar(32)
	PartnerID string `json:"partner_id"`
	// Timestamp
	// int(10)
	Timestamp string `json:"timestamp"`
	// Random characters
	// string
	Random string `json:"random"`
	// Unique Transaction ID from Partner's System[A-Za-z0-9_-]{3,40}
	// varchar(40)
	PartnerOrderCode string `json:"partner_order_code"`
	// Amount of Payment
	// decimal(12,0)
	Amount string `json:"amount"`
	// Customer name
	// varchar(40)
	CustomerName string `json:"customer_name,omitempty"`
	// Payee name displayed on the page[a-zA-Z0-9 ]{0,32}When this item is not empty, ustomer_name will be invalid
	// varchar(32)
	PayEEName string `json:"payee_name,omitempty"`
	// Notification url
	// varchar(255)
	NotifyURL string `json:"notify_url"`
	// URI for redirecting after the transaction finish
	// varchar(255)
	ReturnURL string `json:"return_url,omitempty"`
	// Other additional parameters
	// varchar(32)
	ExtraData string `json:"extra_data,omitempty"`
	// md5(partner_id:timestamp:random:partner_order_code:amount:customer_name:payee_name:notify_url:return_url:extra_data:partner_secret)
	// varchar(32)
	Sign string `json:"sign"`
}

type PayInRequest struct {
	MerchantOrderID string
	Amount          decimal.Decimal
}

func (payload *PayInRequest) toRaw(cfg *Config) (*rawPayInPayload, error) {
	if !payload.Amount.IsInteger() {
		return nil, ErrInvalidAmount
	}
	return &rawPayInPayload{
		PartnerID:        cfg.PartnerID,
		PartnerOrderCode: payload.MerchantOrderID,
		Amount:           payload.Amount.StringFixed(0),
		CustomerName:     "",
		NotifyURL:        cfg.NotifyURL,
		ReturnURL:        cfg.ReturnURL,
		ExtraData:        "",
	}, nil
}

func (raw *rawPayInPayload) GenerateSignedRequest(conf *Config) (*http.Request, error) {
	const (
		PATH   = "/gateway/bnb/createVA.do"
		METHOD = http.MethodGet
	)

	_ = raw.GenerateSign(conf.Secret)

	valus := url.Values{}
	valus.Set("partner_id", raw.PartnerID)
	valus.Set("timestamp", raw.Timestamp)
	valus.Set("random", raw.Random)
	valus.Set("partner_order_code", raw.PartnerOrderCode)
	valus.Set("amount", raw.Amount)
	valus.Set("customer_name", raw.CustomerName)
	valus.Set("payee_name", raw.PayEEName)
	valus.Set("notify_url", raw.NotifyURL)
	valus.Set("return_url", raw.ReturnURL)
	valus.Set("extra_data", raw.ExtraData)
	valus.Set("sign", raw.Sign)

	path := PATH + "?" + valus.Encode()

	return http.NewRequest(METHOD, path, nil)
}

func (raw *rawPayInPayload) GenerateSign(secret string) string {
	const (
		SignatureContent = "{partner_id}:{timestamp}:{random}:{partner_order_code}:{amount}:{customer_name}:{payee_name}:{notify_url}:{return_url}:{extra_data}:{partner_secret}"
	)
	randomBs := make([]byte, 16)
	_, _ = rand.Read(randomBs)
	randomStr := hex.EncodeToString(randomBs)

	ts := strconv.FormatInt(time.Now().Unix(), 10)

	formater := strings.NewReplacer(
		"{partner_id}", raw.PartnerID,
		"{timestamp}", ts,
		"{random}", randomStr,
		"{partner_order_code}", raw.PartnerOrderCode,
		"{amount}", raw.Amount,
		"{customer_name}", raw.CustomerName,
		"{payee_name}", raw.PayEEName,
		"{notify_url}", raw.NotifyURL,
		"{return_url}", raw.ReturnURL,
		"{extra_data}", raw.ExtraData,
		"{partner_secret}", secret,
	)
	signature := formater.Replace(SignatureContent)
	sign := md5.Sum(strings2.ToBytesNoAlloc(signature))

	// TODO: 这个写法不好
	raw.Timestamp = ts
	raw.Random = randomStr
	raw.Sign = hex.EncodeToString(sign[:])

	return raw.Sign
}

type rawPayInResponse struct {
	Code    int    `json:"code"` // code == 200 is success, != 200 is error
	Message string `json:"msg"`  // Response message
	Data    struct {
		PartnerID        string          `json:"partner_id"`         // Partner ID
		SystemOrderCode  string          `json:"system_order_code"`  // Unique Payment ID from Long77
		PartnerOrderCode string          `json:"partner_order_code"` // Unique Transaction ID from Partner's System
		Amount           decimal.Decimal `json:"amount"`             // Amount of Payment
		RequestTime      json.Number     `json:"request_time"`       // Request time
		BankAccount      struct {
			BankCode          string `json:"bank_code"`         // The Bank code
			BankName          string `json:"bank_name"`         // The Bank name
			BankAccountNumber string `json:"bank_account_no"`   // The Bank account number
			BankAccountName   string `json:"bank_account_name"` // The Bank account name
		} `json:"bank_account"` // Receiving account information
		PaymentID  string `json:"payment_id"`  // Unique Payment ID from bank
		PaymentURL string `json:"payment_url"` // Payment URL from bank
	} `json:"data"` // Return parameter set
}
type PayInResponse struct {
	SupplierOrderCode string
	PaymentID         string
	PaymentURL        string
}

func (PayInResponse) fromRaw(raw *rawPayInResponse) (*PayInResponse, error) {
	if raw.Code != 200 {
		return nil, fmt.Errorf("code: %d, message: %s", raw.Code, raw.Message)
	}
	return &PayInResponse{
		SupplierOrderCode: raw.Data.SystemOrderCode,
		PaymentID:         raw.Data.PaymentID,
		PaymentURL:        raw.Data.PaymentURL,
	}, nil
}

type rawPaymentDetailResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		PartnerID        string `json:"partner_id"`
		SystemOrderCode  string `json:"system_order_code"`
		PartnerOrderCode string `json:"partner_order_code"`
		Amount           string `json:"amount"`
		RequestTime      string `json:"request_time"`
		ExtraData        string `json:"extra_data"`
		BankAccount      struct {
			BankCode        string `json:"bank_code"`
			BankName        string `json:"bank_name"`
			BankAccountNo   string `json:"bank_account_no"`
			BankAccountName string `json:"bank_account_name"`
		} `json:"bank_account"`
		Payment struct {
			PaidAmount   string `json:"paid_amount"`
			Fees         string `json:"fees"`
			PaymentTime  string `json:"payment_time"`
			CallbackTime string `json:"callback_time"`
			PaymentID    string `json:"payment_id"`
			PaymentURL   string `json:"payment_url"`
			Status       string `json:"status"`
		} `json:"payment"`
	} `json:"data"`
}
