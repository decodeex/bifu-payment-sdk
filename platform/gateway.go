package platform

import (
	"context"
	"fmt"
)

// 在线决策接口（中台 C 期）。
//
// 分工：中台出**选道决策**与**订单号**并持有状态机；点差配置从中台读，
// **成交价在本地算**；SDK 用自己的渠道 key 调渠道，调完把结果回传。
// 渠道调用**不过中台**。
//
// # 为什么没有 Quote / 锁汇接口
//
// 早期版本这里有一个 `POST /gateway/quote`：中台调渠道取价、加点差、
// 返回一个签名的锁汇令牌，建单时带回去。那个设计被推翻了（见中台设计 §4.0）——
// 中台不碰渠道 API，于是它没有独立的价格来源，报价接口就无从实现。
// 现在的分工是：**中台给点差（`/internal/config/fx`）、渠道给实时价、SDK 本地算**。
//
// 这也意味着中台无法在下单环节挡住一个算错的价：它只能事后复算比对，
// 而复算的输入正是 SDK 报上来的那些数。所以复算是**实现漂移的探测器，
// 不是安全控制** —— 它刻意不拒单。定价正确性由两侧共用的 pricing-vectors.json 保证。

// RouteRequest 选道。
//
// 字段名与建单请求保持一致（requestAmount 而不是 amount）——
// 早期两边不一致过一次，第一次对着真中台跑就 400 了。
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

// RouteRejected 是被排除的通道及原因。
//
// **中台一定会返回它**，SDK 也一定要暴露出来：运营最常问的问题就是
// 「为什么这笔没走 A 通道」。不给原因就只能去翻日志，
// 而日志里未必留了当时的限额与额度快照。
type RouteRejected struct {
	ChannelNo string `json:"channelNo"`
	Rejection string `json:"rejection"`
	Reason    string `json:"reason"`
}

type RouteReply struct {
	// 首选通道。**空字符串表示没有可用通道**（中台返回 null）——
	// 这是一个正常的业务结论，不是错误，所以中台回的是 200。
	// Enforce 模式下拿到空值必须拒单，不能拿一个空通道号往下走
	ChannelNo string `json:"chosen"`
	// 渠道 API 基准地址。调用方原本自己就知道它，这里给出来只为便于核对配置是否一致
	BaseEndpoint string           `json:"baseEndpoint"`
	Candidates   []RouteCandidate `json:"candidates"`
	Rejected     []RouteRejected  `json:"rejected"`
}

func (c *Client) Route(ctx context.Context, req RouteRequest) (*RouteReply, error) {
	var out RouteReply
	if err := c.do(ctx, "POST", "/api/gateway/route", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PricingSnapshot 是这一笔的定价快照：**算出来的结果 + 算它用的输入**，两组都要。
//
// 少了输入那一组（渠道原始价 + 单位方向 + 点差版本号），中台收到的成交价
// 就是一个无法验证的数字，而复算是唯一能发现「两套定价实现漂移了」的手段。
type PricingSnapshot struct {
	// ---- 算它用的输入 ----
	// 渠道原样返回的价，未做任何换算
	ChannelRawPrice string `json:"channelRawPrice"`
	// 上面那个数是哪个方向的。**没有默认值**，猜错就是资损
	PriceOrientation string `json:"priceOrientation"`
	// 用的是哪个点差版本，中台据此取当时的点差参数复算
	FxRuleVersionNo int `json:"fxRuleVersionNo"`
	// ---- 算出来的结果 ----
	DealPrice string `json:"dealPrice"`
	FeeAmount string `json:"feeAmount"`
	// 当时生效的点差参数，冗余上报，便于事后不依赖版本表就看懂这一笔
	SpreadType   string  `json:"spreadType"`
	SpreadValue  string  `json:"spreadValue"`
	FeeFixed     string  `json:"feeFixed"`
	FeeRate      string  `json:"feeRate"`
	FeeMin       *string `json:"feeMin"`
	FeeMax       *string `json:"feeMax"`
	RoundingMode string  `json:"roundingMode"`
	PriceScale   int     `json:"priceScale"`
	AmountScale  int     `json:"amountScale"`
}

// GrayDecision 这一笔算不算灰度。
//
// 命中判定在**本地**做：判定需要与定价用同一个版本，而定价已经在本地了。
// ReleaseID 来自 `/internal/config/fx` 的 gray.releaseId —— 中台靠它把计数
// 落到正确的发布上，填错或不填，灰度放量的验证数据就是空的。
type GrayDecision struct {
	Hit       bool    `json:"hit"`
	ReleaseID *string `json:"releaseId"`
}

// CreateOrderRequest 建单。中台生成订单号并持有状态机。
type CreateOrderRequest struct {
	MerchantOrderNo string `json:"merchantOrderNo"`
	ChannelNo       string `json:"channelNo"`
	Direction       string `json:"direction"`
	FiatCode        string `json:"fiatCode"`
	SettlementCode  string `json:"settlementCode"`
	RequestAmount   string `json:"requestAmount"`
	// RequestAmount 是**哪一侧**的金额：FIAT | SETTLEMENT。**必填。**
	//
	// bifufx-api 的出金传的是账户币种（结算币）金额，入金传的是法币金额。
	// 中台不给这一项默认值：默认对一半调用方是错的，而错的那一半是出金 ——
	// 差一个汇率的量级，且请求内部自洽，复算也发现不了。
	AmountSide string          `json:"amountSide"`
	Pricing    PricingSnapshot `json:"pricing"`
	Gray       *GrayDecision   `json:"gray,omitempty"`
}

// PriceCheck 是中台的复算结论。nil 表示**没有复算**（不是「复算通过」）。
type PriceCheck struct {
	OK                bool    `json:"ok"`
	ExpectedDealPrice string  `json:"expectedDealPrice"`
	Reason            *string `json:"reason"`
}

type CreateOrderReply struct {
	OrderNo string `json:"orderNo"`
	TxnNo   string `json:"txnNo"`
	Status  string `json:"status"`
	// 是否命中了已有订单（幂等重放）。true 时不要重复发渠道请求
	IdempotentReplay bool `json:"idempotentReplay"`
	// 复算结论。**不影响建单成败**，但 OK=false 说明两侧定价实现已经漂了，必须告警
	PriceCheck *PriceCheck `json:"priceCheck"`
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
