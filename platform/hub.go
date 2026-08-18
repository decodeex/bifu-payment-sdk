package platform

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Hub 是给调用方用的门面。它包住调用方**原有的那一次渠道调用**，
// 在前后插入中台交互；不要求调用方实现任何接口。
type Hub struct {
	cli *Client
}

func NewHub(cfg Config) (*Hub, error) {
	cli, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Hub{cli: cli}, nil
}

// NewHubWithClient 让调用方复用已有的 Client（比如同一个进程里还要单独拉快照）。
func NewHubWithClient(cli *Client) *Hub { return &Hub{cli: cli} }

func (h *Hub) Mode() Mode { return h.cli.cfg.Mode }

// DepositIntent 是调用方要做的这一笔入金。
type DepositIntent struct {
	// 调用方自己的订单号（bifufx-api 里的 ticket）
	MerchantOrderNo string
	FiatCode        string
	SettlementCode  string
	// 金额用 string 传，避免在边界上被 float 削掉精度
	RequestAmount string
	// 调用方自己已经选好的通道。
	// Shadow 模式下**始终用它执行**，中台的选择只用于对比
	PreferredChannelNo string
	// Price 按通道取**渠道实时价**。必填。
	//
	// 为什么是回调而不是一个价格字段：Enforce 模式下最终用哪条通道由中台选道决定，
	// 取价必须发生在选道**之后**。而中台不碰渠道 API（它没有渠道凭据），
	// 所以这一步只能由调用方做 —— 它本来就在调这个渠道。
	Price PriceFunc
}

// PriceFunc 取某条通道的渠道实时价。
//
// 返回渠道**原样**的价与它的单位方向，不要在这里做任何换算 ——
// 换算与点差都在 SDK 里按共用向量的口径做，多一处换算就多一处漂移点。
type PriceFunc func(ctx context.Context, channelNo string) (ChannelPrice, error)

// Decision 是执行前的决策。Shadow 模式下 ChannelNo 一定等于
// intent.PreferredChannelNo；Enforce 模式下才可能是中台选的另一条。
type Decision struct {
	ChannelNo string
	// 中台给的订单号。Shadow 模式下建单失败时可能为空
	OrderNo string
	// 本地算出的成交价，Shadow 模式下仅供参考
	DealPrice string
	// 这一笔是否在降级（拿不到中台决策，用了本地兜底）状态下执行
	Degraded bool
	// 本次定价的完整快照（算出来的结果 + 算它用的输入）。定价失败时为 nil
	Pricing *PricingSnapshot
	// 本地算出的完整报价：毛额 / 手续费 / 净额 / 点差收益。
	// 调用方拿它去调渠道 —— 渠道要的是净额还是毛额，因渠道而异
	Quote      *QuoteResult
	// 中台的复算结论，建单时顺带返回。nil = 没复算
	PriceCheck *PriceCheck
	Route      *RouteReply
}

// ExecResult 是调用方执行完渠道调用之后要回填的东西。
type ExecResult struct {
	// 渠道流水号。渠道没给就留空（比如下单直接失败）
	ChannelOrderNo string
	// 渠道原始状态码。归一后的状态丢掉了渠道到底说了什么
	RawChannelStatus string
	// 给用户的支付跳转地址 / 表单，SDK 不解释它，原样带回给调用方
	RedirectURL string
	FormHTML    string

	Failed        bool
	FailureReason string
	// 失败之后还要不要换通道重试。**要重试就必须置 true**，
	// 否则中台会把订单落成终态，而终态不可逆 —— 这笔订单就永久死了
	WillRetry bool

	PaidAmount    string
	ChannelPaidAt *time.Time
}

// ExecuteFunc 就是调用方原来那一次渠道调用。
type ExecuteFunc func(ctx context.Context, d Decision) (ExecResult, error)

// Outcome 是 Hub.Deposit 的返回。
type Outcome struct {
	Decision Decision
	Exec     ExecResult
	// 中台交互过程中发生但被吞掉的错误（只可能在 Shadow / Off 模式下非空）。
	// 刻意返回给调用方而不是只打日志：调用方可以决定要不要告警
	SuppressedErrors []error
}

// ErrHubUnavailable 是 Enforce 模式下拒单的原因之一。
var ErrHubUnavailable = errors.New("payment hub unavailable")

