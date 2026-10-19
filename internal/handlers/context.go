package handlers

import (
	"context"

	"github.com/GssMahadevan/gvn-explorer/internal/auth"
)

func contextWithUser(ctx context.Context, u auth.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func userFrom(ctx context.Context) auth.User {
	u, _ := ctx.Value(userKey).(auth.User)
	return u
}
