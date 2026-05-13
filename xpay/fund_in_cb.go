package xpay

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

type Status = string

const (
	StatusSuccess           Status = "000"
	StatusFailed            Status = "111"
	StatusPending           Status = "001"
	StatusBankPaymentSucess Status = "002"
)

type rawFundInCallbackPayload struct {
	// Encrypted data using Xpay Encryption function for RefID, Curr Amount, Status, Amount, TransID, ValidatationKey, EncryptText,
	// Format:
	// RefID=Value&Curr=Value& Amount=Value&Status=Value&TransID=Value&ValidationKey=Value&EncryptText=Value
	Data *rawFundInCallbackPayloadData `json:"Data"`
	// MD5 encrypted text, will get the same value with EncryptText encrypted in “Data” above after decrypting.
	EncryptText string `json:"EncryptText"`
}

func (payload *rawFundInCallbackPayload) CallbackResponse() string {
	return fmt.Sprintf("%s||%s", payload.Data.TransactionID, payload.Data.ValidationKey)
}

func (payload *rawFundInCallbackPayload) Encode() string {
	dataStr := payload.Data.Encode()
	encData := fundInDataEncrypt(dataStr)

	sb := strings.Builder{}
	sb.WriteString("EncryptText=")
	sb.WriteString(payload.EncryptText)
	sb.WriteString("&Data=")
	sb.WriteString(encData)
	return sb.String()
}

func (payload *rawFundInCallbackPayload) Decode(query string) error {
	if query == "" {
		return ErrorEmptyCallbackPayload
	}
	tmp := rawFundInCallbackPayload{}
	values, err := url.ParseQuery(query)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrorInvalidCallbackPayload, err)
	}

	tmp.EncryptText = values.Get("EncryptText")
	if tmp.EncryptText == "" {
		return ErrorCallbackPayloadMissingRequiredField
	}

	dataStr := values.Get("Data")
	if dataStr == "" {
		return ErrorEmptyCallbackPayloadData
	}
	dataStr, err = fundInDataDecrypt(dataStr)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrorInvalidCallbackPayload, err)
	}
	if tmp.Data == nil {
		tmp.Data = &rawFundInCallbackPayloadData{}
	}
	if err := tmp.Data.Decode(dataStr); err != nil {
		return err
	}

	*payload = tmp
	return nil
}

func (payload *rawFundInCallbackPayload) VerifySignature(key string) error {
	if !strings.EqualFold(payload.EncryptText, payload.Data.EncryptText) {
		return fmt.Errorf("%w, two sign not equal: %s, %s", ErrInvalidSign, payload.EncryptText, payload.Data.EncryptText)
	}

	expect := payload.generateSignature(key)
	if !strings.EqualFold(expect, payload.EncryptText) {
		return fmt.Errorf("%w, expect %s, got %s", ErrInvalidSign, expect, payload.EncryptText)
	}
	return nil
}

func (payload *rawFundInCallbackPayload) generateSignature(key string) string {
	const (
		SignatureContent = "[MerchantEncryptKey]:[RefID],[Curr],[Amount],[Status],[TransID],[ValidationKey]"
	)
	formater := strings.NewReplacer(
		"[MerchantEncryptKey]", key,
		"[RefID]", payload.Data.ReferenceID,
		"[Curr]", string(payload.Data.Currency),
		"[Amount]", payload.Data.getAmountStr(),
		"[Status]", string(payload.Data.Status),
		"[TransID]", payload.Data.TransactionID,
		"[ValidationKey]", payload.Data.ValidationKey,
	)
	content := formater.Replace(SignatureContent)
	hash := md5.Sum(strings2.ToBytesNoAlloc(content))
	return hex.EncodeToString(hash[:])
}

type rawFundInCallbackPayloadData struct {
	// Reference ID or transaction ID provided by merchant.
	ReferenceID string `json:"RefID"`
	// 3-letter currency code according to ISO-4217
	Currency Currency `json:"Curr"`
	// Transaction amount
	Amount decimal.Decimal `json:"Amount"`
	// 3-letter transaction status code
	Status Status `json:"Status"`
	// Transaction ID in Xpay system
	TransactionID string `json:"TransID"`
	// Verification key that need to supply for Xpay digital signature verification
	ValidationKey string `json:"ValidationKey"`
	// MD5 encrypted text using encryption key provide by Xpay on a string built up by concatenating the other fields above
	EncryptText string `json:"EncryptText"`

	amountStr string
}

