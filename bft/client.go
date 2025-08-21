package bft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/decode-ex/payment-sdk/bft/internal"
	httptransport "github.com/decode-ex/payment-sdk/internal/http_transport"
	"github.com/shopspring/decimal"
)

const (
	BASE_URL = "https://api.exlinked.com"
)

type Config struct {
	MerchantID     string
	DefaultPayType PayType
	PublicKey      string
	PrivateKey     string
}

type Client struct {
	http   *http.Client
	config *Config
}

func NewClient(conf Config) (*Client, error) {
	transport, err := httptransport.NewTransport(BASE_URL)
	if err != nil {
		return nil, err
	}

	return &Client{
		http: &http.Client{
			Transport: transport,
		},
		config: &conf,
	}, nil
}

type CheckoutRequest struct {
	inner *internal.CheckoutPayload
}

func NewCheckoutRequest(
	uniqueCode string,
	money decimal.Decimal,
	orderID string,
	payerName string,
) *CheckoutRequest {
	return &CheckoutRequest{
		inner: &internal.CheckoutPayload{
			UniqueCode: uniqueCode,
			Money:      money,
			OrderID:    orderID,
			PayerName:  payerName,
			PayType:    internal.PayTypeUnknown,
		},
	}
}

func (req *CheckoutRequest) SetMerchantID(merchantID string) *CheckoutRequest {
	req.inner.Uid = merchantID
	return req
}

type PayType = internal.PayType

const (
	PayTypeUnionPay PayType = internal.PayTypeUnionPay
	PayTypeAlipay   PayType = internal.PayTypeAlipay
	PayTypeWeChat   PayType = internal.PayTypeWeChat
)

func (req *CheckoutRequest) SetPayType(payType PayType) *CheckoutRequest {
	req.inner.PayType = payType
	return req
}

func (req *CheckoutRequest) SetJumpURL(jumpURL string) *CheckoutRequest {
	if jumpURL != "" {
		req.inner.JumpUrl = jumpURL
	}
	return req
}

func (req *CheckoutRequest) generateSignedRquest(ctx context.Context, conf *Config) (*http.Request, error) {
	if conf == nil {
		panic("Config cannot be nil")
	}
	if req.inner.PayType == internal.PayTypeUnknown {
		req.inner.PayType = conf.DefaultPayType
	}
	if req.inner.Uid == "" {
		req.inner.Uid = conf.MerchantID
	}
	if err := req.inner.Validate(); err != nil {
		return nil, fmt.Errorf("invalid checkout payload: %w", err)
	}

	httpReq, err := req.inner.GenerateSignedRequest(ctx, conf.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("generate signed request error: %w", err)
	}
	return httpReq, nil
}

type CheckoutReply struct {
	inner *internal.CheckoutResponse
}

func (reply *CheckoutReply) GetCheckoutURL() string {
	return reply.inner.Data
}

func (cli *Client) Checkout(ctx context.Context, req *CheckoutRequest) (*CheckoutReply, error) {

	httpReq, err := req.generateSignedRquest(ctx, cli.config)
	if err != nil {
		return nil, fmt.Errorf("generate signed request error: %w", err)
	}
	resp, err := cli.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var reply internal.CheckoutResponse
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("decode response error: %w", err)
	}

	if !reply.IsSuccess() {
		return nil, fmt.Errorf("API error status: %d, message: %s", reply.Code, reply.Message)
	}

	return &CheckoutReply{
		inner: &reply,
	}, nil
}
