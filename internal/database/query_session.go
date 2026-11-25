package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

const MaxQuerySessions = 16

// NewQuerySession pins a physical SQL connection to a tab. Catalog operations
// retain a separate pool slot. BigQuery sessions share the client, not jobs.
func (c *Client) NewQuerySession(ctx context.Context) (*Client, error) {
	if c.parent != nil {
		return c.parent.NewQuerySession(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.sessionsMu.Lock()
	if c.closing {
		c.sessionsMu.Unlock()
		return nil, fmt.Errorf("connection is closing")
	}
	if c.slots == nil {
		c.slots = make(chan struct{}, MaxQuerySessions)
	}
	slots := c.slots
	c.sessionsMu.Unlock()
	select {
	case slots <- struct{}{}:
	default:
		return nil, fmt.Errorf("connection has %d query sessions; close an unused query tab to open another", MaxQuerySessions)
	}
	child := &Client{parent: c, config: c.config, bq: c.bq, bqAPI: c.bqAPI, db: c.db}
	if c.bq == nil {
		conn, err := c.db.Conn(ctx)
		if err != nil {
			<-slots
			return nil, err
		}
		child.session = conn
	}
	c.sessionsMu.Lock()
	if c.closing {
		c.sessionsMu.Unlock()
		child.Close()
		return nil, fmt.Errorf("connection is closing")
	}
	if c.sessions == nil {
		c.sessions = map[*Client]struct{}{}
	}
	c.sessions[child] = struct{}{}
	c.sessionsMu.Unlock()
	return child, nil
}

func (c *Client) closeClient() error {
	_ = c.closePostgreSQLCursor()
	if c.parent != nil {
		var err error
		if c.session != nil {
			// Discard instead of pooling session-local SET/ATTACH/temp state or
			// unfinished transactions into another tab or catalog operation.
			err = c.session.Raw(func(any) error { return driver.ErrBadConn })
			if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
				err = nil
			}
			c.session.Close()
		}
		c.parent.sessionsMu.Lock()
		delete(c.parent.sessions, c)
		c.parent.sessionsMu.Unlock()
		<-c.parent.slots
		return err
	}
	c.sessionsMu.Lock()
	c.closing = true
	children := make([]*Client, 0, len(c.sessions))
	for child := range c.sessions {
		children = append(children, child)
	}
	c.sessionsMu.Unlock()
	for _, child := range children {
		child.Close()
	}
	if c.bq != nil {
		return c.bq.Close()
	}
	if c.session != nil {
		c.session.Close()
	}
	return c.db.Close()
}
