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

	//userName, err := c.repo.CheckApiKey(token)
	//if err == nil {
	//	c.log.With("username", userName).Debug("user authenticated from database")
	//	c.keys[token] = userName
	//	return &entity.UserAuth{Username: userName}, nil
	//}

	if c.authKey == token {
		userName := "internal"
		c.log.With("username", userName).Debug("user authenticated from config")
		c.keys[token] = userName
		return &entity.UserAuth{Name: userName}, nil
	}

	return nil, fmt.Errorf("invalid token")
}
