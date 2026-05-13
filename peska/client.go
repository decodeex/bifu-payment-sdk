package peska

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	httptransport "github.com/decodeex/bifu-payment-sdk/internal/http_transport"
)

const (
	_DEV_BASE_URL  = "https://demo-transfer.peska.co/"
	_PROD_BASE_URL = "https://transfer.peska.co/"
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

type Client struct {
	http *http.Client
	conf *Config
	env  Env
}

type Config struct {
	CallbackURL   string
	SuccessURL    string
	MerchantEmail string

	Secret []byte
	Key    string
}

func NewClient(env Env, conf Config) (*Client, error) {
	transport, err := httptransport.NewTransport(env.baseURL())
	if err != nil {
		return nil, err
	}

	return &Client{
		http: &http.Client{
			Transport: transport,
		},
		conf: &conf,
		env:  env,
	}, nil
}

func NewDevClient(conf Config) (*Client, error) {
	return NewClient(EnvDev, conf)
}

func NewProdClient(conf Config) (*Client, error) {
	return NewClient(EnvProd, conf)
}

func (cli *Client) CreatePayInURL(ctx context.Context, payload *PayInRequest) (*PayInReply, error) {
	raw := payload.toRaw(cli.conf)
	req, err := raw.GenerateSignedRequest(ctx, cli.env, cli.conf)
	if err != nil {
		return nil, fmt.Errorf("generate signed request failed: %w", err)
	}

	resp, err := cli.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	reply := raw.Reply()
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}
	return PayInReply{}.fromRaw(&reply)
}

func (cli *Client) QueryPayIn(ctx context.Context, payload *GetPayInRecordPayload) (*PayInRecord, error) {
	raw := payload.toRaw(cli.conf)
	req, err := raw.GenerateSignedRequest(cli.conf.Secret, cli.conf.Key)
	if err != nil {
		return nil, fmt.Errorf("generate signed request failed: %w", err)
	}

	req = req.WithContext(ctx)
	resp, err := cli.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	reply := raw.Reply()
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}
	if err = reply.GetError(); err != nil {
		return nil, err
	}
	return reply.GetData(), nil
}
