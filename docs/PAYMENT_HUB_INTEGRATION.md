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
| `POST /api/gateway/quote` | 🚧 开发中 | 报价 + 锁汇 |
| `POST /api/gateway/route` | 🚧 开发中 | 选道，返回首选 + 候选 |
| `POST /api/gateway/orders` | 🚧 开发中 | 建单，中台生成订单号 |
| `POST /api/gateway/orders/{orderNo}/result` | 🚧 开发中 | 回传执行结果 |

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

---

## 4. 已经做完的部分（这个分支上）

```
platform/
  doc.go          包说明 + 三档模式 + 接入示例
  signer.go       HMAC-SHA256 签名，与中台逐字节一致
  client.go       Config / HTTP / 错误归一 / Observer
  snapshot.go     配置快照 + 单维度点差（对应中台已上线的两个接口）
  gateway.go      quote / route / orders / result（对应中台开发中的四个接口）
  hub.go          三档模式的编排，就是 §3 里那个 Deposit
  insecure.go     自签证书开关，单独一个文件以便审查时显眼
  *_test.go       14 条测试
  testdata/signature-vectors.json   ← 与中台共用的签名向量
examples/hubdemo/ 可直接跑的 MVP demo
.github/workflows/ci.yml            ← 这个仓库原来没有 CI
```

测试 14 条全绿，`platform` 包语句覆盖率 **79.2%**。

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
商户 M100001（业务线 bifu），生成于 2026-08-18T05:49:57.240Z
币种 11 个，通道 0 条，点差规则 1 条
  点差 v42 CNY/USDT DEPOSIT 通道000003 点差=33(BPS) 手续费=1+0.1% 取整=HALF_UP [商户 M100001]
✅ 快照里不含任何密钥字段（渠道凭据仍由业务线自己持有）

=== 2. 查单维度点差 GET /api/internal/config/fx ===
命中层级 MERCHANT，生效版本 v42 点差 33(BPS)

=== 3. 走一遍完整链路（Shadow 模式）===
  ⚠️  [route] payment hub POST /api/gateway/route: status 404 ...
  ⚠️  [quote] payment hub POST /api/gateway/quote: status 404 ...
  → 假渠道下单：通道 000001，中台订单号 ""，成交价 ""，降级=true
  结果：通道 000001，支付链接 https://fake-channel.example.com/pay/abc
```

这段输出正好把两件事都证明了：

1. **签名与配置读取在真中台上是通的**（快照与点差都拿到了，且中台验签通过）
2. **中台的 C 期接口还没上线（404），而 Shadow 模式把它吞了，支付照常完成** ——
   这就是兼容性承诺的现场证据

demo 检测到 404 之后会自动切到内置假中台再跑一次，把 route → quote → orders →
渠道 → 回传整条链路演示完整。

---

## 6. 还没做的（排期）

| 切片 | 内容 | 依赖 |
|---|---|---|
| ~~S1~~ | ~~签名 + 共用向量~~ ✅ | — |
| ~~S2~~ | ~~中台客户端 + 快照 + 单维度点差~~ ✅ | — |
| ~~S3~~ | ~~三档模式编排 + MVP demo~~ ✅ | — |
| **S4** | 落盘上报队列（进程重启不丢）+ 指数退避 + 死信 | 需要与你们确认存储选型 |
| **S5** | 查单补偿：对停在「处理中」超过 T 的订单主动查单 | 依赖 S4；也依赖渠道查单能力（见 §7.2） |
| **S6** | `bifufx-api` 侧接入（先 1 条通道跑 Shadow） | 需要中台 C3–C5 上线 |
| **S7** | 出金链路 | 渠道文档 |

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

### 7.3 中台缺一个字段：手续费上限

`bifufx-api` 的 `CommonFiatDepositConfig` 里有：

```go
ServiceFeeFixed  decimal.Decimal  // 服务费固定值
ServiceFeeRate   decimal.Decimal  // 服务费比例
ServiceFeeMax    decimal.Decimal  // 服务费最大值   ← 中台没有对应字段
```

中台的点差版本有 `feeFixed` 与 `feeRate`，但**没有 `feeMax`**。
如果现在线上真的在用「服务费上限」，那配置迁到中台之后这个能力会丢。
麻烦确认一下这个字段现在有没有在用；要用的话我在中台补上。

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

## 8. 需要你们拍板的

1. **§7.3 的手续费上限**现在有没有在用？
2. **§7.4 那行 `.gitignore`** 当初是有意为之吗？如果是，请告诉我原因，我换个方式放测试。
3. **S4 上报队列的存储**用什么？（我倾向留成接口，由 `bifufx-api` 注入自己的库）
4. `bifufx-api` 第一个跑 Shadow 的通道选哪条？建议挑量最小的一条。
5. 渠道凭据的归属：目前方案是**继续由业务线自己持有**（中台不下发密钥）。
   中台侧还有一个待拍板的选项是「中台下发、SDK 只在内存持有」，
   那样商户在中台改密钥就能即时生效。你们更倾向哪种？

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
