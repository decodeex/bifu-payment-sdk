package mtpay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
	"golang.org/x/text/language"

	httptransport "github.com/decode-ex/payment-sdk/internal/http_transport"
	"github.com/decode-ex/payment-sdk/mtpay/internal"
)

type Config struct {
	AccessKey   string // Access key for API authentication
	SecretKey   string // Secret key for API authentication
	Endpoint    string // API endpoint URL
	CallbackURL string // Callback URL for deposit notifications
}

type Client struct {
	http_client *http.Client
	config      *Config
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.Endpoint == "" {
		return nil, fmt.Errorf("access key, secret key, and endpoint must be provided")
	}

	if cfg.CallbackURL == "" {
		return nil, fmt.Errorf("callback URL must be provided")
	}

	transport, err := httptransport.NewTransport(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	return &Client{
		http_client: &http.Client{
			Transport: transport,
		},
		config: &cfg,
	}, nil
}

type DepositRequest struct {
	raw *internal.APIMerchantDepositRequest
}

func NewDepositRequest(
	merchantOrderNo string,
	userRealName string,

	payCurrency string,
	toAmount decimal.Decimal,
) *DepositRequest {
	raw := &internal.APIMerchantDepositRequest{
		Client: internal.APIMerchantDepositRequestClient{
			RealName: userRealName,
		},
		DepositCurrency: internal.CurrencyMTC,
		FiatCurrency:    payCurrency,
		DepositAmount:   toAmount,
		MerchantOrderNo: merchantOrderNo,
		WebhookURL:      "", // set when sending the request
	}
	return &DepositRequest{
		raw: raw,
	}
}

func (req *DepositRequest) SetWebhookURL(webhookURL string) {
	req.raw.WebhookURL = webhookURL
}

func (req *DepositRequest) SetLanguage(lang language.Tag) {
	langStr := lang.String()
	if req.raw.Language == nil {
		req.raw.Language = &langStr
	} else {
		*req.raw.Language = langStr
	}
}

// func (req *DepositRequest) SetPaymentMethod(method internal.PaymentMethod) {
// 	if req.raw.PaymentMethod == nil {
// 		req.raw.PaymentMethod = &method
// 	} else {
// 		*req.raw.PaymentMethod = method
// 	}
// }

func (req *DepositRequest) Validate() error {
	return nil // Add validation logic if needed
}

func (req *DepositRequest) GenerateSignedRquest(ctx context.Context, cfg *Config) (*http.Request, error) {
	if cfg == nil {
		panic("Config cannot be nil")
	}

	req.SetWebhookURL(cfg.CallbackURL)
	return req.raw.GenerateSignedRquest(ctx, cfg.AccessKey, cfg.SecretKey)
}

type DepositResponse struct {
	raw *internal.APIMerchantDepositResponseData
}

func (resp *DepositResponse) GetCheckoutURL() string {
	return resp.raw.CheckoutURL
}

func (resp *DepositResponse) GetGatewayRequestCode() string {
	return resp.raw.RequestCode
}

func (cli *Client) Deposit(ctx context.Context, req *DepositRequest) (*DepositResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	httpReq, err := req.GenerateSignedRquest(ctx, cli.config)
	if err != nil {
		return nil, err
	}

	resp, err := cli.http_client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var responseBody internal.APIMerchantDepositResponse
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return nil, err
	}
	if !responseBody.IsSuccess {
		return nil, fmt.Errorf("API error status: %s, message: %s", responseBody.StatusCode, responseBody.Message)
	}

	return &DepositResponse{raw: responseBody.Data}, nil
}
