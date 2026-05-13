package bifu_payment_sdk

import "context"

var callbackURLCtxKey struct{ string } = struct{ string }{"callbackURL"}

func InjectCallbackURL(ctx context.Context, callbackURL string) context.Context {
	return context.WithValue(ctx, callbackURLCtxKey, callbackURL)
}
