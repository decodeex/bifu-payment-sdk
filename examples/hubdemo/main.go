// hubdemo 是「接入支付中台」的最小可跑示例。
//
// 它做三件事：
//
//  1. 用商户凭据拉一次配置快照（中台 B7，**已上线**）
//  2. 查一个维度的生效点差（同上）
//  3. 走一遍完整新链路：选道 → 报价 → 建单 → 调渠道（这里用假渠道）→ 回传
//     （中台 C 期接口，**还在开发**。真中台上会 404，这时 demo 会自动切到
//     内置的假中台，让整条链路仍然可以完整跑通并观察）
//
// 跑法：
//
//	export PAY_HUB_URL=https://<中台地址>
//	export PAY_HUB_API_KEY=ak_xxx
//	export PAY_HUB_API_SECRET=sk_xxx
//	export PAY_HUB_INSECURE=1        # 自签证书的测试环境才需要
//	go run ./examples/hubdemo
//
// 不带任何环境变量直接跑，会全程用内置假中台，适合本地看效果。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/decodeex/bifu-payment-sdk/platform"
)

type logObserver struct{}

func (logObserver) OnDivergence(_ context.Context, d platform.Divergence) {
	fmt.Printf("  ⚠️  决策差异：商户单号 %s，我们选 %s，中台会选 %s\n",
		d.MerchantOrderNo, d.CallerChannelNo, d.HubChannelNo)
}

func (logObserver) OnError(_ context.Context, stage string, err error) {
	fmt.Printf("  ⚠️  [%s] %v\n", stage, err)
}

func main() {
	base := os.Getenv("PAY_HUB_URL")
	key := os.Getenv("PAY_HUB_API_KEY")
	secret := os.Getenv("PAY_HUB_API_SECRET")

	if base == "" || key == "" || secret == "" {
		fmt.Println("没有配中台凭据，全程使用内置假中台。")
		srv := httptest.NewServer(&fakeHub{secret: "sk_demo"})
		defer srv.Close()
		base, key, secret = srv.URL, "ak_demo", "sk_demo"
		runAll(base, key, secret, false)
		return
	}

	fmt.Printf("中台地址：%s\n", base)
	runAll(base, key, secret, os.Getenv("PAY_HUB_INSECURE") == "1")
}

