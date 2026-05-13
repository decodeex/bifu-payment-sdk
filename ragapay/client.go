package ragapay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	httptransport "github.com/decodeex/bifu-payment-sdk/internal/http_transport"
	"github.com/shopspring/decimal"
)

const (
	_BASE_URL = "https://checkout.ragapay.com"
)

type Client struct {
	http *http.Client

	conf *Config
}

type Config struct {
	SuccessURL string

	PublicID string
	Password string
}

func NewClient(conf Config) (*Client, error) {
	transport, err := httptransport.NewTransport(_BASE_URL)
	if err != nil {
		return nil, err
	}

	return &Client{
		http: &http.Client{
			Transport: transport,
		},
		conf: &conf,
	}, nil
}

func (cli *Client) newCheckoutSession(ctx context.Context, payload *rawCheckoutPayload) (*rawCheckoutResponse, error) {
	req, err := payload.GenerateSignedRequest(cli.conf)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := cli.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody := rawCheckoutResponseError{}
		err := json.NewDecoder(resp.Body).Decode(&errBody)
		if err != nil || len(errBody.Errors) == 0 {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("unexpected status code: %d %s", resp.StatusCode, errBody.Errors[0].ErrorMessage)
	}

	dec := json.NewDecoder(resp.Body)
	var respBody rawCheckoutResponse
	if err := dec.Decode(&respBody); err != nil {
		return nil, err
	}
	return &respBody, nil
}

type PurchaseRequest struct {
	MerchantOrderID string
	Amount          decimal.Decimal
	Currency        string
	Description     string
}

func (req *PurchaseRequest) toRaw(conf *Config) *rawCheckoutPayload {
	dec := currencyDecimal[req.Currency]
	return &rawCheckoutPayload{
		Operation: OperationPurchase,
		Order: rawOrder{
			ID:          req.MerchantOrderID,
			Amount:      req.Amount.StringFixed(dec),
			Currency:    req.Currency,
			Description: req.Description,
		},
		SessionExpiry: 30,
		SuccessURL:    conf.SuccessURL,
	}
}

func (req *PurchaseRequest) Validate() error {
	if req.MerchantOrderID == "" {
		return errors.New("order ID is required")
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return errors.New("amount must be greater than zero")
	}

	{
		dec, supported := currencyDecimal[req.Currency]
		if !supported {
			return fmt.Errorf("unsupported currency: %s", req.Currency)
		}
		tr := req.Amount.Truncate(dec)
		if !tr.Equal(req.Amount) {
			return fmt.Errorf("unsupported currency precision: %s: %d", req.Currency, dec)
		}
	}

	return nil
}

func (cli *Client) Purchase(ctx context.Context, req *PurchaseRequest) (*PurchaseReply, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	purchase := req.toRaw(cli.conf)
	resp, err := cli.newCheckoutSession(ctx, purchase)
	if err != nil {
		return nil, err
	}
	return &PurchaseReply{
		RedirectURL: resp.RedirectURL,
	}, nil
}

type PurchaseReply struct {
	RedirectURL string
}