// Deposit 走一遍完整链路。
//
// 三档模式的差别集中在这一个函数里，刻意不拆成三份实现 —— 拆开之后
// 「Shadow 与 Enforce 到底哪里不一样」就得靠读两段代码去对，很容易漂移。
func (h *Hub) Deposit(ctx context.Context, in DepositIntent, exec ExecuteFunc) (*Outcome, error) {
	if exec == nil {
		return nil, errors.New("payment hub: exec 不能为 nil")
	}
	out := &Outcome{}

	// ModeOff：完全不碰中台。等价于没接入这一层
	if h.cli.cfg.Mode == ModeOff {
		d := Decision{ChannelNo: in.PreferredChannelNo}
		r, err := exec(ctx, d)
		out.Decision, out.Exec = d, r
		return out, err
	}

	enforce := h.cli.cfg.Mode == ModeEnforce
	d := Decision{ChannelNo: in.PreferredChannelNo}

	// 1. 选道。Shadow 模式只为了对比，Enforce 模式才真的用
	route, err := h.cli.Route(ctx, RouteRequest{
		FiatCode:       in.FiatCode,
		SettlementCode: in.SettlementCode,
		Direction:      "DEPOSIT",
		RequestAmount:  in.RequestAmount,
	})
	if err != nil {
		h.cli.observeError(ctx, "route", err)
		out.SuppressedErrors = append(out.SuppressedErrors, err)
		// 选道是可降级的：拿不到就用调用方自己的选择，但要打降级标记
		d.Degraded = true
	} else {
		d.Route = route
		if enforce {
			d.ChannelNo = route.ChannelNo
		} else if route.ChannelNo != in.PreferredChannelNo && h.cli.cfg.Observer != nil {
			h.cli.cfg.Observer.OnDivergence(ctx, Divergence{
				MerchantOrderNo: in.MerchantOrderNo,
				CallerChannelNo: in.PreferredChannelNo,
				HubChannelNo:    route.ChannelNo,
			})
		}
	}

	// 2. 定价：中台给点差、渠道给实时价、**本地算成交价**。
	//
	// **Enforce 模式下不可降级** —— 猜点差等于亏钱。拿不到点差配置时绝不用 0 点差顶上：
	// 那等于把平台该收的那部分白送出去，而且一笔都不会报错。
	pricing, quote, gray, err := h.priceDeposit(ctx, in, d.ChannelNo)
	if err != nil {
		h.cli.observeError(ctx, "pricing", err)
		if enforce {
			return nil, errors.Join(ErrHubUnavailable, err)
		}
		out.SuppressedErrors = append(out.SuppressedErrors, err)
		d.Degraded = true
	} else {
		d.Pricing, d.Quote = pricing, quote
		d.DealPrice = pricing.DealPrice
	}

	// 3. 建单。**Enforce 模式下不可降级** —— 没有订单号这笔无法对账
	if d.Pricing != nil {
		order, err := h.cli.CreateOrder(ctx, CreateOrderRequest{
			MerchantOrderNo: in.MerchantOrderNo,
			ChannelNo:       d.ChannelNo,
			Direction:       "DEPOSIT",
			FiatCode:        in.FiatCode,
			SettlementCode:  in.SettlementCode,
			RequestAmount:   in.RequestAmount,
			// 入金金额一律法币侧：用户说的是「充多少钱」
			AmountSide: "FIAT",
			Pricing:    *d.Pricing,
			Gray:       gray,
		})
		if err != nil {
			h.cli.observeError(ctx, "orders", err)
			if enforce {
				return nil, errors.Join(ErrHubUnavailable, err)
			}
			out.SuppressedErrors = append(out.SuppressedErrors, err)
		} else {
			d.OrderNo = order.OrderNo
			d.PriceCheck = order.PriceCheck
			// 复算不一致不阻断这一笔（中台侧也不拒单），但必须让人看见：
			// 它的含义是两套定价实现已经漂了，而这种问题不会自己好，且每一笔都在错。
			if order.PriceCheck != nil && !order.PriceCheck.OK {
				reason := ""
				if order.PriceCheck.Reason != nil {
					reason = *order.PriceCheck.Reason
				}
				h.cli.observeError(ctx, "priceCheck", errors.New("中台复算不一致："+reason))
			}
		}
	} else if enforce {
		// 走不到这里（上面已经 return），留着是为了让「Enforce 必须有定价」这条不可能被后续改动绕过
		return nil, ErrHubUnavailable
	}

	// 4. 调用方去调渠道
	res, execErr := exec(ctx, d)
	out.Decision, out.Exec = d, res

	// 5. 回传。没有订单号就没法回传（Shadow 模式下建单可能失败），跳过但记下来
	if d.OrderNo == "" {
		if h.cli.cfg.Mode == ModeShadow {
			out.SuppressedErrors = append(out.SuppressedErrors,
				errors.New("没有中台订单号，本次结果未回传"))
		}
		return out, execErr
	}
	if err := h.reportOnce(ctx, d, in, res); err != nil {
		h.cli.observeError(ctx, "result", err)
		// 回传失败不影响支付本身：用户已经拿到支付链接了。
		// 但它必须能被看到 —— 静默失败会让中台缺数据而没人知道
		out.SuppressedErrors = append(out.SuppressedErrors, err)
	}
	return out, execErr
}

