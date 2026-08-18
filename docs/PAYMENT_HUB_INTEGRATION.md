# payment SDK 接入支付中台 · 改造方案（待 review）

> 分支：`feat/payment-hub-integration`
> 作者：AI 自主开发流程（Hendy Chen 名下）
> 请重点看 §1（兼容性承诺）与 §7（我发现的几个问题）

---

## 0. 一句话

新增一个 `platform` 包，让 SDK 能从支付中台读配置、取路由与定价决策、把订单回传中台；
**现有代码一行没动**，不接入的调用方升级前后行为完全一致，接入方也可以先在
「只读影子模式」下跑一段时间再决定要不要真的切换。

---

## 1. 兼容性承诺（这一节是本方案的核心）

| 承诺 | 怎么做到的 |
|---|---|
| 现有 10 个渠道包一行未改 | `platform` 是新增包，**不 import 本仓库任何渠道包** |
| 唯一动到的现有文件是 `.gitignore` | 它原来把所有测试文件都忽略了，见 §7.4。不影响任何调用方的编译 |
| module 路径不变 | 仍是 `github.com/decodeex/bifu-payment-sdk` |
| 不引入新的第三方依赖 | 只用标准库；`go.mod` 未改动 |
| 不要求调用方实现新接口 | 接入点是**包住调用方原有那一次渠道调用**的闭包，见 §3 |
| 不接入 = 零影响 | 不 import `platform` 包的话，编译产物里根本没有它 |
| 接入了也能一键关掉 | `ModeOff`，一个配置项，不用发版回滚 |

### 1.1 为什么特别强调这一点

这个仓库**已经破坏过一次调用方**。`bifufx-api` 现在同时依赖两个 SDK：

```
github.com/decode-ex/payment-sdk         v0.0.5   ← 只给 bft 用
github.com/decodeex/bifu-payment-sdk     v0.1.2-0.20260526051456-ab46c0f7304b
```

它的 `channel/bft/gateway.go` 里留着这段注释：

> 以前一直使用的是 v0.0.5 版本，但是 v0.0.8 版本对 bft 模块就不兼容了……由于时间问题，这个暂时保留

所以本方案的第一原则是**只加不改**。另外注意上面那个依赖是**钉在 commit
`ab46c0f` 上的伪版本**（`refactor/chippay` 分支的 HEAD），所以：

- 本次改动**不动 `refactor/chippay`**，全部放在 `feat/payment-hub-integration`
- 调用方要试的时候，自己把伪版本指到这个分支的 commit 即可；不想试就完全不受影响

### 1.2 三档模式 = 迁移路径

```
ModeOff      什么都不做。线上开关，出问题一键回到现状
ModeShadow   照常调中台，但**始终按调用方自己的决策执行**。
             中台的任何错误都不会让支付失败，差异会通过 Observer 抛出来
ModeEnforce  按中台的决策执行。定价拿不到或建单失败一律拒单
```

**Shadow 是这套方案能被安全验证的关键**：它对现有资金流是只读的。
先在生产上跑 Shadow，看 Observer 报出来的「中台会选 A、我们选了 B」有多少、
成交价差多少，确认无误再切 Enforce。

这一点有测试直接钉住：`TestShadowSurvivesTotalHubOutage` ——
把假中台的 route 与 quote 都改成 500，断言支付照样完成。

---

## 2. 中台那边现在有什么

| 接口 | 状态 | 用途 |
|---|---|---|
| `GET /api/internal/config/snapshot` | ✅ **已上线** | 币种、通道（endpoint / 限额 / 币种对 / 日切）、生效点差 |
| `GET /api/internal/config/fx` | ✅ **已上线** | 单维度点差，并告诉你命中的是商户级还是业务线兜底级 |
| `POST /api/gateway/route` | ✅ **已上线** | 选道，返回首选 + 候选 + **被排除的原因** |
| `POST /api/gateway/orders` | ✅ **已上线** | 建单，中台生成订单号，顺带返回复算结论 |
| `POST /api/gateway/orders/{orderNo}/result` | ✅ **已上线** | 回传执行结果 |

### 2.1 ⚠️ 没有报价接口，成交价由 SDK 本地算

这一节是这份方案 8 月 18 日的**实质变更**，请重点看。

