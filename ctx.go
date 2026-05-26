package bifu_payment_sdk

import (
	"context"

	"github.com/decodeex/bifu-payment-sdk/internal/ctxutil"
)

// InjectCallbackURL 注入回调地址
// 一些时候, 需要一些独立于全局的配置. 这些配置散落在各个请求中又比较啰唆, 因此通过ctx做定制
// 并不是所有渠道都支持
func InjectCallbackURL(ctx context.Context, callbackURL string) context.Context {
	return ctxutil.InjectCallbackURL(ctx, callbackURL)
}
