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
}

func newFakeHub(t *testing.T, secret string) *fakeHub {
	return &fakeHub{t: t, secret: secret, fail: map[string]int{}, routeChannel: "000009"}
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
	case r.URL.Path == "/api/gateway/quote":
		_ = json.NewEncoder(w).Encode(QuoteReply{
			QuoteToken:      "v1.payload.sig",
			ChannelNo:       f.routeChannel,
			DealPrice:       "0.138472",
			BasePrice:       "0.138889",
			FeeAmount:       "1.997",
			FxVersionNo:     42,
			ExpiresAtUnixMs: 1_770_000_180_000,
		})
	case r.URL.Path == "/api/gateway/orders":
		_ = json.NewEncoder(w).Encode(CreateOrderReply{OrderNo: "ORD20260818001", TxnNo: "PAY20260818001", Status: "PENDING_PAY"})
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
	f.fail["/api/gateway/quote"] = 500
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
func TestEnforceRejectsWhenQuoteFails(t *testing.T) {
	f := newFakeHub(t, "sk_test")
	f.fail["/api/gateway/quote"] = 503
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
