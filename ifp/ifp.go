package ifp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	httptransport "github.com/decodeex/bifu-payment-sdk/internal/http_transport"
)

const (
	_DEV_BASE_URL  = "https://test.ddtpay.org/"
	_PROD_BASE_URL = "https://ddtpay.org/"
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
	http   *http.Client
	config *Config
}

type Config struct {
	AccessKey  string
	PrivateKey []byte

	CallbackURL string
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
		config: &conf,
	}, nil
}

func NewDevClient(conf Config) (*Client, error) {
	return NewClient(EnvDev, conf)
}

func NewProdClient(conf Config) (*Client, error) {
	return NewClient(EnvProd, conf)
}

// 买入指定金额
func (cli *Client) BuyWithAmount(ctx context.Context, req *FiatBuyRequest) (*BuyCoinReply, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	raw := req.toRaw(cli.config)
	reqBody, err := raw.GenerateSignedRequest(ctx, cli.config)
	if err != nil {
		return nil, err
	}
	resp, err := cli.http.Do(reqBody)
	if err != nil {
		return nil, fmt.Errorf("do request error: %w", err)
	}
	defer resp.Body.Close()

	res := &rawBuyResponse{}
	err = json.NewDecoder(resp.Body).Decode(res)
	if err != nil {
		return nil, fmt.Errorf("decode response error: %w", err)
	}

	return BuyCoinReply{}.fromRaw(res)
}

// 买入指定数量
func (cli *Client) BuyWithQuantity(ctx context.Context, req any) (*BuyCoinReply, error) {
	panic("not implemented")
}

func (cli *Client) QueryOrder(ctx context.Context, req *QueryOrderRequest) (*QueryOrderResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	reqBody, err := req.toRaw(cli.config).GenerateSignedRequest(ctx, cli.config)
	if err != nil {
		return nil, err
	}

	res, err := cli.http.Do(reqBody)
	if err != nil {
		return nil, fmt.Errorf("do request error: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	body := &QueryOrderResponse{}
	err = json.NewDecoder(res.Body).Decode(body)
	if err != nil {
		return nil, fmt.Errorf("decode response error: %w", err)
	}
	return body, nil
}

type baseRequest struct {
	ts string // timestamp, millisecond
}

func (base *baseRequest) GenerateSignature(accessKey string, privateKey []byte) string {
	content := fmt.Sprintf("%s_%s", accessKey, base.ts)
	mac := hmac.New(sha256.New, privateKey)
	mac.Write([]byte(content))

	sign := hex.EncodeToString(mac.Sum(nil))

	return strings.ToUpper(sign)
}

func newBaseRequest() *baseRequest {
	return &baseRequest{
		ts: strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
}

func newBaseRequestWithTimestamp(ts int64) *baseRequest {
	return &baseRequest{
		ts: strconv.FormatInt(ts, 10),
	}
}
