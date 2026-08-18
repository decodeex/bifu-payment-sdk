package platform

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/shopspring/decimal"
)

// 跨语言定价向量。
//
// testdata/pricing-vectors.json 与中台仓库
// payment-platform/packages/domain/src/pricing-vectors.json 是**同一份文件**：
// 两个仓库读同一批输入、断言同一批期望值。
//
// 为什么需要它：定价算术发生在这一侧（SDK），而中台是点差配置的权威并做事后复算。
// 两套实现（TS + decimal.js / Go + shopspring）一定会漂移，除非有东西钉住。
// 改这份文件等于改定价口径，两边的用例会同时红 —— 这是有意的。
type pricingVector struct {
	Name  string `json:"name"`
	Why   string `json:"why"`
	Input struct {
		Direction       string `json:"direction"`
		BasePrice       string `json:"basePrice"`
		FiatAmount      string `json:"fiatAmount"`
		SpreadType      string `json:"spreadType"`
		SpreadValue     string `json:"spreadValue"`
		FeeFixed        string  `json:"feeFixed"`
		FeeRatePercent  string  `json:"feeRatePercent"`
		FeeMin          *string `json:"feeMin"`
		FeeMax          *string `json:"feeMax"`
		PricePrecision  int32  `json:"pricePrecision"`
		AmountPrecision int32  `json:"amountPrecision"`
		Rounding        string `json:"rounding"`
	} `json:"input"`
	Expected struct {
		DealPrice       string `json:"dealPrice"`
		GrossCrypto     string `json:"grossCrypto"`
		Fee             string `json:"fee"`
		NetCrypto       string `json:"netCrypto"`
		SpreadRevenue   string `json:"spreadRevenue"`
		PlatformRevenue string `json:"platformRevenue"`
	} `json:"expected"`
}

// mustOptDec 处理向量里可空的上下限。老向量根本没有这两个键，
// 解出来是 nil —— 也就是「不限制」，与中台侧 undefined 归成 null 的处理一致。
func mustOptDec(t *testing.T, s *string) *decimal.Decimal {
	t.Helper()
	if s == nil || *s == "" {
		return nil
	}
	d := mustDec(t, *s)
	return &d
}

func mustDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("解析 %q: %v", s, err)
	}
	return d
}

func TestPricingVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/pricing-vectors.json")
	if err != nil {
		t.Fatalf("读取向量文件: %v", err)
	}
	var doc struct {
		Vectors []pricingVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析向量文件: %v", err)
	}
	// 空数组会让下面的循环静默通过
	if len(doc.Vectors) == 0 {
		t.Fatal("向量文件里没有向量")
	}

	for _, v := range doc.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			in := QuoteInput{
				BasePrice:       mustDec(t, v.Input.BasePrice),
				FiatAmount:      mustDec(t, v.Input.FiatAmount),
				SpreadType:      SpreadType(v.Input.SpreadType),
				SpreadValue:     mustDec(t, v.Input.SpreadValue),
				FeeFixed:        mustDec(t, v.Input.FeeFixed),
				FeeRatePercent:  mustDec(t, v.Input.FeeRatePercent),
				FeeMin:          mustOptDec(t, v.Input.FeeMin),
				FeeMax:          mustOptDec(t, v.Input.FeeMax),
				PricePrecision:  v.Input.PricePrecision,
				AmountPrecision: v.Input.AmountPrecision,
				Rounding:        RoundingMode(v.Input.Rounding),
			}
			var got *QuoteResult
			var err error
			if v.Input.Direction == "DEPOSIT" {
				got, err = QuoteDeposit(in)
			} else {
				got, err = QuoteWithdraw(in)
			}
			if err != nil {
				t.Fatalf("%s: %v\n  理由: %s", v.Name, err, v.Why)
			}

			// 用 Equal 比较而不是字符串：两边的 String() 尾零策略可能不同，
			// 但数值必须完全相等（这里不允许容差 —— 容差是中台复算校验那一层的事）
			cmp := []struct {
				field string
				got   decimal.Decimal
				want  string
			}{
				{"dealPrice", got.DealPrice, v.Expected.DealPrice},
				{"grossCrypto", got.GrossCrypto, v.Expected.GrossCrypto},
				{"fee", got.Fee, v.Expected.Fee},
				{"netCrypto", got.NetCrypto, v.Expected.NetCrypto},
				{"spreadRevenue", got.SpreadRevenue, v.Expected.SpreadRevenue},
				{"platformRevenue", got.PlatformRevenue, v.Expected.PlatformRevenue},
			}
			for _, c := range cmp {
				want := mustDec(t, c.want)
				if !c.got.Equal(want) {
					t.Errorf("%s 不一致\n  期望 %s\n  实际 %s\n  理由: %s", c.field, want, c.got, v.Why)
				}
			}
		})
	}
}

