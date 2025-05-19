package core

import "fmt"

func (c *Core) ReadDatabase(table, filter string, limit int, plain bool) (interface{}, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("database service not available")
	}
	if limit > 100 {
		limit = 100
	}
	return c.repo.ReadTable(table, filter, limit, plain)
}
