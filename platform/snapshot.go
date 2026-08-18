package platform

import (
	"context"
	"net/url"
)

// 对内配置接口（中台 B7，已上线）。
//
// 定位是**静态配置下发**：不常变的东西（渠道 endpoint、币种对、限额、生效点差）。
// 在线决策（报价 / 选道）走 gateway.go 里那几个接口。

// Currency 是中台下发的币种。
type Currency struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      string `json:"type"` // FIAT | CRYPTO
	Precision int    `json:"precision"`
}

// CurrencyPair 是某条通道在某个方向上支持的币种组合。
type CurrencyPair struct {
	Direction      string `json:"direction"` // DEPOSIT | WITHDRAW
	FiatCode       string `json:"fiatCode"`
	SettlementCode string `json:"settlementCode"`
}

// Channel 是中台下发的通道。金额一律 string —— 中台侧是 NUMERIC(38,18)，
// 用 float64 接会静默丢精度。需要计算时再转 decimal。
type Channel struct {
	ChannelNo         string         `json:"channelNo"`
	Name              string         `json:"name"`
	Type              string         `json:"type"`
	SettlementCycle   string         `json:"settlementCycle"`
	CutoffTime        string         `json:"cutoffTime"`
	SupportsDeposit   bool           `json:"supportsDeposit"`
	SupportsWithdraw  bool           `json:"supportsWithdraw"`
	BaseEndpoint      *string        `json:"baseEndpoint"`
	CurrencyPairs     []CurrencyPair `json:"currencyPairs"`
	DepositMin        *string        `json:"depositMin"`
	DepositMax        *string        `json:"depositMax"`
	WithdrawMin       *string        `json:"withdrawMin"`
	WithdrawMax       *string        `json:"withdrawMax"`
	DailyLimit        *string        `json:"dailyLimit"`
	LimitCurrencyCode *string        `json:"limitCurrencyCode"`
}

// FxVersion 是一份生效中的点差数值。
type FxVersion struct {
	VersionNo   int    `json:"versionNo"`
	SpreadType  string `json:"spreadType"` // BPS | FIXED
	SpreadValue string `json:"spreadValue"`
	FeeFixed    string `json:"feeFixed"`
	FeeRate     string `json:"feeRate"`
	// 单笔手续费下限 / 上限，nil = 不限制。建单快照里必须原样带上，否则中台复算会误报
	FeeMin       *string `json:"feeMin"`
	FeeMax       *string `json:"feeMax"`
	RoundingMode string  `json:"roundingMode"`
	PriceScale   int     `json:"priceScale"`
	AmountScale  int     `json:"amountScale"`
}

// FxRule 是某个维度上生效的点差。MerchantNo 为空表示这是业务线的兜底规则。
type FxRule struct {
	FxVersion
	MerchantNo     *string `json:"merchantNo"`
	ChannelNo      string  `json:"channelNo"`
	FiatCode       string  `json:"fiatCode"`
	SettlementCode string  `json:"settlementCode"`
	Direction      string  `json:"direction"`
}

// Snapshot 是整份配置快照。
//
// 注意这里**没有任何密钥字段**：渠道侧的 App Secret 不从中台下发（中台侧有一条
// 自动化断言专门守着这件事）。渠道凭据仍然由调用方自己持有 —— 也就是说接入这一层
// 不需要把任何密钥交出去。
type Snapshot struct {
	MerchantNo       string     `json:"merchantNo"`
	BusinessLineCode string     `json:"businessLineCode"`
	GeneratedAt      string     `json:"generatedAt"`
	Currencies       []Currency `json:"currencies"`
	Channels         []Channel  `json:"channels"`
	FxRules          []FxRule   `json:"fxRules"`
}

// Snapshot 拉取整份配置。
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var out Snapshot
	if err := c.do(ctx, "GET", "/api/internal/config/snapshot", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FxConfig 是单维度点差查询的结果。
type FxConfig struct {
	// 命中的是商户级还是业务线兜底级 —— 排查「为什么价格不是我配的那个」时最有用
	MatchedLevel   string `json:"matchedLevel"` // MERCHANT | BUSINESS_LINE
	ChannelNo      string `json:"channelNo"`
	FiatCode       string `json:"fiatCode"`
	SettlementCode string `json:"settlementCode"`
	Direction      string `json:"direction"`
	// 为空表示该维度还没有生效配置 —— 这时应当拒绝报价，不要拿 0 点差顶上
	Full *FxVersion `json:"full"`
	Gray *GrayFx    `json:"gray"`
}

// GrayFx 是正在放量的灰度版本。
//
// 「这笔流量算不算灰度」由中台在报价时判定，不在这里做 —— 那需要订单序号 / 流量哈希。
// 这里的字段只用于观测与排查。
type GrayFx struct {
	FxVersion
	// 灰度发布 id。建单时原样填进 gray.releaseId，中台据此把计数落到正确的发布上。
	// 没有它就没法归属 —— 判出命中也白判
	ReleaseID     string  `json:"releaseId"`
	Scope         *string `json:"scope"`
	Threshold     *int    `json:"threshold"`
	ReleasedCount int     `json:"releasedCount"`
}

// FxConfig 查单个维度的生效点差。
func (c *Client) FxConfig(ctx context.Context, channelNo, fiatCode, settlementCode, direction string) (*FxConfig, error) {
	q := url.Values{}
	q.Set("channelNo", channelNo)
	q.Set("fiatCode", fiatCode)
	q.Set("settlementCode", settlementCode)
	q.Set("direction", direction)

	var out FxConfig
	// query 必须参与签名，所以路径带上它一起传进 do()
	if err := c.do(ctx, "GET", "/api/internal/config/fx?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