早先的设计里有一个 `POST /gateway/quote`：中台去调渠道取价、加上点差、
返回一个签名的锁汇令牌，建单时带回去。**那个接口不会有了。**

原因是分工变了：中台不碰渠道 API（它没有渠道凭据，也不该有），
于是它没有独立的价格来源，报价接口无从实现。现在的分工是

| 谁 | 负责 |
|---|---|
| 中台 | 点差配置（`/internal/config/fx`）、选道、订单号与状态机、事后复算 |
| 渠道 | 实时汇率 |
| **SDK** | **拿两者在本地算成交价** |

对接入方的影响只有一处：`DepositIntent` 多一个 `Price` 回调，
用来取渠道实时价（你们本来就在调这个渠道）。之所以是回调而不是一个价格字段：
Enforce 模式下最终用哪条通道由中台选道决定，取价必须发生在选道**之后**。

**定价一致性怎么保证**：SDK（Go + shopspring/decimal）与中台（TypeScript + decimal.js）
读**同一份** `pricing-vectors.json`（本仓库 `platform/testdata/` 与中台
`packages/domain/src/` 逐字节一致），两边断言同一批期望值。改这份文件等于改定价口径，
两边的用例会同时红。这个机制已经抓出过两个真实的 Go 侧 bug（手续费多取了一次整、
点差收益的基准少取了一次整）。

**中台的复算不会拒单**：它没有独立价格来源，只能发现「两套实现算得不一样」，
发现不了「有人故意报假价」。既然挡不住作假，拒单就只剩坏处 ——
把 SDK 侧一个计算 bug 放大成全线停摆。所以复算不一致时中台照常建单，
只在响应里给出结论（`priceCheck`）。**SDK 会把它报给 `Observer.OnError`，请务必接上告警**：
它的含义是两套定价实现已经漂了，而这种问题不会自己好，且每一笔都在错。

鉴权：商户 API Key + 时间戳 + HMAC-SHA256 签名，时间窗双向 5 分钟。
**渠道侧的 App Secret 不下发**，渠道凭据仍然由调用方自己持有 ——
接入这一层不需要把任何密钥交出去。

---

## 3. 接入长什么样

以 `bifufx-api` 的 `channel/chippay/gateway.go` 为例，原来是：

```go
func (g *gateway) Deposit(ctx context.Context, ticket string, req *deposit.Request, u *deposit.UserInfo) (*deposit.GatewayDepositReply, error) {
    resp, err := g.client.BuyCoin(ctx, buildReq(ticket, req, u))
    if err != nil {
        return nil, err
    }
    return &deposit.GatewayDepositReply{RedirectURL: resp.PayURL}, nil
}
```

接入之后（`hub` 是进程级单例）：

```go
func (g *gateway) Deposit(ctx context.Context, ticket string, req *deposit.Request, u *deposit.UserInfo) (*deposit.GatewayDepositReply, error) {
    var reply *deposit.GatewayDepositReply

    _, err := g.hub.Deposit(ctx, platform.DepositIntent{
        MerchantOrderNo:    ticket,
        FiatCode:           req.FromCurrency,
        SettlementCode:     req.ToCurrency,
        RequestAmount:      req.FromAmount.String(),
        PreferredChannelNo: g.hubChannelNo, // 你自己已经选好的通道

        // 取渠道实时价。这里换成渠道自己的行情接口即可，
        // 返回渠道**原样**的价 + 它的单位方向，不要在这里做任何换算 ——
        // 换算与点差都由 SDK 按共用向量的口径做，多一处换算就多一处漂移点。
        Price: func(ctx context.Context, channelNo string) (platform.ChannelPrice, error) {
            r, err := g.client.MarketPriceRate(ctx) // 比如 BFT 的 market-price-rate
            if err != nil {
                return platform.ChannelPrice{}, err
            }
            return platform.ChannelPrice{
                RawInPrice:  r.MarketInPrice,  // 6.7600
                RawOutPrice: r.MarketOutPrice, // 6.6800
                Orientation: platform.FiatPerSettlement, // 1 USDT = N CNY
            }, nil
        },
    }, func(ctx context.Context, d platform.Decision) (platform.ExecResult, error) {
        resp, err := g.client.BuyCoin(ctx, buildReq(ticket, req, u)) // ← 原来那一行，没变
        if err != nil {
            return platform.ExecResult{Failed: true, FailureReason: err.Error(), WillRetry: false}, err
        }
        reply = &deposit.GatewayDepositReply{RedirectURL: resp.PayURL}
        return platform.ExecResult{ChannelOrderNo: resp.OrderNo, RedirectURL: resp.PayURL}, nil
    })
    if err != nil {
        return nil, err
    }
    return reply, nil
}
```

