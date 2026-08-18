package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// fakeHub 是一个**会验签**的假中台。
//
// 刻意验签而不是照单全收：不验的话，签名写错了这些用例照样全绿，
// 而真正的失败会等到对接真中台时才出现。
type fakeHub struct {
	t      *testing.T
	secret string

	mu    sync.Mutex
	calls []string
	// 每个路径要不要故意失败
	fail map[string]int
	// 最后一次收到的回传体
	lastReport   ReportResultRequest
	routeChannel string
	fx           FxConfig
	priceCheck   *PriceCheck
	lastOrder    CreateOrderRequest
}

func newFakeHub(t *testing.T, secret string) *fakeHub {
	return &fakeHub{
		t: t, secret: secret, fail: map[string]int{}, routeChannel: "000009",
		fx: defaultFxConfig(),
	}
}

// defaultFxConfig 是一份典型的点差配置：+30bps、1 USDT + 0.1%、无上下限。
//
// 与 pricing-vectors.json 里 BFT 那一条同参数，所以本地算出来的成交价
// 应当正好是 0.147485 —— 测试里可以直接断言这个数。
func defaultFxConfig() FxConfig {
	return FxConfig{
		MatchedLevel:   "BUSINESS_LINE",
		ChannelNo:      "000009",
		FiatCode:       "CNY",
		SettlementCode: "USDT",
		Direction:      "DEPOSIT",
		Full: &FxVersion{
			VersionNo:    42,
			SpreadType:   "BPS",
			SpreadValue:  "30",
			FeeFixed:     "1",
			FeeRate:      "0.1",
			RoundingMode: "HALF_UP",
			PriceScale:   6,
			AmountScale:  2,
		},
	}
}

func (f *fakeHub) called(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == path {
			return true
		}
	}
	return false
}

func (f *fakeHub) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeHub) order() CreateOrderRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastOrder
}

