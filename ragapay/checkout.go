package ragapay

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

type Operation = string

const (
	OperationPurchase = "purchase"
	OperationDebit    = "debit"
	OperationTransfer = "transfer"
)

type URLTarget = string

const (
	URLTargetBlank  = "_blank"
	URLTargetSelf   = "_self"
	URLTargetParent = "_parent"
	URLTargetTop    = "_top"
)

type rawCheckoutPayload struct {
	// Key for Merchant identification
	MerchantKey string `json:"merchant_key"`
	// Defines a payment transaction
	Operation Operation `json:"operation"`
	// An array of payment methods. Limits the available methods on the Checkout page (the list of the possible values in the Payment methods section).
	// In the case of parameter absence, the pre-routing rules are applied.
	// If pre-routing rules are not configured, all available payment methods are displayed.
	Methods []string `json:"methods,omitempty"`
	// This parameter is used to direct payments to a specific sub-account (channel).
	// If the channel is configured for Merchant Mapping, the system matches the value with the corresponding channel_id value in the request to route the payment.
	//
	// Note: The channel must correspond to one of the payment methods (brands) listed in the methods array.
	// If the methods array is empty, only the channel_id will affect the selection of the payment method (Merchant Mapping).
	//
	// max length: 16
	ChannelID string `json:"channel_id,omitempty"`
	// Session expiration time in minutes.
	// Default value = 60, Could not be zero.
	//
	// range: 1-720
	SessionExpiry int `json:"session_expiry,omitempty"`
	// URL to redirect the Customer in case of the successful payment
	//
	// Valid URL, max length: 1024
	SuccessURL string `json:"success_url"`
	// URL to return Customer in case of a payment cancellation (“Close” button on the Checkout page).
	//
	// Valid URL, min: 0 max: 1024
	CancelURL string `json:"cancel_url,omitempty"`
	// URL where the payer will be redirected in case of session expiration
	ExpiryURL string `json:"expiry_url,omitempty"`
	// URL to return Customer in case of undefined transaction status.
	// If the URL is not specified, the cancel_url is used for redirection.
	//
	// Valid URL, min: 0 max: 1024
	ErrorURL string `json:"error_url,omitempty"`
	// Name of, or keyword for a browsing context where Customer should be returned according to HTML specification.
	URLTarget URLTarget `json:"url_target,omitempty"`
	// Special attribute pointing for further tokenization
	// If the card_token is specified, req_token will be ignored.
	// For purchase and debit operations.
	RequestToken bool `json:"req_token,omitempty"`
	// Credit card token value
	// For purchase and debit operations.
	CardToken string `json:"card_token,omitempty"`
	// Initialization of the transaction with possible following recurring
	// Only for purchase operation
	RecurringInit bool `json:"recurring_init,omitempty"`
	// Schedule ID for recurring payments
	// Only for purchase operation
	// It's available when recurring_init = true
	ScheduleID string `json:"schedule_id,omitempty"`
	// Indicates the need of calculation for the VAT amount
	// - 'true':  if VAT calculation needed
	// - 'false': if VAT should not be calculated for current payment.
	// Only for purchase operation
	VatCalc bool `json:"vat_calc,omitempty"`
	// Special signature to validate your request to Payment Platform Addition in Signature section.
	Hash string `json:"hash"`
	// Information about an order
	Order rawOrder `json:"order"`
	// Customer's information
	Customer *rawUserInfo `json:"customer,omitempty"`
	// Billing address information.
	// Condition: If the object or some object's parameters are NOT specified in the request, then it will be displayed on the Checkout page (if a payment method needs)
	BillAddress *rawBillingAddress `json:"bill_address,omitempty"`
	// Payee's information.
	// Specify additional information about Payee for transfer operation if it is required by payment provider.
	Payee *rawUserInfo `json:"payee,omitempty"`
	// Billing address information for Payee.
	PayeeBillingAddress *rawBillingAddress `json:"payee_bill_address,omitempty"`
	// Additional information regarding crypto transactions
	Crypto *rawCryptoInfo `json:"crypto,omitempty"`
	// Extra-parameters required for specific payment method
	Parameters map[string]any `json:"parameters,omitempty"`
	// Custom data
	// This block can contain arbitrary data, which will be returned in the callback.
	CustomData map[string]string `json:"custom_data,omitempty"`
}

func (payload *rawCheckoutPayload) GenerateSignature(password string) string {
	const (
		SignatureContent = "{OrderNumber}{OrderAmount}{OrderCurrency}{OrderDescription}{MerchantPassword}"
	)
	formater := strings.NewReplacer(
		"{OrderNumber}", payload.Order.ID,
		"{OrderAmount}", payload.Order.Amount,
		"{OrderCurrency}", payload.Order.Currency,
		"{OrderDescription}", payload.Order.Description,
		"{MerchantPassword}", password,
	)
	content := strings.ToUpper(formater.Replace(SignatureContent))
	s1 := md5.Sum(strings2.ToBytesNoAlloc(content))
	s1Hex := hex.EncodeToString(s1[:])
	s2 := sha1.Sum(strings2.ToBytesNoAlloc(s1Hex))
	return hex.EncodeToString(s2[:])
}

