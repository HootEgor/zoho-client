package cont

import (
	"context"
	"zohoclient/entity"
)

type ctxKey string

const UserDataKey ctxKey = "userData"

func PutUser(c context.Context, user *entity.UserAuth) context.Context {
	return context.WithValue(c, UserDataKey, *user)
}

func GetUser(c context.Context) *entity.UserAuth {
	user, ok := c.Value(UserDataKey).(entity.UserAuth)
	if !ok {
		return &entity.UserAuth{}
	}
	return &user
}
