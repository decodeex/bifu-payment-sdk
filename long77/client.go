package long77

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	httptransport "github.com/decodeex/bifu-payment-sdk/internal/http_transport"
)

const (
	_BASE_URL = "https://vi.long77.net/"
)

type Client struct {
	http   *http.Client
	config *Config
}

type Config struct {
	NotifyURL string // Callback url
	ReturnURL string // Success url

	PartnerID string //
	Secret    string //
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
		config: &conf,
	}, nil
}

func (c *Client) CreatePayInURL(ctx context.Context, in *PayInRequest) (*PayInResponse, error) {
	raw, err := in.toRaw(c.config)
	if err != nil {
		return nil, err
	}
	req, err := raw.GenerateSignedRequest(c.config)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var out rawPayInResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return PayInResponse{}.fromRaw(&out)
}
