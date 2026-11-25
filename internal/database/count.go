package database

import (
	"context"
	"fmt"
	"strconv"
)

func (c *Client) CountRows(ctx context.Context, table Table) (int64, error) {
	result, err := c.Query(ctx, "SELECT COUNT(*) FROM "+table.Reference(c.Engine())+";")
	if err != nil {
		return 0, err
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		return 0, fmt.Errorf("database returned an invalid row count")
	}
	count, err := strconv.ParseInt(result.Rows[0][0], 10, 64)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("database returned an invalid row count")
	}
	return count, nil
}