func runAll(base, key, secret string, insecure bool) {
	ctx := context.Background()
	cli, err := platform.NewClient(platform.Config{
		BaseURL:            base,
		APIKey:             key,
		APISecret:          secret,
		Mode:               platform.ModeShadow,
		Timeout:            10 * time.Second,
		Observer:           logObserver{},
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		fmt.Printf("构造客户端失败：%v\n", err)
		os.Exit(1)
	}

	// ---- 1. 配置快照（B7，已上线）----
	fmt.Println("\n=== 1. 拉配置快照 GET /api/internal/config/snapshot ===")
	snap, err := cli.Snapshot(ctx)
	if err != nil {
		fmt.Printf("失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("商户 %s（业务线 %s），生成于 %s\n", snap.MerchantNo, snap.BusinessLineCode, snap.GeneratedAt)
	fmt.Printf("币种 %d 个，通道 %d 条，点差规则 %d 条\n",
		len(snap.Currencies), len(snap.Channels), len(snap.FxRules))
	for _, c := range snap.Channels {
		fmt.Printf("  通道 %s %-12s 入金=%v 出金=%v 币种对 %d 个\n",
			c.ChannelNo, c.Name, c.SupportsDeposit, c.SupportsWithdraw, len(c.CurrencyPairs))
	}
	for _, r := range snap.FxRules {
		owner := "业务线兜底"
		if r.MerchantNo != nil {
			owner = "商户 " + *r.MerchantNo
		}
		fmt.Printf("  点差 v%d %s/%s %s 通道%s 点差=%s(%s) 手续费=%s+%s%% 取整=%s [%s]\n",
			r.VersionNo, r.FiatCode, r.SettlementCode, r.Direction, r.ChannelNo,
			r.SpreadValue, r.SpreadType, r.FeeFixed, r.FeeRate, r.RoundingMode, owner)
	}

	// 这一条断言是给人看的：接入这一层**不需要**把渠道密钥交给中台
	blob, _ := json.Marshal(snap)
	if strings.Contains(strings.ToLower(string(blob)), "secret") {
		fmt.Println("❌ 快照里出现了 secret 字样 —— 这不该发生，请立刻反馈")
		os.Exit(1)
	}
	fmt.Println("✅ 快照里不含任何密钥字段（渠道凭据仍由业务线自己持有）")

	// ---- 2. 单维度点差（B7，已上线）----
	if len(snap.FxRules) > 0 {
		r := snap.FxRules[0]
		fmt.Println("\n=== 2. 查单维度点差 GET /api/internal/config/fx ===")
		fx, err := cli.FxConfig(ctx, r.ChannelNo, r.FiatCode, r.SettlementCode, r.Direction)
		if err != nil {
			fmt.Printf("失败：%v\n", err)
		} else {
			fmt.Printf("命中层级 %s", fx.MatchedLevel)
			if fx.Full != nil {
				fmt.Printf("，生效版本 v%d 点差 %s(%s)", fx.Full.VersionNo, fx.Full.SpreadValue, fx.Full.SpreadType)
			} else {
				fmt.Print("，⚠️ 该维度没有生效配置 —— 此时应当拒绝报价，不要拿 0 点差顶上")
			}
			if fx.Gray != nil {
				fmt.Printf("，灰度中 v%d 已放量 %d", fx.Gray.VersionNo, fx.Gray.ReleasedCount)
			}
			fmt.Println()
		}
	}

	// ---- 3. 完整新链路 ----
	fmt.Println("\n=== 3. 走一遍完整链路（Shadow 模式）===")
	hub := platform.NewHubWithClient(cli)
	out, err := deposit(ctx, hub)
	if err != nil {
		fmt.Printf("链路返回错误：%v\n", err)
	}
	if out != nil {
		report(out)
	}

	// 真中台上 C 期接口还没有，落到这里说明链路的后半段没跑成。
	// 用内置假中台再跑一次，把整条链路演示完整
	if out != nil && out.Decision.OrderNo == "" && len(out.SuppressedErrors) > 0 {
		fmt.Println("\n--- 中台的 C 期接口还没上线（上面的报错就是证据）。")
		fmt.Println("--- 换内置假中台再跑一次，把整条链路演示完整：")
		srv := httptest.NewServer(&fakeHub{secret: "sk_demo"})
		defer srv.Close()
		fakeCli, err := platform.NewClient(platform.Config{
			BaseURL: srv.URL, APIKey: "ak_demo", APISecret: "sk_demo",
			Mode: platform.ModeEnforce, Observer: logObserver{},
		})
		if err != nil {
			fmt.Printf("构造失败：%v\n", err)
			return
		}
		out2, err2 := deposit(ctx, platform.NewHubWithClient(fakeCli))
		if err2 != nil {
			fmt.Printf("链路返回错误：%v\n", err2)
		}
		if out2 != nil {
			report(out2)
		}
	}
}

// deposit 演示接入方要写的代码：把原来那一次渠道调用放进闭包。
func deposit(ctx context.Context, hub *platform.Hub) (*platform.Outcome, error) {
	ticket := fmt.Sprintf("DEMO%d", time.Now().UnixNano())
	return hub.Deposit(ctx, platform.DepositIntent{
		MerchantOrderNo:    ticket,
		FiatCode:           "CNY",
		SettlementCode:     "USDT",
		RequestAmount:      "7200",
		PreferredChannelNo: "000001",
	}, func(_ context.Context, d platform.Decision) (platform.ExecResult, error) {
		// ↓↓↓ 这里原本是 g.client.Checkout(ctx, req) 之类的一行 ↓↓↓
		fmt.Printf("  → 假渠道下单：通道 %s，中台订单号 %q，成交价 %q，降级=%v\n",
			d.ChannelNo, d.OrderNo, d.DealPrice, d.Degraded)
		paidAt := time.Now()
		return platform.ExecResult{
			ChannelOrderNo:   "CH-DEMO-0001",
			RawChannelStatus: "PENDING",
			RedirectURL:      "https://fake-channel.example.com/pay/abc",
			ChannelPaidAt:    &paidAt,
		}, nil
		// ↑↑↑ 原来那一行 ↑↑↑
	})
}

func report(out *platform.Outcome) {
	fmt.Printf("  结果：通道 %s，中台订单号 %q，支付链接 %s\n",
		out.Decision.ChannelNo, out.Decision.OrderNo, out.Exec.RedirectURL)
	if out.Decision.Route != nil {
		fmt.Printf("  中台候选：%d 条\n", len(out.Decision.Route.Candidates))
	}
	for _, e := range out.SuppressedErrors {
		fmt.Printf("  被吞掉的错误（Shadow 模式下不影响支付）：%v\n", e)
	}
	if len(out.SuppressedErrors) == 0 {
		fmt.Println("  ✅ 全链路无错误")
	}
}

// fakeHub 与 platform 包测试里的那个是同一套逻辑，独立一份是为了让这个示例
// 可以单独拷走运行，不依赖测试文件。
type fakeHub struct{ secret string }

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	switch {
	case r.URL.Path == "/api/internal/config/snapshot":
		_ = json.NewEncoder(w).Encode(platform.Snapshot{
			MerchantNo: "M100001", BusinessLineCode: "bifu",
			GeneratedAt: time.Now().Format(time.RFC3339),
			Currencies:  []platform.Currency{{Code: "CNY", Name: "人民币", Type: "FIAT", Precision: 2}},
			Channels: []platform.Channel{{
				ChannelNo: "000001", Name: "假通道", Type: "P2P", SupportsDeposit: true,
				CurrencyPairs: []platform.CurrencyPair{{Direction: "DEPOSIT", FiatCode: "CNY", SettlementCode: "USDT"}},
			}},
			FxRules: []platform.FxRule{{
				FxVersion: platform.FxVersion{VersionNo: 1, SpreadType: "BPS", SpreadValue: "30",
					FeeFixed: "1", FeeRate: "0.1", RoundingMode: "HALF_UP", PriceScale: 6, AmountScale: 2},
				ChannelNo: "000001", FiatCode: "CNY", SettlementCode: "USDT", Direction: "DEPOSIT",
			}},
		})
	case r.URL.Path == "/api/internal/config/fx":
		_ = json.NewEncoder(w).Encode(platform.FxConfig{
			MatchedLevel: "BUSINESS_LINE", ChannelNo: "000001", FiatCode: "CNY",
			SettlementCode: "USDT", Direction: "DEPOSIT",
			Full: &platform.FxVersion{VersionNo: 1, SpreadType: "BPS", SpreadValue: "30"},
		})
	case r.URL.Path == "/api/gateway/route":
		_ = json.NewEncoder(w).Encode(platform.RouteReply{
			ChannelNo: "000001", BaseEndpoint: "https://fake-channel.example.com",
			CallbackURL: "https://hub.example.com/callback/000001/M100001",
			Candidates: []platform.RouteCandidate{
				{ChannelNo: "000001", Score: 0.92, Reason: "成功率最高"},
				{ChannelNo: "000002", Score: 0.71, Reason: "备选"},
			},
		})
	case r.URL.Path == "/api/gateway/quote":
		_ = json.NewEncoder(w).Encode(platform.QuoteReply{
			QuoteToken: "v1.eyJkZW1vIjp0cnVlfQ.sig", ChannelNo: "000001",
			DealPrice: "0.138472", BasePrice: "0.138889", FeeAmount: "1.997", FxVersionNo: 1,
			ExpiresAtUnixMs: time.Now().Add(3 * time.Minute).UnixMilli(),
		})
	case r.URL.Path == "/api/gateway/orders":
		_ = json.NewEncoder(w).Encode(platform.CreateOrderReply{
			OrderNo: "ORD-DEMO-0001", TxnNo: "PAY-DEMO-0001", Status: "PENDING_PAY",
		})
	case strings.HasPrefix(r.URL.Path, "/api/gateway/orders/") && strings.HasSuffix(r.URL.Path, "/result"):
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Printf("  ← 假中台收到回传：%v\n", body)
		_ = json.NewEncoder(w).Encode(platform.ReportResultReply{OrderNo: "ORD-DEMO-0001", Status: "PROCESSING"})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "NOT_FOUND", "path": r.URL.Path})
	}
}
