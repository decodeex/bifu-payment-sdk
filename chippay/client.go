package chippay

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	paymentsdk "github.com/decode-ex/payment-sdk"
	httptransport "github.com/decode-ex/payment-sdk/internal/http_transport"
	"github.com/shopspring/decimal"
)

const (
	_DEV_BASE_URL  = "https://open-v2.chippaytest.com"
	_PROD_BASE_URL = "https://open-v2.chippay.com"

	_DEV_PUB_KEY_STR  = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQCBjEj/DylMlxONCDkkZQxh+woiD4goiG5WM+Ju3V2hmJpjpGCqXDClf4TLTymZMyM4GF0JL1euwgaacZ/pcxVHXpyGg8UstFUPrw7SStYURk4CLIWjuCrzZwALLGFQFNxQGFsXCR1WwpE08byw0asTWTL4VB9YlYRiV8huB/gcqwIDAQAB"
	_PROD_PUB_KEY_STR = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQCPV284s9ydOOZGCUFIw1/0d2mtC2XX8Y6oFVYtBqhno5hhI9qzUOZ+U2Raqfu8JAcbxqXaVX7MUjxlSWSHOJ5X2yiQ5GsNgNvpTKlOnv37iC/iJdajaqzyxC1mDfW+M8X6IQsWyvoRkNZ8V8WfmCPtFL7viGPbE9XKZfZApZRgXwIDAQAB"
)

type Env int

const (
	EnvDev Env = iota
	EnvProd
)

func (e Env) baseURL() string {
	switch e {
	case EnvDev:
		return _DEV_BASE_URL
	case EnvProd:
		return _PROD_BASE_URL
	default:
		return _DEV_BASE_URL
	}
}

func (e Env) PublicKey() *rsa.PublicKey {
	switch e {
	case EnvDev:
		return _DEV_PUBLIC_KEY
	case EnvProd:
		return _PROD_PUBLIC_KEY
	default:
		return _DEV_PUBLIC_KEY
	}
}

var (
	_DEV_PUBLIC_KEY  = mustDecodePublicKey(_DEV_PUB_KEY_STR)
	_PROD_PUBLIC_KEY = mustDecodePublicKey(_PROD_PUB_KEY_STR)
)

func mustDecodePublicKey(base64Key string) *rsa.PublicKey {
	pubKeyBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		panic(fmt.Errorf("failed to decode public key: %w", err))
	}
	pubKey, err := x509.ParsePKIXPublicKey(pubKeyBytes)
	if err != nil {
		panic(fmt.Errorf("failed to parse public key: %w", err))
	}
	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		panic(errors.New("invalid public key type"))
	}
	return rsaPubKey
}

type Config struct {
	MerchantID string
	PrivateKey string

	CallbackURL string
	RedirectURL string

	privateKey *rsa.PrivateKey
	env        Env
}

func (c *Config) PublicKey() *rsa.PublicKey {
	return c.env.PublicKey()
}

type Client struct {
	http   *http.Client
	config *Config
}

func NewClient(env Env, config Config) (*Client, error) {
	transport, err := httptransport.NewTransport(env.baseURL())
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	priKeyBytes, err := base64.StdEncoding.DecodeString(config.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}
	priKey, err := x509.ParsePKCS8PrivateKey(priKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	rsaPriKey, ok := priKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("invalid private key type")
	}

	return &Client{
		http: &http.Client{
			Transport: transport,
		},
		config: &Config{
			MerchantID:  config.MerchantID,
			PrivateKey:  config.PrivateKey,
			CallbackURL: config.CallbackURL,
			RedirectURL: config.RedirectURL,
			privateKey:  rsaPriKey,

			env: env,
		},
	}, nil
}

func NewDevClient(conf Config) (*Client, error) {
	return NewClient(EnvDev, conf)
}

func NewProdClient(conf Config) (*Client, error) {
	return NewClient(EnvProd, conf)
}

type BuyCoinRequest struct {
	MerchantOrderID string

	PayAmount   decimal.Decimal
	PayCurrency string
	CoinAmount  decimal.Decimal

	CustomerAreaCode string
	CustomerPhone    string
	CustomerName     string
}

func (raw *BuyCoinRequest) Validate() error {
	if raw.MerchantOrderID == "" {
		return ErrInvalidMerchantOrderID
	}

	if raw.PayAmount.LessThanOrEqual(decimal.Zero) {
		return errors.New("amount must be greater than zero")
	}

	amount := raw.PayAmount.Truncate(0)
	if !amount.Equal(raw.PayAmount) {
		return ErrInvalidAmount
	}

	if raw.PayCurrency == "" {
		return ErrInvalidCurrency
	}

	currencyAllow := false
	for _, allow := range []string{"CNY", "VND"} {
		if strings.EqualFold(raw.PayCurrency, allow) {
			currencyAllow = true
			break
		}
	}
	if !currencyAllow {
		return ErrInvalidCurrency
	}

	if raw.CustomerPhone == "" {
		return ErrInvalidCustomerPhone
	}
	if raw.CustomerName == "" {
		return ErrInvalidCustomerName
	}
	return nil

}