func (f *fakeHub) report() ReportResultRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReport
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := r.URL.RequestURI()

	f.mu.Lock()
	f.calls = append(f.calls, r.URL.Path)
	status, shouldFail := f.fail[r.URL.Path]
	f.mu.Unlock()

	// 验签：头齐全 + 签名对得上
	ts, err := strconv.ParseInt(r.Header.Get("x-timestamp"), 10, 64)
	if err != nil {
		f.t.Errorf("%s: x-timestamp 不是数字: %q", path, r.Header.Get("x-timestamp"))
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Header.Get("x-api-key") == "" {
		f.t.Errorf("%s: 缺 x-api-key", path)
	}
	want := signRequest(f.secret, signatureParts{method: r.Method, path: path, timestamp: ts, body: body})
	if got := r.Header.Get("x-signature"); got != want {
		f.t.Errorf("%s: 签名不对\n  期望 %s\n  实际 %s", path, want, got)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if shouldFail {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{\"code\":\"BOOM\",\"message\":\"假中台故意失败\"}"))
		return
	}

	w.Header().Set("content-type", "application/json")
	switch {
	case r.URL.Path == "/api/gateway/route":
		_ = json.NewEncoder(w).Encode(RouteReply{
			ChannelNo:    f.routeChannel,
			BaseEndpoint: "https://channel.example.com",
			CallbackURL:  "https://hub.example.com/callback/000009/M100001",
			Candidates:   []RouteCandidate{{ChannelNo: f.routeChannel, Score: 0.9, Reason: "成功率最高"}},
		})
	case r.URL.Path == "/api/internal/config/fx":
		// 点差配置。成交价不在这里算了 —— 中台只给点差，价由 SDK 本地算
		_ = json.NewEncoder(w).Encode(f.fx)
	case r.URL.Path == "/api/gateway/orders":
		var req CreateOrderRequest
		if err := json.Unmarshal(body, &req); err != nil {
			f.t.Errorf("建单体解析失败: %v", err)
		}
		f.mu.Lock()
		f.lastOrder = req
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(CreateOrderReply{
			OrderNo:    "ORD20260818001",
			TxnNo:      "PAY20260818001",
			Status:     "PENDING_PAY",
			PriceCheck: f.priceCheck,
		})
	case strings.HasPrefix(r.URL.Path, "/api/gateway/orders/") && strings.HasSuffix(r.URL.Path, "/result"):
		var req ReportResultRequest
		if err := json.Unmarshal(body, &req); err != nil {
			f.t.Errorf("回传体解析失败: %v", err)
		}
		f.mu.Lock()
		f.lastReport = req
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(ReportResultReply{OrderNo: "ORD20260818001", Status: "PROCESSING"})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type recordObserver struct {
	mu          sync.Mutex
	divergences []Divergence
	errs        []string
}

func (o *recordObserver) OnDivergence(_ context.Context, d Divergence) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.divergences = append(o.divergences, d)
}

func (o *recordObserver) OnError(_ context.Context, stage string, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errs = append(o.errs, stage)
}

func newTestHub(t *testing.T, mode Mode, f *fakeHub) (*Hub, *httptest.Server, *recordObserver) {
	srv := httptest.NewServer(f)
	obs := &recordObserver{}
	hub, err := NewHub(Config{
		BaseURL:   srv.URL,
		APIKey:    "ak_test",
		APISecret: "sk_test",
		Mode:      mode,
		Observer:  obs,
	})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	return hub, srv, obs
}

func intent() DepositIntent {
	return DepositIntent{
		MerchantOrderNo:    "BFT20260818000123",
		FiatCode:           "CNY",
		SettlementCode:     "USDT",
		RequestAmount:      "7200",
		PreferredChannelNo: "000001",
		Price:              fakePrice(nil),
	}
}

// fakePrice 返回 BFT 实测的那一组价（6.76 / 6.68，法币每结算币）。
// err 非 nil 时模拟渠道取价失败。
func fakePrice(err error) PriceFunc {
	return func(_ context.Context, _ string) (ChannelPrice, error) {
		if err != nil {
			return ChannelPrice{}, err
		}
		return ChannelPrice{
			RawInPrice:  decimal.RequireFromString("6.7600"),
			RawOutPrice: decimal.RequireFromString("6.6800"),
			Orientation: FiatPerSettlement,
		}, nil
	}
}

func okExec(called *bool, seen *Decision) ExecuteFunc {
	return func(_ context.Context, d Decision) (ExecResult, error) {
		*called = true
		*seen = d
		return ExecResult{
			ChannelOrderNo:   "CH-778899",
			RawChannelStatus: "FINISHED",
			RedirectURL:      "https://pay.example.com/x",
		}, nil
	}
}

// ModeOff 必须一个中台请求都不发 —— 它是线上开关，不能有任何副作用
func TestModeOffTouchesNothing(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeOff, f)
	defer srv.Close()

	var called bool
	var seen Decision
	out, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !called {
		t.Fatal("exec 没被调用")
	}
	if f.callCount() != 0 {
		t.Fatalf("ModeOff 竟然发了 %d 个中台请求", f.callCount())
	}
	if seen.ChannelNo != "000001" {
		t.Fatalf("应当用调用方自己的通道，实际 %q", seen.ChannelNo)
	}
	if len(out.SuppressedErrors) != 0 {
		t.Fatalf("ModeOff 不该有被吞的错误: %v", out.SuppressedErrors)
	}
}

// Shadow 模式的全部意义：中台挂了也不能影响这笔支付
func TestShadowSurvivesTotalHubOutage(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.fail["/api/gateway/route"] = 500
	f.fail["/api/internal/config/fx"] = 500
	hub, srv, obs := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	var called bool
	var seen Decision
	out, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err != nil {
		t.Fatalf("中台挂了不该让支付失败: %v", err)
	}
	if !called {
		t.Fatal("exec 没被调用 —— 支付被中台的故障挡住了")
	}
	if seen.ChannelNo != "000001" {
		t.Fatalf("应当用调用方自己的通道，实际 %q", seen.ChannelNo)
	}
	if !seen.Degraded {
		t.Fatal("拿不到中台决策时应当打降级标记")
	}
	if len(out.SuppressedErrors) == 0 {
		t.Fatal("被吞掉的错误必须返回给调用方，否则没人知道中台在缺数据")
	}
	if len(obs.errs) == 0 {
		t.Fatal("Observer 应当收到错误")
	}
}

