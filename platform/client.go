package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Mode 决定这一层对现有支付流程的影响程度。见 doc.go。
type Mode string

const (
	// ModeOff 什么都不做，等价于没接入。留作线上开关
	ModeOff Mode = "off"
	// ModeShadow 照常调中台，但始终按调用方自己的决策执行；中台的错误不影响支付
	ModeShadow Mode = "shadow"
	// ModeEnforce 按中台的决策执行；定价拿不到或建单失败一律拒单
	ModeEnforce Mode = "enforce"
)

func (m Mode) valid() bool {
	return m == ModeOff || m == ModeShadow || m == ModeEnforce
}

// Config 是接入中台需要的全部配置。
type Config struct {
	// 中台地址，如 https://pay.internal.example.com
	BaseURL string
	// 商户在中台的 API Key（明文标识，不是密钥）
	APIKey string
	// 商户在中台的 API Secret，用于请求签名
	APISecret string
	// 默认 shadow —— 接入的第一步不应该改变任何现有行为
	Mode Mode
	// 单次请求超时。默认 5s
	Timeout time.Duration
	// 可选：自定义 HTTP client（测试注入 httptest、或走内网代理）
	HTTPClient *http.Client
	// 可选：观测钩子。Shadow 模式下的差异、上报失败都从这里出去
	Observer Observer
	// 可选：时钟，测试用。默认 time.Now
	Now func() time.Time
	// 可选：跳过 TLS 校验。**只允许在自签证书的测试环境用**
	InsecureSkipVerify bool
}

// Observer 让接入方把这一层发生的事接到自己的日志 / 监控上。
//
// 刻意不在 SDK 里直接打日志：调用方各有自己的日志库与字段规范，
// SDK 自己打会同时污染两边。
type Observer interface {
	// OnDivergence 只在 Shadow 模式触发：中台的决策与调用方自己的决策不一致。
	// 这是 Shadow 模式存在的全部理由，务必接上
	OnDivergence(ctx context.Context, d Divergence)
	// OnError 这一层内部的失败。Shadow 模式下这些都不会影响支付，
	// 但必须能被看到，否则「中台没数据」会被当成中台的问题
	OnError(ctx context.Context, stage string, err error)
}

// Divergence 记录一次决策差异。
type Divergence struct {
	MerchantOrderNo string
	// 调用方自己选的通道
	CallerChannelNo string
	// 中台会选的通道
	HubChannelNo string
	// 中台给出的成交价（Shadow 模式下没有被使用）
	HubDealPrice string
}

// Client 是中台的低层客户端：负责签名、超时、错误归一。
// 业务语义在 snapshot.go / gateway.go 里。
type Client struct {
	cfg  Config
	http *http.Client
	base *url.URL
}

// APIError 是中台返回的非 2xx。保留状态码与响应体 —— 少了任何一个都很难排查。
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("payment hub %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// ErrNotConfigured 表示 Config 不完整。刻意在 NewClient 就报错而不是等到第一次请求：
// 配置错误应该在启动时暴露，不该等到某个用户下单时才炸。
var ErrNotConfigured = errors.New("payment hub: incomplete config")

func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("%w: BaseURL / APIKey / APISecret 都不能为空", ErrNotConfigured)
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeShadow
	}
	if !cfg.Mode.valid() {
		return nil, fmt.Errorf("%w: 未知的 Mode %q", ErrNotConfigured, cfg.Mode)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("%w: BaseURL 解析失败: %v", ErrNotConfigured, err)
	}

	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
		if cfg.InsecureSkipVerify {
			hc.Transport = insecureTransport()
		}
	}
	return &Client{cfg: cfg, http: hc, base: base}, nil
}

// Mode 暴露出来，方便调用方在自己的健康检查里确认线上跑的是哪一档。
func (c *Client) Mode() Mode { return c.cfg.Mode }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		// 签名与发送用**同一份** buffer。分别 marshal 两次会因为键顺序不同而对不上
		body = b
	}

	u, err := c.base.Parse(path)
	if err != nil {
		return fmt.Errorf("build url: %w", err)
	}
	ts := c.cfg.Now().UnixMilli()
	sig := signRequest(c.cfg.APISecret, signatureParts{
		method:    method,
		path:      requestURI(u),
		timestamp: ts,
		body:      body,
	})

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("x-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("x-signature", sig)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call payment hub: %w", err)
	}
	defer resp.Body.Close()

	// 全量读出来：非 2xx 时响应体就是排查线索，截断了等于自断手脚
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       requestURI(u),
			Body:       string(raw),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w (body=%s)", err, string(raw))
	}
	return nil
}

func (c *Client) observeError(ctx context.Context, stage string, err error) {
	if c.cfg.Observer != nil && err != nil {
		c.cfg.Observer.OnError(ctx, stage, err)
	}
}
