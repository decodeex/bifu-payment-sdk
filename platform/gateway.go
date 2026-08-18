package platform

import (
	"context"
	"fmt"
)

// 在线决策接口（中台 C 期，正在开发中）。
//
// 这四个接口是「在线决策 + 结果回传」这个架构的落点：中台出决策与订单号，
// SDK 用自己的渠道 key 去调渠道，调完把结果回传。渠道调用**不过中台**。
//
// ⚠️ C 期尚未上线。本包已按契约实现，可以对着假中台（platform 的测试里就有一个）
// 完整跑通；对着真中台要等 C3–C5 部署。

// QuoteRequest 报价 + 锁汇。
type QuoteRequest struct {
	ChannelNo      string `json:"channelNo,omitempty"` // 留空 = 让中台选道
	FiatCode       string `json:"fiatCode"`
	SettlementCode string `json:"settlementCode"`
	Direction      string `json:"direction"`
	RequestAmount  string `json:"requestAmount"`
}

// QuoteReply 里的 QuoteToken 是一个签名的自包含令牌，建单时原样带回去。
//
// 中台刻意不建 quote 表：报价绝大多数不会成单，建表等于写一堆几分钟后没人看的行。
// 防篡改靠签名、防过期靠令牌里的有效期、防重复成交靠中台侧 quote_id 的唯一索引。
type QuoteReply struct {
	QuoteToken      string `json:"quoteToken"`
	ChannelNo       string `json:"channelNo"`
	DealPrice       string `json:"dealPrice"`
	BasePrice       string `json:"basePrice"`
	FeeAmount       string `json:"feeAmount"`
	FxVersionNo     int    `json:"fxVersionNo"`
	Gray            bool   `json:"gray"`
	ExpiresAtUnixMs int64  `json:"expiresAtUnixMs"`
}

func (c *Client) Quote(ctx context.Context, req QuoteRequest) (*QuoteReply, error) {
	var out QuoteReply
	if err := c.do(ctx, "POST", "/api/gateway/quote", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RouteRequest 选道。
type RouteRequest struct {
	FiatCode       string `json:"fiatCode"`
	SettlementCode string `json:"settlementCode"`
	Direction      string `json:"direction"`
	RequestAmount  string `json:"requestAmount"`
}

// RouteCandidate 是候选通道。
//
// 候选列表必须给出来：不给的话 SDK 在中台不可用时只能瞎猜，
// 运营看到一个没有备选的决策也无法判断路由配得对不对。
type RouteCandidate struct {
	ChannelNo string  `json:"channelNo"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
}

type RouteReply struct {
	ChannelNo    string           `json:"channelNo"`
	BaseEndpoint string           `json:"baseEndpoint"`
	CallbackURL  string           `json:"callbackUrl"`
	Candidates   []RouteCandidate `json:"candidates"`
}

func (c *Client) Route(ctx context.Context, req RouteRequest) (*RouteReply, error) {
	var out RouteReply
	if err := c.do(ctx, "POST", "/api/gateway/route", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateOrderRequest 建单。中台生成订单号并持有状态机。
type CreateOrderRequest struct {
	MerchantOrderNo string `json:"merchantOrderNo"`
	QuoteToken      string `json:"quoteToken"`
	ChannelNo       string `json:"channelNo"`
	RequestAmount   string `json:"requestAmount"`
	Direction       string `json:"direction"`
}

type CreateOrderReply struct {
	OrderNo string `json:"orderNo"`
	TxnNo   string `json:"txnNo"`
	Status  string `json:"status"`
}

func (c *Client) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderReply, error) {
	var out CreateOrderReply
	if err := c.do(ctx, "POST", "/api/gateway/orders", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReportResultRequest 回传执行结果。
//
// 三个号一个都不能少：中台订单号（路径里）、商户订单号、渠道流水号。
// 少一个，对账时就会出现「两边都有这笔，但对不上是不是同一笔」。
type ReportResultRequest struct {
	MerchantOrderNo  string  `json:"merchantOrderNo"`
	ChannelOrderNo   *string `json:"channelOrderNo"`
	Status           string  `json:"status"`
	RawChannelStatus *string `json:"rawChannelStatus"`
	PaidAmount       *string `json:"paidAmount"`
	// 渠道侧的完成时间，RFC3339 **带时区**。不带时区的时间在对账时会差几个小时
	ChannelPaidAt *string `json:"channelPaidAt"`
	Degraded      bool    `json:"degraded"`
	// 这次失败之后还要不要换通道重试。
	// 中台靠它决定订单是留在「处理中」还是落终态 —— 终态不可逆，
	// 要重试却先落成失败，这笔订单就永久死了
	WillRetry     bool    `json:"willRetry"`
	FailureReason *string `json:"failureReason"`
}

type ReportResultReply struct {
	OrderNo string `json:"orderNo"`
	Status  string `json:"status"`
}

func (c *Client) ReportResult(ctx context.Context, orderNo string, req ReportResultRequest) (*ReportResultReply, error) {
	if orderNo == "" {
		return nil, fmt.Errorf("payment hub: orderNo 不能为空")
	}
	var out ReportResultReply
	path := fmt.Sprintf("/api/gateway/orders/%s/result", orderNo)
	if err := c.do(ctx, "POST", path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
