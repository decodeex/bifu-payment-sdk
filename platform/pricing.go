package platform

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// 定价：在渠道实时价之上加中台配置的点差。
//
// # 为什么算术在这一侧
//
// 中台是点差配置的**权威**，但它不在每笔支付的关键路径上：
//
//	SDK 读中台点差（/internal/config/fx）→ SDK 调渠道拿实时价 → SDK 本地算成交价
//	→ 建单 → 调渠道 → 回传（带上渠道原始价与点差版本号）→ 中台事后复算校验
//
// 这样中台挂了不影响收单，也不需要中台去接渠道或行情源。
//
// 代价是定价算术有两套实现（中台 TypeScript + decimal.js，这里 Go + shopspring）。
// 两套实现一定会漂移，除非有东西钉住 —— 所以本文件的每一条规则都由
// testdata/pricing-vectors.json 钉住，那份文件与中台仓库的
// packages/domain/src/pricing-vectors.json 是**同一份**。
//
// # 单位方向：最容易造成资损的地方
//
// BasePrice 的定义是「**每 1 法币折合的结算币数量**」（USDT/CNY，量级 0.147…）。
// 渠道返回的通常是反过来的（BFT 实测 marketInPrice=6.76，即 1 USDT = 6.76 CNY），
// 两者差 45 倍。所以渠道价必须先经 ChannelPrice.BasePrice() 换算，
// 不允许把渠道返回的数字直接塞进 BasePrice。

// RoundingMode 与中台一致，只有三种。
type RoundingMode string

const (
	RoundingUp     RoundingMode = "UP"
	RoundingDown   RoundingMode = "DOWN"
	RoundingHalfUp RoundingMode = "HALF_UP"
)

// SpreadType 点差类型。BPS 的单位是万分之一，FIXED 的单位与价格相同。
type SpreadType string

const (
	SpreadBps   SpreadType = "BPS"
	SpreadFixed SpreadType = "FIXED"
)

// bpsDenominator 万分之一。单独命名而不是散落的 10000，是因为它出现在成交价公式里，
// 写错一个零就是 10 倍的价差。
var bpsDenominator = decimal.NewFromInt(10000)

// QuoteInput 与中台 money.ts 的 QuoteInput 一一对应，字段名刻意保持一致，
// 便于两边对照 —— 名字不一样的时候，人会以为它们语义也不一样。
type QuoteInput struct {
	// 每 1 法币折合的结算币数量（USDT/CNY）。渠道价必须先换算再传进来
	BasePrice decimal.Decimal
	// 下单金额，以法币计价
	FiatAmount decimal.Decimal
	SpreadType SpreadType
	// 点差数值。**支持负数**（汇率补贴），负点差方向与正点差相反
	SpreadValue decimal.Decimal
	// 手续费固定部分，以结算币计价
	FeeFixed decimal.Decimal
	// 手续费百分比部分，0.1 表示 0.1%（不是 0.001）
	FeeRatePercent decimal.Decimal
	// 成交价精度
	PricePrecision int32
	// 结算币金额精度
	AmountPrecision int32
	Rounding        RoundingMode
}

// QuoteResult 与中台 money.ts 的 QuoteResult 对应。
type QuoteResult struct {
	DealPrice   decimal.Decimal
	GrossCrypto decimal.Decimal
	Fee         decimal.Decimal
	// 入金：用户实际可得；出金：用户实际应扣
	NetCrypto       decimal.Decimal
	SpreadRevenue   decimal.Decimal
	FeeRevenue      decimal.Decimal
	PlatformRevenue decimal.Decimal
}

// PricingError 定价过程中的错误。单独一个类型，方便调用方把它与网络错误区分开：
// 网络错误可以重试，定价错误重试一万次也还是错的。
type PricingError struct{ msg string }

func (e *PricingError) Error() string { return e.msg }

func quantize(v decimal.Decimal, precision int32, mode RoundingMode) (decimal.Decimal, error) {
	switch mode {
	case RoundingUp:
		// 注意：这里要的是「向上（朝 +∞）」，不是「远离零」。
		// shopspring 的 RoundUp 是远离零，负数会往下走 —— 与中台的 ROUND_UP 不一致
		return v.RoundCeil(precision), nil
	case RoundingDown:
		return v.RoundFloor(precision), nil
	case RoundingHalfUp:
		return v.Round(precision), nil
	default:
		return decimal.Decimal{}, &PricingError{msg: fmt.Sprintf("未知的取整模式 %q", mode)}
	}
}

// dealPriceOf 应用点差后的成交价。
//
// sign = -1 表示下浮（入金），+1 表示上浮（出金）。两种点差类型只在这一个函数里分叉，
// 后面的折算 / 手续费 / 取整完全一样 —— 分叉点越少越不会漂。
func dealPriceOf(i QuoteInput, sign int64) (decimal.Decimal, error) {
	s := decimal.NewFromInt(sign)
	var raw decimal.Decimal
	switch i.SpreadType {
	case SpreadFixed:
		raw = i.BasePrice.Add(i.SpreadValue.Mul(s))
	case SpreadBps:
		raw = i.BasePrice.Mul(decimal.NewFromInt(1).Add(i.SpreadValue.Div(bpsDenominator).Mul(s)))
	default:
		return decimal.Decimal{}, &PricingError{msg: fmt.Sprintf("未知的点差类型 %q", i.SpreadType)}
	}
	dealPrice, err := quantize(raw, i.PricePrecision, i.Rounding)
	if err != nil {
		return decimal.Decimal{}, err
	}
	// 固定点差的绝对值可能大于底价，算出来是 0 或负数。那是配置错了，
	// 但报价环节绝不能吐一个负价出去 —— 下游按它折算就是资损
	if !dealPrice.IsPositive() {
		return decimal.Decimal{}, &PricingError{
			msg: fmt.Sprintf("点差过大导致成交价为 %s，拒绝报价（负价被下游折算就是资损）", dealPrice.String()),
		}
	}
	return dealPrice, nil
}