// Shadow 模式下中台选了别的通道，也只能记差异，不能真的换
func TestShadowRecordsDivergenceButDoesNotSwitch(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.routeChannel = "000009" // 与调用方的 000001 不同
	hub, srv, obs := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	var called bool
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if seen.ChannelNo != "000001" {
		t.Fatalf("Shadow 模式竟然换了通道: %q", seen.ChannelNo)
	}
	if len(obs.divergences) != 1 {
		t.Fatalf("应当记录 1 条差异，实际 %d", len(obs.divergences))
	}
	if obs.divergences[0].HubChannelNo != "000009" || obs.divergences[0].CallerChannelNo != "000001" {
		t.Fatalf("差异内容不对: %+v", obs.divergences[0])
	}
}

// Enforce 模式才真的按中台的决策走
func TestEnforceUsesHubChannel(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.routeChannel = "000009"
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	var called bool
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if seen.ChannelNo != "000009" {
		t.Fatalf("Enforce 模式应当用中台选的通道，实际 %q", seen.ChannelNo)
	}
	if seen.OrderNo == "" {
		t.Fatal("Enforce 模式必须拿到中台订单号")
	}
}

// 定价拿不到就拒单：猜价格等于亏钱。而且**不能**已经调过渠道了
func TestEnforceRejectsWhenPricingFails(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.fail["/api/internal/config/fx"] = 503
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	var called bool
	var seen Decision
	_, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err == nil {
		t.Fatal("拿不到成交价必须拒单")
	}
	if !errors.Is(err, ErrHubUnavailable) {
		t.Fatalf("错误应当能被 errors.Is(ErrHubUnavailable) 判定，实际 %v", err)
	}
	if called {
		t.Fatal("拒单之后不该再调渠道 —— 钱会真的动")
	}
}

// 建单失败也拒单：没有订单号这笔无法对账
func TestEnforceRejectsWhenCreateOrderFails(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.fail["/api/gateway/orders"] = 500
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	var called bool
	var seen Decision
	_, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err == nil {
		t.Fatal("建单失败必须拒单")
	}
	if called {
		t.Fatal("拒单之后不该再调渠道")
	}
}

// 回传必须带上三个号里的两个（第三个是路径上的中台订单号），以及渠道时间的时区
func TestReportCarriesAllIdentifiers(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	paidAt := time.Date(2026, 8, 18, 10, 20, 0, 0, time.FixedZone("CST", 8*3600))
	_, err := hub.Deposit(context.Background(), intent(), func(_ context.Context, _ Decision) (ExecResult, error) {
		return ExecResult{
			ChannelOrderNo:   "CH-778899",
			RawChannelStatus: "FINISHED",
			PaidAmount:       "7200.00",
			ChannelPaidAt:    &paidAt,
			WillRetry:        false,
		}, nil
	})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !f.called("/api/gateway/orders/ORD20260818001/result") {
		t.Fatal("没有回传结果")
	}
	r := f.report()
	if r.MerchantOrderNo != "BFT20260818000123" {
		t.Errorf("商户订单号丢了: %q", r.MerchantOrderNo)
	}
	if r.ChannelOrderNo == nil || *r.ChannelOrderNo != "CH-778899" {
		t.Errorf("渠道流水号丢了: %v", r.ChannelOrderNo)
	}
	if r.RawChannelStatus == nil || *r.RawChannelStatus != "FINISHED" {
		t.Errorf("渠道原始状态丢了: %v", r.RawChannelStatus)
	}
	// 不带时区的时间在对账时会差几个小时
	if r.ChannelPaidAt == nil || *r.ChannelPaidAt != "2026-08-18T10:20:00+08:00" {
		t.Errorf("渠道时间不对或丢了时区: %v", r.ChannelPaidAt)
	}
}