// priceDeposit 本地算这一笔入金的价。
//
// 三个来源：点差配置来自中台（`/internal/config/fx`）、实时价来自渠道（调用方给的回调）、
// 算术来自本包的 pricing.go —— 与中台 money.ts 由 pricing-vectors.json 钉在一起。
func (h *Hub) priceDeposit(
	ctx context.Context,
	in DepositIntent,
	channelNo string,
) (*PricingSnapshot, *QuoteResult, *GrayDecision, error) {
	if in.Price == nil {
		return nil, nil, nil, errors.New("payment hub: DepositIntent.Price 不能为 nil —— 没有渠道实时价就算不出成交价")
	}
	fx, err := h.cli.FxConfig(ctx, channelNo, in.FiatCode, in.SettlementCode, "DEPOSIT")
	if err != nil {
		return nil, nil, nil, err
	}
	// 灰度命中就用灰度那一版的点差，否则用全量版
	ver, gray := pickFxVersion(fx, in.MerchantOrderNo)
	if ver == nil {
		return nil, nil, nil, &PricingError{msg: "该维度没有生效的点差配置：" +
			channelNo + " " + in.FiatCode + "/" + in.SettlementCode + " DEPOSIT"}
	}

	price, err := in.Price(ctx, channelNo)
	if err != nil {
		return nil, nil, nil, err
	}
	basePrice, err := price.BasePrice("DEPOSIT")
	if err != nil {
		return nil, nil, nil, err
	}

	qi, err := quoteInputFrom(*ver, basePrice, in.RequestAmount)
	if err != nil {
		return nil, nil, nil, err
	}
	q, err := QuoteDeposit(*qi)
	if err != nil {
		return nil, nil, nil, err
	}

	snap := &PricingSnapshot{
		ChannelRawPrice:  price.RawInPrice.String(),
		PriceOrientation: string(price.Orientation),
		FxRuleVersionNo:  ver.VersionNo,
		DealPrice:        q.DealPrice.String(),
		FeeAmount:        q.Fee.String(),
		SpreadType:       ver.SpreadType,
		SpreadValue:      ver.SpreadValue,
		FeeFixed:         ver.FeeFixed,
		FeeRate:          ver.FeeRate,
		FeeMin:           ver.FeeMin,
		FeeMax:           ver.FeeMax,
		RoundingMode:     ver.RoundingMode,
		PriceScale:       ver.PriceScale,
		AmountScale:      ver.AmountScale,
	}
	return snap, q, gray, nil
}

// pickFxVersion 决定这一笔用哪一版点差，以及要不要报成灰度命中。
//
// 判定放在本地：它必须与**定价用的那一版**是同一个决定。放到中台去判，
// 中台就得在建单时告诉 SDK「你刚才应该用灰度版算」—— 但价已经算完了。
func pickFxVersion(fx *FxConfig, merchantOrderNo string) (*FxVersion, *GrayDecision) {
	if fx.Gray != nil && grayHit(fx.Gray, merchantOrderNo) {
		id := fx.Gray.ReleaseID
		v := fx.Gray.FxVersion
		return &v, &GrayDecision{Hit: true, ReleaseID: &id}
	}
	if fx.Full == nil {
		return nil, nil
	}
	return fx.Full, nil
}