func (req *BuyCoinRequest) toRaw(conf *Config) *rawBuyPayload {
	return &rawBuyPayload{
		AreaCode:        req.CustomerAreaCode,
		CoinAmount:      req.CoinAmount.StringFixed(0),
		CoinSign:        CoinSignUSDT,
		CompanyOrderNum: req.MerchantOrderID,
		OrderPayChannel: OrderPayChannel_BankCard,
		OrderTime:       time.Now(),
		OrderType:       OrderTypeBuy,
		PayCoinSign:     strings.ToLower(req.PayCurrency),
		Phone:           req.CustomerPhone,
		Total:           req.PayAmount.StringFixed(0),
		UserName:        req.CustomerName,

		CompanyID: conf.MerchantID,
		SyncURL:   conf.RedirectURL,
		AsyncUrl:  conf.CallbackURL,
	}
}

type BuyCoinReply struct {
	SupplyOrderNum string
	RedirectURL    string
}

func (BuyCoinReply) fromRaw(raw *rawBuyResponse) (*BuyCoinReply, error) {
	if raw == nil {
		return nil, errors.New("raw response is nil")
	}
	if raw.Code != StatusCodeSuccess {
		return nil, fmt.Errorf("failed to buy coin: %s", raw.Message)
	}
	return &BuyCoinReply{
		SupplyOrderNum: raw.Data.OrderNo,
		RedirectURL:    raw.Data.Link,
	}, nil
}

// BuyCoin 快捷订单
// https://open-v2.chippay.com/api/cnAPI.html
func (c *Client) BuyCoin(ctx context.Context, req *BuyCoinRequest) (*BuyCoinReply, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	raw := req.toRaw(c.config)
	buyReq, err := raw.GenerateSignedRequest(ctx, c.config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signed request: %w", err)
	}
	resp, err := c.http.Do(buyReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	rawReply := raw.Reply()
	if err := json.NewDecoder(resp.Body).Decode(&rawReply); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return BuyCoinReply{}.fromRaw(&rawReply)
}

type IntentOrderRequest struct {
	MerchantOrderID string

	PayAmount decimal.Decimal
	// PayCurrency string
}

func (raw *IntentOrderRequest) Validate() error {
	if raw.MerchantOrderID == "" {
		return ErrInvalidMerchantOrderID
	}

	if raw.PayAmount.LessThanOrEqual(decimal.Zero) {
		return errors.New("amount must be greater than zero")
	}

	amount := raw.PayAmount.Truncate(0)
	if !amount.Equal(raw.PayAmount) {
		return ErrInvalidAmount
	}

	return nil

}

func (req *IntentOrderRequest) toRaw(ctx context.Context, conf *Config) *rawAddIntentOrderPayload {
	callbackURL := paymentsdk.GetCallbackURL(ctx, conf.CallbackURL)

	return &rawAddIntentOrderPayload{
		CompanyOrderNum: req.MerchantOrderID,
		TotalAmount:     int32(req.PayAmount.IntPart()),

		CompanyID: conf.MerchantID,
		SyncURL:   conf.RedirectURL,
		AsyncUrl:  callbackURL,
	}
}

type IntentOrderReply struct {
	SupplyOrderNum string
	RedirectURL    string
}

func (IntentOrderReply) fromRaw(raw *rawAddIntentOrderResponse) (*IntentOrderReply, error) {
	if raw == nil {
		return nil, errors.New("raw response is nil")
	}
	if raw.Code != StatusCodeSuccess {
		return nil, fmt.Errorf("failed to add intent order: %s", raw.Message)
	}
	return &IntentOrderReply{
		SupplyOrderNum: raw.Data.OrderNo,
		RedirectURL:    raw.Data.Link,
	}, nil
}

// AddIntentOrder 创建自选订单
// https://open-v2.chippay.com/api/addIntentOrder.html
func (cli *Client) AddIntentOrder(ctx context.Context, req *IntentOrderRequest) (*IntentOrderReply, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	raw := req.toRaw(ctx, cli.config)
	intentReq, err := raw.GenerateSignedRequest(ctx, cli.config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signed request: %w", err)
	}
	resp, err := cli.http.Do(intentReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	rawReply := raw.Reply()
	if err := json.NewDecoder(resp.Body).Decode(&rawReply); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return IntentOrderReply{}.fromRaw(&rawReply)
}
