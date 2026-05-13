package help2pay

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
	"golang.org/x/text/language"
)

type LanguageCode = string

const (
	LanguageCode_EN  LanguageCode = "en-us"  // English
	LanguageCode_CN  LanguageCode = "zh-cn"  // Chinese Simplified
	LanguageCode_TH  LanguageCode = "th"     // Thai
	LanguageCode_MY  LanguageCode = "ms-my"  // Malay (Malaysia)
	LanguageCode_VI  LanguageCode = "vi-vn"  // Vietnamese (Vietnam)
	LanguageCode_ID  LanguageCode = "id-id"  // Indonesian
	LanguageCode_BUR LanguageCode = "bur"    // Burmese
	LanguageCode_FIL LanguageCode = "fil-ph" // Tagalog (Philippines)
	LanguageCode_HI  LanguageCode = "hi-in"  // Hindi (India)
	LanguageCode_KM  LanguageCode = "km-kh"  // Khmer (Cambodia)
)

var (
	// 顺序必须保持对应
	languageCodes = []string{LanguageCode_EN, LanguageCode_CN, LanguageCode_TH, LanguageCode_MY, LanguageCode_VI, LanguageCode_ID, LanguageCode_BUR, LanguageCode_FIL, LanguageCode_HI, LanguageCode_KM}
	langMatcher   = language.NewMatcher([]language.Tag{
		language.English,
		language.Chinese,
		language.Thai,
		language.Malay,
		language.Vietnamese,
		language.Indonesian,
		language.Burmese,
		language.Filipino, // MAYBUG
		language.Hindi,
		language.Khmer,
	})
)

func getLanguageCode(lang language.Tag) LanguageCode {
	_, i, _ := langMatcher.Match(lang)
	return languageCodes[i]
}

type CurrencyCode = string

const (
	CurrencyCodeMYR CurrencyCode = "MYR"
	CurrencyCodeTHB CurrencyCode = "THB"
	CurrencyCodeVND CurrencyCode = "VND"
	CurrencyCodeIDR CurrencyCode = "IDR"
	CurrencyCodeINR CurrencyCode = "INR"
	CurrencyCodePHP CurrencyCode = "PHP"
)

type rawDepositFormRequest struct {
	// Registered merchant code with Gateway.
	// 1<len<50
	Merchant string `form:"Merchant"`
	// International currency code, e.g. MYR, THB
	// len==3
	Currency CurrencyCode `form:"Currency"`
	// Merchant’s customer ID that identify their customer
	// Customer ID is frequent use as user identifier for gateway
	// Customer ID must be unique per user
	// 1<len<50
	Customer string `form:"Customer"`
	// Transaction ID created by Merchant with reference
	// to each payment transaction. Reference must be unique.
	// 1<len<50
	Reference string `form:"Reference"`
	// A generated hash key for determining the validity of the payment submission by the merchant to gateway to prevent fraudulent activity.
	// 1~500
	Key string `form:"Key"`
	// Fiat and cryptocurrency may have different decimal places
	// Fiat Numerical figures with 2 decimal places.
	// Cryptocurrency
	//     TET-ETHE (USDT - ERC20) Numerical figures with 6 decimal places
	//     TEX-TRON (USDT - TRC20) Numerical figures with 2 decimal places
	// Important:
	// VND, IDR currency and PPTP (THB currency) Will Only Allow .00 decimal submission.
	// Cryptocurrency Will Only Allow .00 decimal submission.
	// 1<len<20
	Amount string `form:"Amount"`
	// Column provided to merchant for Note usage.
	// 1<len<500
	Note string `form:"Note,omitempty"`
	// Transaction time in the format of YYYY-MM-DD hh:mm:sstt
	// e.g. 2012-05-01 08:04:00AM, 2012-08-15 08:00:00PM
	// 1<len<500
	Datetime time.Time `form:"Datetime,omitempty"`
	// The URL to receive transaction status from Gateway to Merchant
	// that will display the transaction status to customer in Merchant’s front-end site.
	// 1<len<500
	FrontURI string `form:"FrontURI"`
	// The URL or HTTP handler to receive transaction status from Gateway to Merchant
	// for Merchant to update their backend system.
	// 1<len<500
	BackURI string `form:"BackURI"`
	// Bank code provided by the Gateway.
	// 1<len<50
	Bank string `form:"Bank"`
	// Language selection that displays to Customer during the submission process.
	// 1<len<10
	Language LanguageCode `form:"Language"`
	// Customer’s IP
	// 1<len<20
	ClientIP string `form:"ClientIP"`
	// Company name will display in deposit page
	// if merchant want customer to acknowledge the transaction is made to the merchant.
	// 1<len<100
	CompanyName string `form:"CompanyName,omitempty"`
}

func (arg *rawDepositFormRequest) Encode() url.Values {
	const (
		TimeFormat = "2006-01-02 03:04:05PM"
	)
	val := url.Values{}
	val.Add("Merchant", arg.Merchant)
	val.Add("Currency", arg.Currency)
	val.Add("Customer", arg.Customer)
	val.Add("Reference", arg.Reference)
	val.Add("Key", arg.Key)
	val.Add("Amount", arg.Amount)
	val.Add("Note", arg.Note)
	val.Add("Datetime", arg.Datetime.Format(TimeFormat))
	val.Add("FrontURI", arg.FrontURI)
	val.Add("BackURI", arg.BackURI)
	val.Add("Bank", arg.Bank)
	val.Add("Language", string(arg.Language))
	val.Add("ClientIP", arg.ClientIP)
	val.Add("CompanyName", arg.CompanyName)

	return val
}

var (
	_DepositFormRequestPath, _ = url.Parse("/MerchantTransfer")
)

func (raw *rawDepositFormRequest) Path() *url.URL {
	return _DepositFormRequestPath
}

type signer struct{}

// A generated hash key for determining the validity of
// the payment submission by the merchant to gateway to prevent fraudulent activity.
func (signer) SignRequest(arg *rawDepositFormRequest, securityCode string) string {
	const (
		Template   = "{Merchant}{Reference}{Customer}{Amount}{Currency}{Datetime}{SecurityCode}{ClientIP}"
		TimeFormat = "20060102150405"
	)

	formater := strings.NewReplacer(
		"{Merchant}", arg.Merchant,
		"{Reference}", arg.Reference,
		"{Customer}", arg.Customer,
		"{Amount}", arg.Amount,
		"{Currency}", arg.Currency,
		"{Datetime}", arg.Datetime.Format(TimeFormat),
		"{SecurityCode}", securityCode,
		"{ClientIP}", arg.ClientIP,
	)
	raw := formater.Replace(Template)

	tmp := md5.Sum(strings2.ToBytesNoAlloc(raw))
	sum := hex.EncodeToString(tmp[:])
	return strings.ToUpper(sum)
}

func (signer) SignCallback(cb *rawDepositCallbackPayload, securityCode string) string {
	const (
		Template = "{Merchant}{Reference}{Customer}{Amount}{Currency}{Status}{SecurityCode}"
	)
	formater := strings.NewReplacer(
		"{Merchant}", cb.Merchant,
		"{Reference}", cb.Reference,
		"{Customer}", cb.Customer,
		"{Amount}", cb.Amount,
		"{Currency}", cb.Currency,
		"{Status}", cb.Status,
		"{SecurityCode}", securityCode,
	)
	raw := formater.Replace(Template)
	tmp := md5.Sum(strings2.ToBytesNoAlloc(raw))
	return hex.EncodeToString(tmp[:])
}

type DepositReply struct {
	RedirectUrl string `json:"redirect_url"`
}