// grayHit 按灰度范围判命中。
//
// FIRST_N_ORDERS：前 N 笔，用中台给的已放量数判 —— 有并发误差，
// 但灰度阈值本来就不需要精确，为此多一次中台往返不值得。
// PERCENT_TRAFFIC：按商户订单号哈希取模，同一笔单反复重试落在同一侧（幂等要它稳定）。
func grayHit(g *GrayFx, merchantOrderNo string) bool {
	if g.Scope == nil || g.Threshold == nil {
		return false
	}
	switch *g.Scope {
	case "FIRST_N_ORDERS":
		return g.ReleasedCount < *g.Threshold
	case "PERCENT_TRAFFIC":
		return int(fnv32(merchantOrderNo)%100) < *g.Threshold
	default:
		return false
	}
}

// fnv32 是 FNV-1a。刻意用一个**写死在这里**的哈希而不是 maphash：
// 后者每个进程的种子不同，同一笔单在两个进程里会落到灰度的两侧。
func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func quoteInputFrom(v FxVersion, basePrice decimal.Decimal, requestAmount string) (*QuoteInput, error) {
	amount, err := decimal.NewFromString(requestAmount)
	if err != nil {
		return nil, &PricingError{msg: "下单金额不是合法数字：" + requestAmount}
	}
	spread, err := decimal.NewFromString(v.SpreadValue)
	if err != nil {
		return nil, &PricingError{msg: "点差不是合法数字：" + v.SpreadValue}
	}
	feeFixed, err := decimal.NewFromString(v.FeeFixed)
	if err != nil {
		return nil, &PricingError{msg: "固定手续费不是合法数字：" + v.FeeFixed}
	}
	feeRate, err := decimal.NewFromString(v.FeeRate)
	if err != nil {
		return nil, &PricingError{msg: "手续费率不是合法数字：" + v.FeeRate}
	}
	i := &QuoteInput{
		BasePrice:       basePrice,
		FiatAmount:      amount,
		SpreadType:      SpreadType(v.SpreadType),
		SpreadValue:     spread,
		FeeFixed:        feeFixed,
		FeeRatePercent:  feeRate,
		PricePrecision:  int32(v.PriceScale),
		AmountPrecision: int32(v.AmountScale),
		Rounding:        RoundingMode(v.RoundingMode),
	}
	if i.FeeMin, err = optDecimal(v.FeeMin); err != nil {
		return nil, err
	}
	if i.FeeMax, err = optDecimal(v.FeeMax); err != nil {
		return nil, err
	}
	return i, nil
}

// optDecimal 把可空的字符串档位转成可空的 decimal。
//
// nil 与空字符串都当「不限制」：中台留空的语义就是不限制，
// 而 JSON 里两种形态都可能出现（没配过是 null，清空过可能是空串）。
func optDecimal(s *string) (*decimal.Decimal, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return nil, &PricingError{msg: "手续费上下限不是合法数字：" + *s}
	}
	return &d, nil
}

func (h *Hub) reportOnce(ctx context.Context, d Decision, in DepositIntent, res ExecResult) error {
	status := "PROCESSING"
	if res.Failed {
		status = "FAILED"
	}
	req := ReportResultRequest{
		MerchantOrderNo: in.MerchantOrderNo,
		Status:          status,
		Degraded:        d.Degraded,
		WillRetry:       res.WillRetry,
	}
	if res.ChannelOrderNo != "" {
		req.ChannelOrderNo = &res.ChannelOrderNo
	}
	if res.RawChannelStatus != "" {
		req.RawChannelStatus = &res.RawChannelStatus
	}
	if res.PaidAmount != "" {
		req.PaidAmount = &res.PaidAmount
	}
	if res.FailureReason != "" {
		req.FailureReason = &res.FailureReason
	}
	if res.ChannelPaidAt != nil {
		// RFC3339 带时区。对账按渠道时间，不按上报时间
		s := res.ChannelPaidAt.Format(time.RFC3339)
		req.ChannelPaidAt = &s
	}
	_, err := h.cli.ReportResult(ctx, d.OrderNo, req)
	return err
}
