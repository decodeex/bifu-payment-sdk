package xpay

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

type Currency = string

// THB, VND, IDR, MYR
const (
	CurrencyTHB Currency = "THB"
	CurrencyVND Currency = "VND"
	CurrencyMYR Currency = "MYR"
	CurrencyIDR Currency = "IDR"
)
const precision = 2

var currencyAllowDecimals = map[Currency]int32{
	"THB": 2,
	"VND": 0,
	"IDR": 0,
	"MYR": 2,
}

type FundInRequest struct {
	// Customer ID in merchant’s system
	CustomerID string
	// currency code
	Currency Currency
	// Transaction Amount
	Amount decimal.Decimal
	// order-id provided by merchant. This value MUST be unique.
	MerchantOrderID string
}

func (req *FundInRequest) Validate() error {
	if req.CustomerID == "" {
		return ErrorInvalidData
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return ErrorInvalidAmount
	}

	dec, supported := currencyAllowDecimals[req.Currency]
	if !supported {
		return ErrorUnsupportedCurrency
	}

	fixedStr := req.Amount.StringFixed(dec)
	de, _ := decimal.NewFromString(fixedStr)
	if !de.Equal(req.Amount) {
		return ErrorInvalidAmount
	}

	if req.MerchantOrderID == "" {
		return ErrorInvalidData
	}
	return nil
}

func (req *FundInRequest) toRaw(cfg *Config) *rawFundInPayload {
	return &rawFundInPayload{
		Data: rawFundInPayloadData{
			merchantID:      cfg.MerchantID,
			CustomerID:      req.CustomerID,
			CustomerIP:      "", // empty is ok
			Currency:        req.Currency,
			Amount:          req.Amount.StringFixed(precision),
			ReferenceID:     req.MerchantOrderID,
			transactionTime: time.Now().Format(time.DateTime),
			redirectURL:     cfg.SuccessURL,
			callbackURL:     cfg.CallbackURL,
			BankCode:        "",
			CardNo:          "",
			CardName:        "",
		},
		Remarks: "", // empty is ok
	}
}

type rawFundInPayload struct {
	// Encypted parameters using provided function sample code by XPay.
	// Format:
	// MerchantID=value&CustID=value&Curr=value&Amount=value&RefID=value&TransTime=value&ReturnURL=value&RequestURL=value&BankCode=value
	Data rawFundInPayloadData `json:"Data"`
	// Transaction remarks(Maximum 250 Characters)
	// For special character names in Vietnamese and Thai languages, please use the format cn={name}.
	// For example, cn=Hùng Nguyễn Minh or cn=วิรเทพ ยุกตะเสวี. It is recommended to use the same name as the card name parameter to avoid SIGN ERROR.
	Remarks string `json:"Remarks"`
	// Signature that hashed using MD5.
	// EncryptText string `json:"EncryptText"`
}

func (payload *rawFundInPayload) GenerateSignature(key string) string {
	const (
		SignatureContent = "[MerchantEncryptKey]:[MerchantID],[CustID],[CustIP],[Curr],[Amount],[RefID],[TransTime],[ReturnURL],[RequestURL],[BankCode],[CardNo],[CardName],[Remarks]"
	)

	formater := strings.NewReplacer(
		"[MerchantEncryptKey]", key,
		"[MerchantID]", payload.Data.merchantID,
		"[CustID]", payload.Data.CustomerID,
		"[CustIP]", payload.Data.CustomerIP,
		"[Curr]", payload.Data.Currency,
		"[Amount]", payload.Data.Amount,
		"[RefID]", payload.Data.ReferenceID,
		"[TransTime]", payload.Data.transactionTime,
		"[ReturnURL]", payload.Data.redirectURL,
		"[RequestURL]", payload.Data.callbackURL,
		"[BankCode]", payload.Data.BankCode,
		"[CardNo]", payload.Data.CardNo,
		"[CardName]", payload.Data.CardName,
		"[Remarks]", payload.Remarks,
	)

	content := formater.Replace(SignatureContent)
	hash := md5.Sum(strings2.ToBytesNoAlloc(content))
	return hex.EncodeToString(hash[:])
}

func (payload *rawFundInPayload) GenerateSignedRequest(ctx context.Context, conf *Config) (*http.Request, error) {
	const (
		apiPath = "/payment.php"
		method  = http.MethodPost
	)
	values := url.Values{}
	values.Set("Data", payload.Data.Encode())
	values.Set("Remarks", payload.Remarks)
	values.Set("EncryptText", payload.GenerateSignature(conf.Key))

	path := apiPath + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, method, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req, nil
}

