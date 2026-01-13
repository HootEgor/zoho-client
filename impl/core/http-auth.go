package core

import (
	"fmt"
	"zohoclient/entity"
)

func (c *Core) AuthenticateByToken(token string) (*entity.UserAuth, error) {
	if token == "" {
		return nil, fmt.Errorf("token not provided")
	}

	// Check cached keys with read lock
	c.keysMu.RLock()
	userName, ok := c.keys[token]
	c.keysMu.RUnlock()
	if ok {
		return &entity.UserAuth{Name: userName}, nil
	}

	if c.authKey == token {
		usr := "internal"
		c.keysMu.Lock()
		c.keys[token] = usr
		c.keysMu.Unlock()
		return &entity.UserAuth{Name: usr}, nil
	}

	return nil, fmt.Errorf("invalid token")
}
