package platform

import (
	"context"
	"errors"
	"time"
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
}

// Decision 是执行前的决策。Shadow 模式下 ChannelNo 一定等于
// intent.PreferredChannelNo；Enforce 模式下才可能是中台选的另一条。
type Decision struct {
	ChannelNo string
	// 中台给的订单号。Shadow 模式下建单失败时可能为空
	OrderNo string
	// 中台给的成交价，Shadow 模式下仅供参考
	DealPrice string
	// 这一笔是否在降级（拿不到中台决策，用了本地兜底）状态下执行
	Degraded bool
	// 中台不可用时为 nil
	Quote *QuoteReply
	Route *RouteReply
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

	// 2. 报价 + 锁汇。**Enforce 模式下不可降级** —— 猜价格等于亏钱
	quote, err := h.cli.Quote(ctx, QuoteRequest{
		ChannelNo:      d.ChannelNo,
		FiatCode:       in.FiatCode,
		SettlementCode: in.SettlementCode,
		Direction:      "DEPOSIT",
		RequestAmount:  in.RequestAmount,
	})
	if err != nil {
		h.cli.observeError(ctx, "quote", err)
		if enforce {
			return nil, errors.Join(ErrHubUnavailable, err)
		}
		out.SuppressedErrors = append(out.SuppressedErrors, err)
		d.Degraded = true
	} else {
		d.Quote = quote
		d.DealPrice = quote.DealPrice
	}

	// 3. 建单。**Enforce 模式下不可降级** —— 没有订单号这笔无法对账
	if d.Quote != nil {
		order, err := h.cli.CreateOrder(ctx, CreateOrderRequest{
			MerchantOrderNo: in.MerchantOrderNo,
			QuoteToken:      d.Quote.QuoteToken,
			ChannelNo:       d.ChannelNo,
			RequestAmount:   in.RequestAmount,
			Direction:       "DEPOSIT",
		})
		if err != nil {
			h.cli.observeError(ctx, "orders", err)
			if enforce {
				return nil, errors.Join(ErrHubUnavailable, err)
			}
			out.SuppressedErrors = append(out.SuppressedErrors, err)
		} else {
			d.OrderNo = order.OrderNo
		}
	} else if enforce {
		// 走不到这里（上面已经 return），留着是为了让「Enforce 必须有报价」这条不可能被后续改动绕过
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