type rawFundInPayloadData struct {
	// Merchant code in XPay System
	merchantID string `json:"MerchantID"`
	// Customer ID in merchant’s system.
	CustomerID string `json:"CustID"`
	// Member’s IP when redirected to XPay.
	// This is preventing phishing and fraud.
	CustomerIP string `json:"CustIP,omitempty"`
	// ISO 4217 standard 3-letter currency code.
	Currency string `json:"Curr"`
	// Transaction Amount.
	// VND and IDR do not support cent value. Please remain .00
	Amount string `json:"Amount"`
	// Reference ID provided by merchant. This value MUST be unique.
	ReferenceID string `json:"RefID"`
	// Merchant transaction time
	// Format: YYYY-MM-DD HH:MM:SS
	transactionTime string `json:"TransTime"`
	// XPay will redirect to this URL upon transaction completion.
	redirectURL string `json:"ReturnURL"`
	// XPay server will keep calling back to this URL until transaction is verified.
	callbackURL string `json:"RequestURL"`
	//If bank code is provided, it will redirect to the bank immediately, else will show XPay Bank Selection page.
	BankCode string `json:"BankCode,omitempty"`
	// For PKR currency payment method, please input "3" and 9 digit in total of 10 digits, this parameter is mandatory.
	// For THQR, please input 10 digits of the Member's actual bank account number. This parameter is mandatory.
	CardNo string `json:"CardNo,omitempty"`
	/*
		Member's actual bank account name.(For FPX,Alipay,MYRQR payment method, this parameter is mandatory.)
		For IDR OVO method, please insert PHONE NUMBER.
		For THQR please input actual Bank Name.
		For PHP currency payment method, please input "03" and 9 digit. Eg:03XXXXXXXXX.
		For EBuy method, please insert MEMBER EMAIL ADDRESS.
		For VNDQR Corporate, please insert MEMBER Actual Name.
		For PKR currency payment method, please input Member Actual Name .
	*/
	CardName string `json:"CardName,omitempty"`
}

func (data *rawFundInPayloadData) Encode() string {
	sb := strings.Builder{}
	sb.WriteString("MerchantID=")
	sb.WriteString(data.merchantID)
	sb.WriteString("&CustID=")
	sb.WriteString(data.CustomerID)
	if data.CustomerIP != "" {
		sb.WriteString("&CustIP=")
		sb.WriteString(data.CustomerIP)
	}
	sb.WriteString("&Curr=")
	sb.WriteString(data.Currency)
	sb.WriteString("&Amount=")
	sb.WriteString(data.Amount)
	sb.WriteString("&RefID=")
	sb.WriteString(data.ReferenceID)
	sb.WriteString("&TransTime=")
	sb.WriteString(data.transactionTime)
	sb.WriteString("&ReturnURL=")
	sb.WriteString(data.redirectURL)
	sb.WriteString("&RequestURL=")
	sb.WriteString(data.callbackURL)

	//sb.WriteString("&BankCode=")
	//sb.WriteString(data.BankCode)

	if data.CardNo != "" {
		sb.WriteString("&CardNo=")
		sb.WriteString(data.CardNo)
	}
	if data.CardName != "" {
		sb.WriteString("&CardName=")
		sb.WriteString(data.CardName)
	}

	return fundInDataEncrypt(sb.String())
}

var delimiters = [13]byte{0, 'g', 'h', 'G', 'k', 'g', 'J', 'K', 'I', 'h', 'i', 'j', 'H'}
var delimitersMap = map[byte]struct{}{
	'g': {}, 'h': {}, 'G': {}, 'k': {}, 'J': {}, 'K': {}, 'I': {}, 'i': {}, 'j': {}, 'H': {},
}
var hexBytes = [16]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

func fundInDataEncrypt(src string) string {
	if src == "" {
		return ""
	}

	idx := 0
	result := make([]byte, 0, len(src)*3)
	bytes := strings2.ToBytesNoAlloc(src)
	for _, b := range bytes {
		if idx == len(delimiters)-1 {
			idx = 1
		} else {
			idx += 1
		}

		suffix := byte('H')
		if idx >= len(delimiters) {
			idx = 1
		} else {
			suffix = delimiters[idx]
		}
		// to hex
		if b < 16 {
			result = append(result, hexBytes[int(b)])
		} else {
			result = append(result, hexBytes[int(b>>4)], hexBytes[int(b&0xf)])
		}

		result = append(result, suffix)
	}
	return strings2.FromBytesNoAlloc(result)
}

func fundInDataDecrypt(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	result := make([]byte, 0, len(encrypted)/2)
	bytes := strings2.ToBytesNoAlloc(encrypted)
	start, end := 0, 0
	// 为啥没用 strings.FieldsFunc, 因为一层循环就可以搞定, 用 strings.FieldsFunc 会多一层循环
	// strings.FieldsFuncSeq 是一层循环, 但是要求go 1.24 及以上
	// 因此这里自己实现
	for {
		if end >= len(bytes) {
			break
		}
		b := bytes[end]
		if _, ok := delimitersMap[b]; ok {
			// 找到了一个分隔符
			if start == end {
				// 如果分隔符前后相同, 说明是空的
				return "", fmt.Errorf("got empty data at %d-%d", start, end)
			}
			part := bytes[start:end]
			// 解析 part
			tmp, err := strconv.ParseInt(strings2.FromBytesNoAlloc(part), 16, 8)
			if err != nil {
				return "", fmt.Errorf("failed to parse part at %d-%d: %w", start, end, err)
			}
			result = append(result, byte(tmp))
			start = end + 1
		}
		end += 1
	}
	return strings2.FromBytesNoAlloc(result), nil
}