// 渠道价换算：BFT 实测返回 6.76 CNY/USDT，直接当 BasePrice 用会差 45 倍
func TestChannelPriceOrientation(t *testing.T) {
	p := ChannelPrice{
		RawInPrice:  mustDec(t, "6.7600"),
		RawOutPrice: mustDec(t, "6.6800"),
		Orientation: FiatPerSettlement,
	}
	in, err := p.BasePrice("DEPOSIT")
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Round(6).String(); got != "0.147929" {
		t.Fatalf("入金 basePrice 期望 0.147929，实际 %s", got)
	}
	// 与原样使用相差 45 倍以上 —— 写出来是为了让人一眼看到搞错的后果
	if ratio := mustDec(t, "6.76").Div(in); ratio.LessThan(decimal.NewFromInt(45)) {
		t.Fatalf("倍数关系不对：%s", ratio)
	}

	out, err := p.BasePrice("WITHDRAW")
	if err != nil {
		t.Fatal(err)
	}
	// 6.68 < 6.76，取倒数后大小关系反转
	if !out.GreaterThan(in) {
		t.Fatalf("出金 basePrice 应大于入金：in=%s out=%s", in, out)
	}
}

func TestChannelPriceSameOrientationPassesThrough(t *testing.T) {
	p := ChannelPrice{
		RawInPrice:  mustDec(t, "0.147929"),
		RawOutPrice: mustDec(t, "0.149701"),
		Orientation: SettlementPerFiat,
	}
	in, err := p.BasePrice("DEPOSIT")
	if err != nil {
		t.Fatal(err)
	}
	if in.String() != "0.147929" {
		t.Fatalf("同向渠道应原样返回，实际 %s", in)
	}
}

// 不声明方向就报错 —— 默认一个方向等于替渠道猜，猜错就是资损
func TestChannelPriceRequiresExplicitOrientation(t *testing.T) {
	p := ChannelPrice{RawInPrice: mustDec(t, "6.76"), RawOutPrice: mustDec(t, "6.68")}
	if _, err := p.BasePrice("DEPOSIT"); err == nil {
		t.Fatal("未声明方向时必须报错")
	}
}

func TestChannelPriceRejectsNonPositive(t *testing.T) {
	for _, bad := range []string{"0", "-6.76"} {
		p := ChannelPrice{
			RawInPrice:  mustDec(t, bad),
			RawOutPrice: mustDec(t, bad),
			Orientation: FiatPerSettlement,
		}
		if _, err := p.BasePrice("DEPOSIT"); err == nil {
			t.Errorf("%s 应当被拒", bad)
		}
	}
}

// 点差大到把成交价压成 0 或负数时，必须拒绝报价而不是吐一个负价出去
func TestRejectsNonPositiveDealPrice(t *testing.T) {
	in := QuoteInput{
		BasePrice:       mustDec(t, "0.147929"),
		FiatAmount:      mustDec(t, "100"),
		SpreadType:      SpreadFixed,
		SpreadValue:     mustDec(t, "0.2"), // 远大于底价
		FeeFixed:        decimal.Zero,
		FeeRatePercent:  decimal.Zero,
		PricePrecision:  6,
		AmountPrecision: 2,
		Rounding:        RoundingHalfUp,
	}
	if _, err := QuoteDeposit(in); err == nil {
		t.Fatal("点差过大时必须拒绝报价")
	}
}

func TestRejectsUnknownEnums(t *testing.T) {
	base := QuoteInput{
		BasePrice:       mustDec(t, "0.147929"),
		FiatAmount:      mustDec(t, "100"),
		SpreadValue:     mustDec(t, "30"),
		FeeFixed:        decimal.Zero,
		FeeRatePercent:  decimal.Zero,
		PricePrecision:  6,
		AmountPrecision: 2,
	}
	bad := base
	bad.SpreadType = SpreadType("WEIRD")
	bad.Rounding = RoundingHalfUp
	if _, err := QuoteDeposit(bad); err == nil {
		t.Error("未知点差类型应报错")
	}

	bad2 := base
	bad2.SpreadType = SpreadBps
	bad2.Rounding = RoundingMode("BANKERS")
	if _, err := QuoteDeposit(bad2); err == nil {
		t.Error("未知取整模式应报错")
	}
}
