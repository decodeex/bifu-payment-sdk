// Package platform 是本 SDK 与「支付中台」对接的部分。
//
// # 它不改动任何现有代码
//
// 这个包是**新增**的，不 import 本仓库任何渠道包，也不要求调用方实现新接口。
// 现有的 asiabank / bft / chippay / help2pay / ifp / long77 / mtpay / peska /
// ragapay / xpay 一行都没动，module 路径没变，也没有引入新的第三方依赖。
// 不接入这个包的调用方，升级前后行为完全一致。
//
// # 三档模式，让新链路可以在不影响现有支付流程的前提下验证
//
//	ModeOff      什么都不做。等价于没接入，留作线上开关
//	ModeShadow   照常调中台（报价 / 选道 / 建单 / 回传），但**始终按调用方自己的
//	             决策执行**；中台的任何错误都不会让这笔支付失败。用来对比
//	             「中台会怎么选」与「我们现在怎么选」，差异记录下来
//	ModeEnforce  按中台的决策执行。定价拿不到或建单失败一律拒单
//
// 迁移路径就是这三档：先 Shadow 跑一段时间看差异，确认无误再切 Enforce。
// Shadow 模式的全部意义在于：它对现有资金流是**只读**的。
//
// # 接入方式：包住你原来那一次渠道调用
//
// 不需要实现接口，把你现在调渠道的那几行放进闭包即可：
//
//	out, err := hub.Deposit(ctx, platform.DepositIntent{
//	    MerchantOrderNo:    ticket,
//	    FiatCode:           "CNY",
//	    SettlementCode:     "USDT",
//	    RequestAmount:      req.FromAmount,
//	    PreferredChannelNo: "000001", // 你自己已经选好的通道
//	}, func(ctx context.Context, d platform.Decision) (platform.ExecResult, error) {
//	    resp, err := g.client.Checkout(ctx, depositReq) // 原来就有的那一行
//	    if err != nil {
//	        return platform.ExecResult{Failed: true, FailureReason: err.Error()}, err
//	    }
//	    return platform.ExecResult{ChannelOrderNo: resp.OrderID, RedirectURL: resp.RedirectURL}, nil
//	})
//
// # 为什么中台要接这一层
//
//   - 配置只有一份：币种、通道、限额、点差、手续费从中台读，不再各业务线各维护一套
//   - 路由决策在中台，可解释、可调整，不用改代码发版
//   - 订单全量汇总到中台，对账才有前提 —— 这是三方对账（中台 / 渠道 / 业务线）
//     里中台那一方的数据来源
package platform