// calcFee 手续费 = 固定部分 + 毛额 × 比例。
//
// **刻意不取整**：中台的实现里，取整只发生在算净额那一步
// （`quantize(gross − fee)`），手续费本身保持未取整。
//
// 我第一版按「手续费也取整」写，跨语言向量立刻抓出来了：amountPrecision=2 时
// 期望的 fee 是 0.044426，我算出 0.04。这种差异单看代码完全看不出对错 ——
// 两边都「很合理」，只有对着同一份向量跑才知道谁是标准。
func calcFee(gross decimal.Decimal, i QuoteInput) decimal.Decimal {
	return i.FeeFixed.Add(gross.Mul(i.FeeRatePercent.Div(decimal.NewFromInt(100))))
}

// baselineGross 不加点差时的毛额，用来算点差收入。
//
// 注意**先把底价按价格精度取整**再折算 —— 与加了点差的那条路径保持同样的步骤，
// 否则点差收入里会混进一个「底价没取整」的偏差。第一版漏了里层这次取整，
// 向量在「大额」那条上抓出来了（期望 666，算出 665.99）。
func baselineGross(i QuoteInput) (decimal.Decimal, error) {
	bp, err := quantize(i.BasePrice, i.PricePrecision, i.Rounding)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return quantize(i.FiatAmount.Mul(bp), i.AmountPrecision, i.Rounding)
}

// QuoteDeposit 入金报价：点差下浮，手续费从毛额里扣。
func QuoteDeposit(i QuoteInput) (*QuoteResult, error) {
	dealPrice, err := dealPriceOf(i, -1)
	if err != nil {
		return nil, err
	}
	gross, err := quantize(i.FiatAmount.Mul(dealPrice), i.AmountPrecision, i.Rounding)
	if err != nil {
		return nil, err
	}
	fee := calcFee(gross, i)
	net, err := quantize(gross.Sub(fee), i.AmountPrecision, i.Rounding)
	if err != nil {
		return nil, err
	}
	base, err := baselineGross(i)
	if err != nil {
		return nil, err
	}
	spreadRevenue := base.Sub(gross)
	return &QuoteResult{
		DealPrice:       dealPrice,
		GrossCrypto:     gross,
		Fee:             fee,
		NetCrypto:       net,
		SpreadRevenue:   spreadRevenue,
		FeeRevenue:      fee,
		PlatformRevenue: spreadRevenue.Add(fee),
	}, nil
}

// QuoteWithdraw 出金报价：点差上浮，手续费加在毛额之上。
func QuoteWithdraw(i QuoteInput) (*QuoteResult, error) {
	dealPrice, err := dealPriceOf(i, 1)
	if err != nil {
		return nil, err
	}
	gross, err := quantize(i.FiatAmount.Mul(dealPrice), i.AmountPrecision, i.Rounding)
	if err != nil {
		return nil, err
	}
	fee := calcFee(gross, i)
	net, err := quantize(gross.Add(fee), i.AmountPrecision, i.Rounding)
	if err != nil {
		return nil, err
	}
	base, err := baselineGross(i)
	if err != nil {
		return nil, err
	}
	spreadRevenue := gross.Sub(base)
	return &QuoteResult{
		DealPrice:       dealPrice,
		GrossCrypto:     gross,
		Fee:             fee,
		NetCrypto:       net,
		SpreadRevenue:   spreadRevenue,
		FeeRevenue:      fee,
		PlatformRevenue: spreadRevenue.Add(fee),
	}, nil
}

// PriceOrientation 渠道报价的单位方向。**不给默认值** —— 猜错就是资损。
type PriceOrientation string

const (
	// 每 1 结算币折合多少法币（BFT 实测 6.76 CNY per USDT）。绝大多数渠道是这个
	FiatPerSettlement PriceOrientation = "FIAT_PER_SETTLEMENT"
	// 每 1 法币折合多少结算币（0.1479 USDT per CNY）。与 BasePrice 同向
	SettlementPerFiat PriceOrientation = "SETTLEMENT_PER_FIAT"
)

// ChannelPrice 渠道返回的原始报价。
type ChannelPrice struct {
	// 渠道原样返回的两个价，未做任何换算
	RawInPrice  decimal.Decimal
	RawOutPrice decimal.Decimal
	Orientation PriceOrientation
}

// BasePrice 把渠道口径换成 BasePrice 口径。
//
// direction 为 "DEPOSIT" 时取 in 价、"WITHDRAW" 时取 out 价 ——
// BFT 的 in/out 指**用户**的资金方向。这个映射错了不会报错，只会让每一笔都用错价。
func (p ChannelPrice) BasePrice(direction string) (decimal.Decimal, error) {
	raw := p.RawOutPrice
	if direction == "DEPOSIT" {
		raw = p.RawInPrice
	}
	if !raw.IsPositive() {
		return decimal.Decimal{}, &PricingError{
			msg: fmt.Sprintf("渠道返回的价格必须为正，实际 %s（取倒数会得到一个看起来正常的负底价）", raw.String()),
		}
	}
	switch p.Orientation {
	case SettlementPerFiat:
		return raw, nil
	case FiatPerSettlement:
		// 取倒数时给足精度：直接用默认精度会把 1/6.76 截断，成交价就跟着偏
		return decimal.NewFromInt(1).DivRound(raw, 30), nil
	default:
		return decimal.Decimal{}, &PricingError{
			msg: fmt.Sprintf("渠道未声明报价方向（%q）。不允许默认 —— 猜错就是资损", p.Orientation),
		}
	}
}
