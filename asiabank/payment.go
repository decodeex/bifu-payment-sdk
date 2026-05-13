package asiabank

import (
	"crypto/sha512"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

type signer struct{}

func (signer) Sign(data map[string]string, secret string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(url.QueryEscape(data[k]))
		if i != len(keys)-1 {
			sb.WriteString("&")
		}
	}
	sb.WriteString(secret)

	h := sha512.New()
	h.Write(strings2.ToBytesNoAlloc(sb.String()))
	ret := h.Sum(nil)
	return hex.EncodeToString(ret)
}

type rawPaymentForm struct {
	// Must be unique across payment request
	// string (36)
	MerchantReference string `form:"merchant_reference"`
	// Currency ISO Code 4217, e.g. MYR
	// string (3)
	Currency string `form:"currency"`
	// e.g. 10000.00,100.00, 1.00
	// double (11,2)
	Amount string `form:"amount"`
	// SHA-512 hashed signature
	// string (128)
	Sign string `form:"sign"`
	// Customer will be redirected back to URL provided
	// string (255)
	ReturnURL string `form:"return_url,omitempty"`
	// Format of IPv4
	// string (15)
	CustomeIP         string `form:"customer_ip"`
	CustomerFirstName string `form:"customer_first_name"`
	// string (128)
	CustomerLastName string `form:"customer_last_name"`
	// string (255)
	CustomerAddress string `form:"customer_address,omitempty"`
	// string (64)
	CustomerPhone string `form:"customer_phone"`
	// string (255)
	CustomerEmail string `form:"customer_email"`
	// For US and Canada only. e.g. CA, NY
	// string (2)
	CustomerState string `form:"customer_state,omitempty"`
	// ISO ALPHA-2 Code, e.g. HK, TW, US
	// string (2)
	CustomerCountry string `form:"customer_country,omitempty"`
	// e.g. DirectDebit
	// string (64)
	Network string `form:"network"`
}

func (p *rawPaymentForm) toParams() map[string]string {
	values := make(map[string]string)
	values["merchant_reference"] = p.MerchantReference
	values["currency"] = p.Currency
	values["amount"] = p.Amount
	values["return_url"] = p.ReturnURL
	values["customer_ip"] = p.CustomeIP
	values["customer_first_name"] = p.CustomerFirstName
	values["customer_last_name"] = p.CustomerLastName
	values["customer_address"] = p.CustomerAddress
	values["customer_phone"] = p.CustomerPhone
	values["customer_email"] = p.CustomerEmail
	values["customer_state"] = p.CustomerState
	values["customer_country"] = p.CustomerCountry
	values["network"] = p.Network

	return values
}

func (p *rawPaymentForm) Encode() url.Values {
	values := p.toParams()
	encoded := make(url.Values)
	for k, v := range values {
		encoded.Set(k, v)
	}
	encoded.Set("sign", p.Sign)
	return encoded
}
