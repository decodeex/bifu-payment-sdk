package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// This field is a bitwise enumeration
// Each value represents a payment method and can be combined using bitwise operations.
type PaymentMethod = int32

const (
	PaymentMethodBankCard  PaymentMethod = 1 << iota // BankCard
	PaymentMethodWeChatPay                           // WeChatPay
	PaymentMethodAlipay                              // Alipay
)

type Language = string

const (
	LanguageEn   = "en"    // English
	LanguageZhCN = "zh_CN" // Simplified Chinese
	LanguageZhTW = "zh_TW" // Traditional Chinese
)

type Currency = string

const (
	CurrencyMTC = "MTC" // MTC currency
	CurrencyCNY = "CNY" // Chinese Yuan
	CurrencyHKD = "HKD" // Hong Kong Dollar
)

func IsDepositCurrencyValid(currency Currency) bool {
	return currency == CurrencyMTC || currency == CurrencyCNY || currency == CurrencyHKD
}

type CurrencyCode = string

type APIMerchantDepositRequest struct {
	Client          APIMerchantDepositRequestClient `json:"client"`                  // Client information
	DepositCurrency Currency                        `json:"depositCurrency"`         // Currency type for the deposit (e.g. MTC/CNY/HKD)
	FiatCurrency    CurrencyCode                    `json:"fiatCurrency"`            // Fiat currency code (e.g. CNY/HKD)
	DepositAmount   decimal.Decimal                 `json:"depositAmount"`           // Amount to Deposit
	MerchantOrderNo string                          `json:"merchantOrderNo"`         // Unique merchant order number
	WebhookURL      string                          `json:"webhookUrl"`              // Webhook URL for notifications
	Language        *Language                       `json:"language,omitempty"`      // Preferred language (default: en)
	PaymentMethod   *PaymentMethod                  `json:"paymentMethod,omitempty"` // Payment method (optional)
}

func (req *APIMerchantDepositRequest) Validate() error {
	if req.Client.RealName == "" {
		return errors.New("client.realName cannot be empty")
	}
	if req.DepositCurrency == "" {
		return errors.New("depositCurrency cannot be empty")
	}
	if !IsDepositCurrencyValid(req.DepositCurrency) {
		return fmt.Errorf("unsupported depositCurrency: %s", req.DepositCurrency)
	}
	if req.FiatCurrency == "" {
		return errors.New("fiatCurrency cannot be empty")
	}
	if req.DepositAmount.LessThanOrEqual(decimal.Zero) {
		return errors.New("depositAmount must be greater than zero")
	}
	if req.MerchantOrderNo == "" {
		return errors.New("merchantOrderNo cannot be empty")
	}
	if req.WebhookURL == "" {
		return errors.New("webhookUrl cannot be empty")
	}

	if req.Language != nil {
		switch lang := *req.Language; lang {
		case LanguageEn, LanguageZhCN, LanguageZhTW:
		default:
			return fmt.Errorf("unsupported language: %s", lang)
		}
	}

	return nil
}

func (req *APIMerchantDepositRequest) GenerateSignedRquest(ctx context.Context, accessKey, secretKey string) (*http.Request, error) {
	const (
		PATH         = "/api/v2/merchant/deposit"
		METHOD       = http.MethodPost
		CONTENT_TYPE = "application/json;charset=UTF-8"
	)

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(req); err != nil {
		return nil, err
	}

	timestampMs := time.Now().UnixMilli()
	signature := GenerateSignature(accessKey, secretKey, timestampMs)
	httpReq, err := http.NewRequestWithContext(ctx, METHOD, PATH, &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", CONTENT_TYPE)
	httpReq.Header.Set("access_key", accessKey)
	httpReq.Header.Set("timestamp", strconv.FormatInt(timestampMs, 10))
	httpReq.Header.Set("signature", signature)

	return httpReq, nil
}

type APIMerchantDepositRequestClient struct {
	RealName     string `json:"realName"`               // Client's real name
	RegisteredAt int64  `json:"registeredAt,omitempty"` // Registration milliseconds timestamp (optional)
}

type APIMerchantDepositResponseData struct {
	CheckoutURL string `json:"checkoutUrl"` // URL for completing the deposit
	RequestCode string `json:"requestCode"` // Request tracking order
	IsAvailable bool   `json:"isAvailable"` // Availability of the deposit
	ExpiresAt   int64  `json:"expiresAt"`   // Expiration timestamp in millisecond
	CreatedAt   int64  `json:"createdAt"`   // Creation timestamp in millisecond
}

type APIResponseCode = string

const (
	APIResponseCodeSuccess            = "SUCCESS"              // Request completed successfully.
	APIResponseCodeAccessKeyError     = "ACCESS_KEY_ERROR"     // Invalid or missing accessKey.
	APIResponseCodeAccountStatusError = "ACCOUNT_STATUS_ERROR" // Account is suspended or disabled.
	APIResponseCodeSignatureError     = "SIGNATURE_ERROR"      // Signature verification failed.
	APIResponseCodeTimestampError     = "TIMESTAMP_ERROR"      // Request timestamp is invalid or expired.
	APIResponseCodeParameterError     = "PARAMETER_ERROR"      // One or more request parameters are incorrect or missing.
	APIResponseCodeSystemError        = "SYSTEM_ERROR"         // Internal system error. Please try again later.
)

type APIResonse[D any] struct {
	Data       *D              `json:"data"`       // Response payload of generic type T.
	IsSuccess  bool            `json:"isSuccess"`  // Indicates whether the request was successful.
	StatusCode APIResponseCode `json:"statusCode"` // Enum value indicating the result of the request.
	Message    string          `json:"message"`    // Description of the result or error message.
	Version    string          `json:"version"`    // API version identifier. Default value: "2.0".
}

type APIMerchantDepositResponse = APIResonse[APIMerchantDepositResponseData]