func (data *rawFundInCallbackPayloadData) getAmountStr() string {
	if data.amountStr == "" {
		data.amountStr = data.Amount.String()
	}
	return data.amountStr
}

func (data *rawFundInCallbackPayloadData) Encode() string {
	sb := strings.Builder{}
	sb.WriteString("RefID=")
	sb.WriteString(data.ReferenceID)
	sb.WriteString("&Curr=")
	sb.WriteString(string(data.Currency))
	sb.WriteString("&Amount=")
	sb.WriteString(data.getAmountStr())
	sb.WriteString("&Status=")
	sb.WriteString(data.Status)
	sb.WriteString("&TransID=")
	sb.WriteString(data.TransactionID)
	sb.WriteString("&ValidationKey=")
	sb.WriteString(data.ValidationKey)
	sb.WriteString("&EncryptText=")
	sb.WriteString(data.EncryptText)
	return sb.String()
}

func (data *rawFundInCallbackPayloadData) Decode(query string) error {
	if query == "" {
		return ErrorEmptyCallbackPayloadData
	}
	tmp := rawFundInCallbackPayloadData{}
	values, err := url.ParseQuery(query)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrorInvalidCallbackPayloadData, err)
	}
	tmp.ReferenceID = values.Get("RefID")
	tmp.Currency = Currency(values.Get("Curr"))
	tmp.amountStr = values.Get("Amount")
	tmp.Status = values.Get("Status")
	tmp.TransactionID = values.Get("TransID")
	tmp.ValidationKey = values.Get("ValidationKey")
	tmp.EncryptText = values.Get("EncryptText")

	for _, field := range []string{tmp.ReferenceID, string(tmp.Currency), tmp.amountStr, tmp.Status, tmp.TransactionID, tmp.ValidationKey, tmp.EncryptText} {
		if field == "" {
			return ErrorCallbackPayloadDataMissingRequiredField
		}
	}

	amount, err := decimal.NewFromString(tmp.amountStr)
	if err != nil {
		return fmt.Errorf("%w amount: %s", ErrorInvalidCallbackPayloadData, err)
	}
	tmp.Amount = amount

	*data = tmp
	return nil
}

func (payload *rawFundInCallbackPayload) IsSuccess() bool {
	return payload.Data.Status == StatusSuccess || payload.Data.Status == StatusBankPaymentSucess
}

type FundInCallbackRequest struct {
	raw *rawFundInCallbackPayload
}

func (req *FundInCallbackRequest) MerchantOrderID() string {
	return req.raw.Data.ReferenceID
}

func (req *FundInCallbackRequest) Amount() decimal.Decimal {
	return req.raw.Data.Amount
}

func (req *FundInCallbackRequest) Currency() Currency {
	return req.raw.Data.Currency
}

func (req *FundInCallbackRequest) Status() Status {
	return req.raw.Data.Status
}

func (req *FundInCallbackRequest) SupplierOrderCode() string {
	return req.raw.Data.TransactionID
}

func (req *FundInCallbackRequest) VerifySignature(conf *Config) error {
	if conf == nil {
		return fmt.Errorf("config is nil")
	}
	if req == nil || req.raw == nil {
		return fmt.Errorf("raw payload is nil")
	}

	return req.raw.VerifySignature(conf.Key)
}

func (req *FundInCallbackRequest) IsSuccess() bool {
	return req.raw.IsSuccess()
}

func ParseFundInCallbackRequest(req *http.Request) (*FundInCallbackRequest, error) {
	// if req.Method != http.MethodGet{
	// 	return nil, fmt.Errorf("invalid method: %s", req.Method)
	// }
	var payload rawFundInCallbackPayload
	query := req.URL.RawQuery
	if err := payload.Decode(query); err != nil {
		return nil, err
	}
	return &FundInCallbackRequest{
		raw: &payload,
	}, nil
}

type FundInCallbackReply struct {
	// Transaction ID in Xpay system.
	TransID string
	// Verification key that need to supply for Xpay digital signature verification
	ValidationKey string
}

func (req *FundInCallbackRequest) GenerateReply() *FundInCallbackReply {
	return &FundInCallbackReply{
		TransID:       req.raw.Data.TransactionID,
		ValidationKey: req.raw.Data.ValidationKey,
	}
}

func (reply *FundInCallbackReply) encode() string {
	return fmt.Sprintf("%s||%s", reply.TransID, reply.ValidationKey)
}

func (reply *FundInCallbackReply) WriteTo(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte(reply.encode()))
	return err
}