要点：

- **不需要改 `deposit.Gateway` 接口**，也不用改工厂注册。你们那套
  `RegisterGatewayFactory` / `GetDepositGateway` / `ProxyGateway` 完全保留
- `WillRetry` 需要你们填。**如果这笔失败之后还要换通道重试，必须置 `true`**：
  中台的终态不可逆，要重试却报了终态，这笔订单就永久死了
- 一开始把 `Mode` 配成 `shadow`，`PreferredChannelNo` 继续填你们自己选的通道
- `Orientation` **必须填对**，而且没有默认值。填错不会报错，只会让每一笔都用错价 ——
  6.76 与 0.1479 差 45 倍，而两者都是「看起来合理」的汇率
- `d.Quote` 里有本地算出的毛额 / 手续费 / 净额。渠道下单要的是净额还是毛额因渠道而异，
  按你们各自渠道的口径取
- `d.PriceCheck` 是中台的复算结论。`nil` 表示**没有复算**（不是「复算通过」），
  比如金额是结算币侧、或者快照缺了复算需要的输入

---

## 4. 已经做完的部分（这个分支上）

```
platform/
  doc.go          包说明 + 三档模式 + 接入示例
  signer.go       HMAC-SHA256 签名，与中台逐字节一致
  client.go       Config / HTTP / 错误归一 / Observer
  snapshot.go     配置快照 + 单维度点差（对应中台已上线的两个接口）
  gateway.go      route / orders / result（对应中台已上线的三个接口）
  pricing.go      本地定价：点差 / 手续费 / 取整，与中台 money.ts 由向量钉住
  hub.go          三档模式的编排，就是 §3 里那个 Deposit
  insecure.go     自签证书开关，单独一个文件以便审查时显眼
  *_test.go       33 条测试，覆盖率 83.9 %
  testdata/signature-vectors.json   ← 与中台共用的签名向量
  testdata/pricing-vectors.json     ← 与中台共用的定价向量（15 条）
examples/hubdemo/ 可直接跑的 MVP demo
.github/workflows/ci.yml            ← 这个仓库原来没有 CI
```

测试 14 条全绿，`platform` 包语句覆盖率 **79.2%**。CI 在本分支上已跑绿
（含 ubuntu 上的 `go test -race ./...`）。

### 4.1 签名向量是与中台共用的同一份文件

`platform/testdata/signature-vectors.json` 与中台仓库
`packages/shared/src/crypto/signature-vectors.json` 内容相同，两边各有一组用例
断言同一批期望签名。这样签名对不上时，**红的那一边就是错的那一边**，不用靠猜
—— 跨语言签名不一致是最难查的一类问题。

6 条向量专门盯 Go 侧最容易踩的坑：

- `path` 必须带 query（用 `URL.RequestURI()`，不是 `URL.Path`）
- body 签的是**原文字节**，签名与发送必须用同一份 buffer
- 时间戳是 **Unix 毫秒**
- method 要 `ToUpper`
- 非 ASCII 的 body 与密钥

Go 侧第一次跑就与中台的期望值全部一致。

---

## 5. MVP demo：真的连了中台跑过

`go run ./examples/hubdemo`。不带环境变量时用内置假中台；带上凭据就打真中台。

**对着真中台（部署环境）跑出来的结果**：