// 失败且要重试时，willRetry 必须传上去 —— 否则中台会把订单落成终态，
// 而终态不可逆，这笔订单就永久死了
func TestReportPropagatesWillRetryOnFailure(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	_, _ = hub.Deposit(context.Background(), intent(), func(_ context.Context, _ Decision) (ExecResult, error) {
		return ExecResult{Failed: true, FailureReason: "渠道返回余额不足", WillRetry: true}, nil
	})
	r := f.report()
	if r.Status != "FAILED" {
		t.Errorf("状态应当是 FAILED，实际 %q", r.Status)
	}
	if !r.WillRetry {
		t.Error("willRetry 没传上去 —— 中台会把订单落成终态，这笔就永久死了")
	}
	if r.FailureReason == nil || *r.FailureReason == "" {
		t.Error("失败原因丢了")
	}
}

// exec 返回的错误要原样传出去，不能被这一层吞掉
func TestExecErrorIsReturned(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	boom := errors.New("渠道超时")
	_, err := hub.Deposit(context.Background(), intent(), func(_ context.Context, _ Decision) (ExecResult, error) {
		return ExecResult{Failed: true, FailureReason: "渠道超时", WillRetry: true}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("exec 的错误被吞了: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]Config{
		"缺 BaseURL":   {APIKey: "a", APISecret: "b"},
		"缺 APIKey":    {BaseURL: "http://x", APISecret: "b"},
		"缺 APISecret": {BaseURL: "http://x", APIKey: "a"},
		"未知 Mode":     {BaseURL: "http://x", APIKey: "a", APISecret: "b", Mode: Mode("weird")},
	}
	for name, cfg := range cases {
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("%s 应当在构造时就报错", name)
		} else if !errors.Is(err, ErrNotConfigured) {
			t.Errorf("%s 的错误应当能被 errors.Is(ErrNotConfigured) 判定，实际 %v", name, err)
		}
	}
	// 不传 Mode 默认 shadow —— 接入的第一步不应该改变任何现有行为
	c, err := NewClient(Config{BaseURL: "http://x", APIKey: "a", APISecret: "b"})
	if err != nil {
		t.Fatalf("合法配置报错了: %v", err)
	}
	if c.Mode() != ModeShadow {
		t.Fatalf("默认模式应当是 shadow，实际 %q", c.Mode())
	}
}

// ---------------------------------------------------------------------------
// 本地定价（架构修订后的核心路径）。
//
// 修订前这一段是「调中台 /gateway/quote 拿价」，而那个接口已经不存在了 ——
// 也就是说本包对着真中台一笔都跑不通，Enforce 会全拒单、Shadow 一条数据都不上报。
// 下面这些用例钉住新的分工：中台给点差、渠道给实时价、成交价在本地算。
// ---------------------------------------------------------------------------

// 本地算出来的成交价必须与共用向量一致 —— 这是 SDK 与中台不漂移的唯一保证
func TestLocalPricingMatchesSharedVector(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	out, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if len(out.SuppressedErrors) != 0 {
		t.Fatalf("不该有被吞掉的错误: %v", out.SuppressedErrors)
	}
	// 6.76 CNY/USDT 取倒数 = 0.147928994...，下浮 30bps 后取 6 位 = 0.147485
	// 与 pricing-vectors.json 里 BFT 那一条完全一致
	if seen.DealPrice != "0.147485" {
		t.Errorf("成交价 = %q，期望 0.147485（与共用向量一致）", seen.DealPrice)
	}
	if seen.Pricing == nil {
		t.Fatal("定价快照不该为 nil")
	}
	if seen.Pricing.ChannelRawPrice != "6.76" || seen.Pricing.PriceOrientation != "FIAT_PER_SETTLEMENT" {
		t.Errorf("快照里的渠道原始价 / 方向不对: %+v", seen.Pricing)
	}
	if seen.Pricing.FxRuleVersionNo != 42 {
		t.Errorf("点差版本号 = %d，期望 42", seen.Pricing.FxRuleVersionNo)
	}
	if seen.Quote == nil || seen.Quote.NetCrypto.String() != "1059.83" {
		t.Errorf("净额不对: %+v", seen.Quote)
	}
}

// 建单请求必须带上复算需要的全部输入，以及金额口径。少一项中台就无法复算
func TestCreateOrderCarriesFullSnapshot(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	got := f.order()
	if got.AmountSide != "FIAT" {
		t.Errorf("入金的金额口径应当是 FIAT，实际 %q", got.AmountSide)
	}
	if got.FiatCode != "CNY" || got.SettlementCode != "USDT" {
		t.Errorf("币种对没带上: %+v", got)
	}
	for name, v := range map[string]string{
		"channelRawPrice":  got.Pricing.ChannelRawPrice,
		"priceOrientation": got.Pricing.PriceOrientation,
		"dealPrice":        got.Pricing.DealPrice,
		"feeAmount":        got.Pricing.FeeAmount,
		"spreadType":       got.Pricing.SpreadType,
		"spreadValue":      got.Pricing.SpreadValue,
		"roundingMode":     got.Pricing.RoundingMode,
	} {
		if v == "" {
			t.Errorf("快照缺 %s —— 中台复算需要它", name)
		}
	}
}

// 手续费上下限必须原样带进快照。不带的话中台按配置里的上下限复算，
// 两边稳定差一个夹取，每一笔都会被标成异常
func TestFeeLimitsFlowIntoSnapshot(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	min, max := "0.5", "1.5"
	f.fx.Full.FeeMin, f.fx.Full.FeeMax = &min, &max
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	got := f.order()
	if got.Pricing.FeeMin == nil || *got.Pricing.FeeMin != min {
		t.Errorf("快照里的手续费下限 = %v，期望 %s", got.Pricing.FeeMin, min)
	}
	if got.Pricing.FeeMax == nil || *got.Pricing.FeeMax != max {
		t.Errorf("快照里的手续费上限 = %v，期望 %s", got.Pricing.FeeMax, max)
	}
	// 未夹取时是 1 + 1061.89×0.1% = 2.06189，上限 1.5 应当把它压下来
	if got.Pricing.FeeAmount != "1.5" {
		t.Errorf("手续费 = %q，上限 1.5 没生效", got.Pricing.FeeAmount)
	}
}

// 灰度命中时要用灰度那一版的点差算价，并把 releaseId 报上去 —— 否则计数无处归属
func TestGrayHitUsesGrayVersionAndReportsReleaseID(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	scope, threshold := "FIRST_N_ORDERS", 100
	f.fx.Gray = &GrayFx{
		FxVersion: FxVersion{
			VersionNo: 43, SpreadType: "BPS", SpreadValue: "50",
			FeeFixed: "1", FeeRate: "0.1", RoundingMode: "HALF_UP",
			PriceScale: 6, AmountScale: 2,
		},
		ReleaseID: "77", Scope: &scope, Threshold: &threshold, ReleasedCount: 3,
	}
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	got := f.order()
	if got.Gray == nil || !got.Gray.Hit {
		t.Fatalf("应当报成灰度命中: %+v", got.Gray)
	}
	if got.Gray.ReleaseID == nil || *got.Gray.ReleaseID != "77" {
		t.Errorf("releaseId = %v，期望 77", got.Gray.ReleaseID)
	}
	if got.Pricing.FxRuleVersionNo != 43 {
		t.Errorf("命中灰度就该用灰度版（43）算价，实际用了 %d", got.Pricing.FxRuleVersionNo)
	}
	// 50bps 下浮：0.147928994... × (1 − 0.005) = 0.147189
	if got.Pricing.DealPrice != "0.147189" {
		t.Errorf("成交价 = %q，没有按灰度版的 50bps 算", got.Pricing.DealPrice)
	}
}

// 已放量数达到阈值就不再命中 —— 否则灰度永远不会结束
func TestGrayStopsAtThreshold(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	scope, threshold := "FIRST_N_ORDERS", 5
	f.fx.Gray = &GrayFx{
		FxVersion:     FxVersion{VersionNo: 43, SpreadType: "BPS", SpreadValue: "50", FeeFixed: "1", FeeRate: "0.1", RoundingMode: "HALF_UP", PriceScale: 6, AmountScale: 2},
		ReleaseID:     "77",
		Scope:         &scope,
		Threshold:     &threshold,
		ReleasedCount: 5,
	}
	hub, srv, _ := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if got := f.order(); got.Gray != nil {
		t.Errorf("已放满就不该再命中: %+v", got.Gray)
	} else if got.Pricing.FxRuleVersionNo != 42 {
		t.Errorf("应当回落到全量版 42，实际 %d", got.Pricing.FxRuleVersionNo)
	}
}

// 按流量比例分流时，同一笔单反复算必须落在同一侧 —— 重试的幂等性依赖它
func TestGrayPercentIsStableForSameOrder(t *testing.T) {
	scope, threshold := "PERCENT_TRAFFIC", 50
	g := &GrayFx{Scope: &scope, Threshold: &threshold}
	first := grayHit(g, "BFT20260818000123")
	for i := 0; i < 50; i++ {
		if grayHit(g, "BFT20260818000123") != first {
			t.Fatal("同一个订单号两次判定结果不同 —— 重试会在灰度两侧跳")
		}
	}
	// 0% 与 100% 是边界：一个都不该中，全都该中
	zero, all := 0, 100
	g.Threshold = &zero
	if grayHit(g, "any") {
		t.Error("阈值 0 不该命中")
	}
	g.Threshold = &all
	if !grayHit(g, "any") {
		t.Error("阈值 100 应当全部命中")
	}
}

// 没给取价回调就直接报错，不要拿一个零值价去算
func TestMissingPriceFuncFails(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	in := intent()
	in.Price = nil
	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), in, okExec(&called, &seen)); err == nil {
		t.Fatal("没有取价回调时 Enforce 必须拒单")
	}
	if called {
		t.Error("拒单了就不该再去调渠道")
	}
}