func (payload *rawCheckoutPayload) GenerateSignedRequest(conf *Config) (*http.Request, error) {
	const (
		path = "/api/v1/session"
	)
	payload.MerchantKey = conf.PublicID
	payload.Hash = payload.GenerateSignature(conf.Password)

	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

type rawOrder struct {
	// Order ID
	// max length: 255
	// [a-z A-Z 0-9 -!"#$%&'()*+,./:;&@]
	ID string `json:"number"`
	// Format depends on currency.
	// - Send Integer type value for currencies with zero-exponent.
	// - Send Float type value for currencies with exponents 2, 3, 4.
	// - For crypto currencies use the exponent as appropriate for the specific currency.
	Amount string `json:"amount"`
	// Currency
	// 3 characters for fiat currencies and from 3 to 6 characters for crypto currencies
	Currency string `json:"currency"`
	// Product name
	// min: 2 max: 1024
	// [a-z A-Z 0-9 !"#$%&'()*+,./:;&@]
	Description string `json:"description"`
}

type rawUserInfo struct {
	// Customer's name
	// Condition: If the parameter is NOT specified in the request, then it will be displayed on the Checkout page (if a payment method needs) - the "Cardholder" field
	// min: 2 max: 32, Latin basic
	Name string `json:"name,omitempty"`
	// Customer's email address
	// Condition: If the parameter is NOT specified in the request, then it will be displayed on the Checkout page (if a payment method needs) - the "E-mail" field
	// min: 2 max: 255, email format
	Email string `json:"email,omitempty"`
}

type rawBillingAddress struct {
	// Billing country
	// 2 characters, e.g. 'US'
	Country string `json:"country"`
	// Billing state
	// min: 2 max: 32 [a-z A-Z]
	// It is 2-letters code for USA, Canada, Australia, Japan, India
	// e.g. CA
	State string `json:"state"`
	// Billing city
	// min: 2 max: 40 [a-z A-Z 0-9 - space]
	// e.g. Los Angeles
	City string `json:"city"`
	// City district
	// min: 2 max: 32 [a-z A-Z 0-9 - space]
	// e.g. Beverlywood
	District string `json:"district"`
	// Billing address
	// min: 2 max: 32 [a-z A-Z 0-9]
	// e.g. Moor Building
	Address string `json:"address"`
	// House number
	// min: 1 max: 9 [a-z A-Z 0-9/ - space]
	// e.g. '12/1'
	HouseNumber string `json:"house_number"`
	// Billing zip code
	// min: 2 max: 10 [a-z A-Z 0-9]
	// e.g. 123456, MK77
	Zip string `json:"zip"`
	// Customer phone number
	// min: 1 max: 32 [0-9 + () -]
	// e.g. 347771112233
	Phone string `json:"phone"`
}

type rawCryptoInfo struct {
	// You can use an arbitrary value or select one from the following.
	// ERC20, TRC20, BEP20, BEP2, OMNI, solana, polygon
	Network string `json:"network"`
}

type rawCheckoutResponse struct {
	RedirectURL string `json:"redirect_url"`
}

type rawCheckoutResponseError struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Errors       []struct {
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	} `json:"errors"`
}

// AED,AUD,BGN,CAD,CHF,CNY,CZK,DKK,EUR,GBP,HKD,HRK,HUF,IDR,ILS,INR,JPY,KES,MXN,MYR,NGN,NOK,NZD,PHP,PLN,QAR,RON,RUB,SAR,SEK,SGD,THB,TRY,UGX,USD,ZAR 序列化后, 必须两位小数
// BHD,KWD,OMR, 序列化后,必须三位小数
// VND 只能是整数

var currencyDecimal = map[string]int32{
	"USD": 2, "GBP": 2, "EUR": 2, "AED": 2, "CNY": 2, "INR": 2, "AUD": 2,
	"BGN": 2, "CAD": 2, "CHF": 2, "CZK": 2, "DKK": 2, "HKD": 2, "HRK": 2, "HUF": 2, "IDR": 2, "ILS": 2, "JPY": 2, "KES": 2, "MXN": 2, "MYR": 2, "NGN": 2, "NOK": 2, "NZD": 2, "PHP": 2, "PLN": 2, "QAR": 2, "RON": 2, "RUB": 2, "SAR": 2, "SEK": 2, "SGD": 2, "THB": 2, "TRY": 2, "UGX": 2, "ZAR": 2,
	"BHD": 3, "KWD": 3, "OMR": 3,
	"VND": 0,
}