```
=== 1. 拉配置快照 GET /api/internal/config/snapshot ===
商户 M100001（业务线 bifu），生成于 2026-08-18T06:01:16.236Z
币种 11 个，通道 1 条，点差规则 1 条
  通道 000003 zz-fx-e2e    入金=true 出金=true 币种对 2 个
  点差 v76 CNY/USDT DEPOSIT 通道000001 点差=33(BPS) 手续费=1+0.1% 取整=HALF_UP [商户 M100001]
✅ 快照里不含任何密钥字段（渠道凭据仍由业务线自己持有）

=== 2. 查单维度点差 GET /api/internal/config/fx ===
命中层级 MERCHANT，生效版本 v76 点差 33(BPS)

=== 3. 走一遍完整链路（Shadow 模式）===
  → 假渠道下单：通道 000001，中台订单号 "ORD20260818000096"，成交价 "0.147441"，降级=false
     本地算出：毛额 1061.58、手续费 2.06158、用户可得 1059.52
     中台复算：一致=true（期望成交价 0.147441）
  结果：通道 000001，中台订单号 "ORD20260818000096"，支付链接 https://fake-channel.example.com/pay/abc
  中台候选：1 条
  ✅ 全链路无错误
```

这段输出把四件事证明了：

1. **签名与配置读取在真中台上是通的**（快照与点差都拿到了，中台验签通过）
2. **选道 → 建单 → 回传三个接口都通了**，`降级=false` 说明一个都没走兜底
3. **跨语言定价一致**：本地 Go 按商户级 33bps 算出 `0.147441`，
   中台用 TypeScript 独立复算，结论一致。这不是「两边跑同一份代码」——
   是两套实现读同一份向量、算同一笔单，得到同一个数
4. 落库的 `price_orientation` / `fx_rule_version_id` / `amount_side` 都正确
   （直接查了中台的库确认）

> **前一版这份文档里贴的输出是 404 的**（当时 C 期接口还没上线，Shadow 把它吞了）。
> 那段输出证明的是「兼容性承诺有效」；现在这段证明的是「链路真的通了」。
> 两件事都成立，但不要拿旧的那段当现状。

## 6. 还没做的（排期）

| 切片 | 内容 | 依赖 |
|---|---|---|
| ~~S1~~ | ~~签名 + 共用向量~~ ✅ | — |
| ~~S2~~ | ~~中台客户端 + 快照 + 单维度点差~~ ✅ | — |
| ~~S3~~ | ~~三档模式编排 + MVP demo~~ ✅ | — |
| **S4** | 落盘上报队列（进程重启不丢）+ 指数退避 + 死信 | 需要与你们确认存储选型 |
| **S5** | 查单补偿：对停在「处理中」超过 T 的订单主动查单 | 依赖 S4；也依赖渠道查单能力（见 §7.2） |
| **S6** | `bifufx-api` 侧接入（先 1 条通道跑 Shadow） | **依赖已解除**，中台侧接口都上线了，可以开始 |
| **S7** | 出金链路 | 渠道文档 + 两个待拍板的决定，见 §7.6 |

S4 的存储刻意留成接口注入：SDK 不该替调用方选 BoltDB / SQLite / 你们自己的库。

---

## 7. 我在这个过程中发现的问题（请一起看）

### 7.1 中台快照曾经自相矛盾（已修）

拿这个 SDK 真的去消费快照时发现：真环境返回「通道 **0** 条 + 点差 **1** 条」，
而那条点差引用的正是通道 `000003`。原因是快照按 `ACTIVE` 过滤了通道，
却没有过滤引用了非 ACTIVE 通道的点差规则 —— 调用方于是拿到一个自己既没有
endpoint 也没有限额的通道的成交价，用不了还看不出为什么。

中台侧已修，并加了一条断言守着「fxRules 里出现的每条通道，channels 里都要有」。
**这是纯写代码看不出来、只有真的去消费接口才会暴露的问题。**

### 7.2 SDK 侧两个硬缺口

| 缺口 | 影响 |
|---|---|
| **出金 0 个渠道** | 10 个包全是入金。出金链路在 SDK 侧是从零开始 |
| **查单只有 `peska` 和 `ifp`** | 渠道回调可能永远不来，对账要靠主动查单补齐。缺查单 = 缺兜底手段 |

这两条决定了 C 期在 SDK 侧的实际工作量，排期时请一并考虑。

### 7.3 手续费模型不只是缺一个字段（中台已改，但迁移要小心）

上一版这里写的是「中台缺 `feeMax`」。把 `withdrawal/config.go` 读完之后发现，
问题比缺字段严重：**手续费的计算语义本身不一样**。

你们的 `CalculateServiceFee`：