// 渠道取价失败：Enforce 拒单，Shadow 降级执行（用户的支付不该因为中台侧的事失败）
func TestChannelPriceFailure(t *testing.T) {
	for _, tc := range []struct {
		mode       Mode
		wantErr    bool
		wantCalled bool
	}{
		{ModeEnforce, true, false},
		{ModeShadow, false, true},
	} {
		f := newFakeHub(t, "sk_test")
		hub, srv, _ := newTestHub(t, tc.mode, f)
		in := intent()
		in.Price = fakePrice(errors.New("渠道取价超时"))
		called := false
		var seen Decision
		_, err := hub.Deposit(context.Background(), in, okExec(&called, &seen))
		if (err != nil) != tc.wantErr {
			t.Errorf("%v: err = %v，期望有错 = %v", tc.mode, err, tc.wantErr)
		}
		if called != tc.wantCalled {
			t.Errorf("%v: 渠道被调用 = %v，期望 %v", tc.mode, called, tc.wantCalled)
		}
		if tc.mode == ModeShadow && !seen.Degraded {
			t.Error("Shadow 下定价失败必须打降级标记")
		}
		srv.Close()
	}
}

// 中台复算不一致要走 Observer 报出来。静默的话两侧漂移可以漂很久没人知道
func TestPriceCheckMismatchIsObserved(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	reason := "成交价复算不一致：应为 0.147485，回传 0.99"
	f.priceCheck = &PriceCheck{OK: false, ExpectedDealPrice: "0.147485", Reason: &reason}
	hub, srv, obs := newTestHub(t, ModeShadow, f)
	defer srv.Close()

	called := false
	var seen Decision
	if _, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen)); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if !called {
		t.Error("复算不一致不该阻断这一笔")
	}
	found := false
	for _, stage := range obs.errs {
		if stage == "priceCheck" {
			found = true
		}
	}
	if !found {
		t.Errorf("复算不一致没有报给 Observer，实际收到的阶段: %v", obs.errs)
	}
}

// 点差配置缺失时绝不拿 0 点差顶上 —— 那等于把平台该收的白送，且一笔都不报错
func TestNoFxConfigIsRejectedNotDefaulted(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.fx.Full = nil
	hub, srv, _ := newTestHub(t, ModeEnforce, f)
	defer srv.Close()

	called := false
	var seen Decision
	_, err := hub.Deposit(context.Background(), intent(), okExec(&called, &seen))
	if err == nil {
		t.Fatal("没有生效点差配置时必须拒单")
	}
	if called {
		t.Error("拒单了就不该调渠道")
	}
}
