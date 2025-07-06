package ctxutil

import "context"

var callbackURLCtxKey struct{ string } = struct{ string }{"callbackURL"}

func InjectCallbackURL(ctx context.Context, callbackURL string) context.Context {
	return context.WithValue(ctx, callbackURLCtxKey, callbackURL)
}

func GetCallbackURL(ctx context.Context, def ...string) string {
	if val, ok := ctx.Value(callbackURLCtxKey).(string); ok {
		return val
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}