```go
if ServiceFeeFixed > 0 { return ServiceFeeFixed }   // 固定值优先，连上下限都不看
fee := amount * ServiceFeeRate
if fee > ServiceFeeMax { return ServiceFeeMax }
if fee < ServiceFeeMin { return ServiceFeeMin }
```

是**二选一 + 上下限**。中台原先是 `feeFixed + 毛额 × feeRate/100`，**相加、无上下限**。

中台现在补了 `feeMin` / `feeMax`，保持相加骨架：二选一是相加的特例
（固定值那一档把费率置 0 就等价），反过来则表达不了「固定 + 比例」。
迁移变换是

| 你们的配置 | 中台配置 |
|---|---|
| `Fixed > 0` | `feeFixed = Fixed`，`feeRate = 0`，上下限留空 |
| `Fixed = 0, Rate > 0` | `feeFixed = 0`，`feeRate = Rate × 100`，`feeMin = Min`，`feeMax = Max` |

**三件要请你们确认的事**（都不是代码能解决的）：

1. **费率单位差 100 倍**。你们的 `ServiceFeeRate` 是小数（`exchangeFeeRate = 0.01` 表示 1%），
   中台的 `feeRate` 是百分数（`0.1` 表示 0.1%）。迁移时要 ×100。
2. 🔴 **`ServiceFeeMax` 的判断在 `ServiceFeeMin` 之前，且比的是 Go 零值**：
   配了费率**但没配上限**的通道，`fee > 0` 成立 → **直接返回 0**。
   也就是这些通道现在实际收 0 手续费。中台把「上限为空」解释成「不封顶」，
   照搬过去这些通道会**从收 0 变成开始收费**，方向完全反了。
   请帮忙核一下线上配置里 `rate > 0 且 max = 0` 的条目 —— 是「不封顶」还是「真的收 0」。
3. **计费基数不同**。你们出金按**换汇前的账户币金额**算费（先扣费再换汇），
   中台按**换算后的结算币毛额**算。同一笔单两种基数算出的手续费不同，
   这个要拍板选一种。

另外：`deposit/config.go` 里那组 `ServiceFee*` **没有任何调用方**
（全仓 `CalculateServiceFee` 只有 withdrawal 在用，`biz/deposit.go` 明写「入金没有手续费」）。
迁移时不要把它们搬进中台，否则入金会凭空开始扣费。

### 7.4 🔴 `.gitignore` 把所有测试文件都忽略掉了（已改）

仓库根目录 `.gitignore` 的第一行是：

```
**/*_test.go
```

这一行把**整个仓库的 Go 测试文件全部忽略**了。后果：

- 仓库里长期只有一个测试文件（`mtpay/internal/sign_test.go`，而且是 `git add -f`
  强制加进来的才没被吞掉）
- 任何人写了测试，提交时会**静默消失** —— `git status` 里看不到，很难发现
- CI 跑 `go test ./...` 等于什么都没测

我第一次提交时就踩到了：14 个测试写完跑绿，`git add -A` 之后测试文件一个都不在
变更列表里。

这一行几乎肯定是误加的（Go 的测试文件本来就不会进编译产物，没有忽略它的理由），
所以本分支把它去掉了。**这是本次改动里唯一动到的现有文件**，而且它不影响任何
调用方的编译。如果当初是有意为之，请告诉我原因，我改回去并换个方式放测试。

### 7.5 仓库本身的另外两件事

- **默认分支是 `refactor/chippay`，不是 `main`**。调用方钉伪版本钉的也是这个分支的
  commit。建议合回 `main` 并把默认分支改过来，否则「最新代码在哪」长期靠口头传递
- **原来没有 CI**。本分支加了 CI，建议设成必需检查。注意 `gofmt` 与 `go vet`
  目前**只对新增的 platform / examples 生效**，不是全仓 —— 因为现有代码本身没通过：
  `gofmt -l .` 列出十几个未格式化的文件，`go vet ./...` 在 `xpay/fund_in.go` 报四处
  「struct field 有 json tag 但没导出」。为了过门禁去批量改那些文件就违背了本次
  「只加不改」的承诺，所以留成一个独立 PR。

  ⚠️ 其中 **xpay 那四处很可能是真 bug**：json tag 打在未导出字段上是不生效的，
  意味着 `merchantID` / `transactionTime` / `redirectURL` / `callbackURL` 这几个字段
  在序列化 / 反序列化时会被静默忽略。麻烦确认一下 xpay 那条链路现在是不是真的在用。

