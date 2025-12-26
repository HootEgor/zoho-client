package core

import (
	"fmt"
	"zohoclient/entity"
)

func (c *Core) AuthenticateByToken(token string) (*entity.UserAuth, error) {
	if token == "" {
		return nil, fmt.Errorf("token not provided")
	}

	if userName, ok := c.keys[token]; ok {
		return &entity.UserAuth{Name: userName}, nil
	}

	if c.authKey == token {
		userName := "internal"
		c.keys[token] = userName
		return &entity.UserAuth{Name: userName}, nil
	}

	return nil, fmt.Errorf("invalid token")
}