---

### 7.6 出金链路：不是「包一层」，是从零对接

把 `bifufx-api` 的出金读完之后，S7 的工作量要重估。现在的出金是这么走的
（`biz/withdrawal.go` 的 `handleWithdrawalV2`）：

分布式锁 → 余额校验 → 算费 → **风控决策引擎**（`decision.CommonRule`，
`EventCode=FIAT_WITHDRAW`，可判 refused / manual_review）→ 冻结资金 →
建 `CapitalLog(pending)` → **发工单**（工单里存了完整收款账户快照）→
审批通过后 Kafka 消费 `WithdrawApproveOrder` 落账。

**渠道执行那一段不存在**：我全仓搜过，没有任何法币出金的渠道 API 调用。
银行出金是人工 EFT；Peska 出金的执行方式代码里看不出来（推测渠道后台人工）；
加密货币走 Cobo/ChainUp 托管。这个 SDK 的 10 个渠道包**全是入金**
（`bft` `chippay` `peska` `ifp` `long77` `ragapay` `xpay` `asiabank` `help2pay` `mtpay`），
查单能力只有 `peska` 与 `ifp` 两个有。

也就是说出金**没有任何现成协议代码可以搬**，要按「新渠道对接」估工作量。
另外两件必须先定的事：

1. **审核归属**。中台设计里出金建单进 `PENDING_REVIEW`、在中台后台审核；
   但你们已经有一套风控引擎 + 工单审批在跑。两套并存必然漂移。
   是中台审，还是你们的工单系统通过中台接口代为审批（带上外部工单号）？
   —— 这个我已经在中台的 P0 群里提了，等产品拍。
2. **BFT / Chippay 到底有没有 payout API**？渠道文档我这边没拿到。
   没有的话「一期做出金」就只能是「中台管审核与记录、执行仍然人工」。

还有一个纯技术缺口：中台目前**没有查单接口**，出金审核通过之后
（`PENDING_REVIEW → PROCESSING`）没有任何机制能让 SDK 知道。中台侧要补
`GET /gateway/orders/{orderNo}`（或对业务线的回调），否则这笔会永远停在处理中。

---

## 8. 需要你们拍板的

1. **§7.3 的三件事**：费率单位（×100）、`rate > 0 且 max = 0` 的条目到底是
   「不封顶」还是「真的收 0」、以及手续费的计费基数（含费还是不含费金额）。
   第二条最急 —— 它决定迁移会不会让一批通道从不收费变成开始收费。
2. **§7.4 那行 `.gitignore`** 当初是有意为之吗？如果是，请告诉我原因，我换个方式放测试。
3. **S4 上报队列的存储**用什么？（我倾向留成接口，由 `bifufx-api` 注入自己的库）
4. `bifufx-api` 第一个跑 Shadow 的通道选哪条？建议挑量最小的一条。
5. 渠道凭据的归属：目前方案是**继续由业务线自己持有**（中台不下发密钥）。
   中台侧还有一个待拍板的选项是「中台下发、SDK 只在内存持有」，
   那样商户在中台改密钥就能即时生效。你们更倾向哪种？
6. **出金的审核归属**（§7.6 第 1 条）。这条我在中台的产品群里也提了，
   但你们是实际持有工单系统的一方，意见更重要。
7. 中台的选道会把「该商户在这条通道上没有录入凭据」当**硬性排除**。
   凭据本身不下发，它在中台的存在等于一条「开通登记」。
   接入前请确认要跑的通道在中台后台都登记过 —— 否则选道会一条都不给。

---

## 9. 怎么自己验一遍

```bash
git fetch origin feat/payment-hub-integration
git checkout feat/payment-hub-integration

go test ./platform/ -v          # 14 条，含与中台共用的签名向量
go run ./examples/hubdemo       # 内置假中台，看完整链路

# 打真中台（凭据找我拿）
PAY_HUB_URL=https://<中台地址> \
PAY_HUB_API_KEY=ak_xxx \
PAY_HUB_API_SECRET=sk_xxx \
PAY_HUB_INSECURE=1 \
go run ./examples/hubdemo
```
