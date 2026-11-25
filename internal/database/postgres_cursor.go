package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

const PostgreSQLCursorMaxRows = 10000
const PostgreSQLCursorMaxBytes = maxResultBytes
const cursorIdleTimeout = time.Minute

// PostgreSQLCursor owns only its read-only transaction on a tab's pinned session.
// Its IO methods run on workers; Done can be inspected without blocking the UI.
type PostgreSQLCursor struct {
	mu           sync.Mutex
	client       *Client
	done         chan struct{}
	closed       bool
	pending      []string
	timer        *time.Timer
	idleDeadline time.Time
}

func (p *PostgreSQLCursor) Done() <-chan struct{} { return p.done }

func (c *Client) closePostgreSQLCursor() error {
	c.cursorMu.Lock()
	p := c.cursor
	c.cursorMu.Unlock()
	if p != nil {
		return p.Close()
	}
	return nil
}

func (c *Client) StartPostgreSQLCursor(ctx context.Context, statement string) (*PostgreSQLCursor, Result, error) {
	if c.Engine() != PostgreSQL || c.session == nil || c.parent == nil {
		return nil, Result{}, errors.New("cursor fetching requires a pinned PostgreSQL query tab")
	}
	if err := c.closePostgreSQLCursor(); err != nil {
		return nil, Result{}, err
	}
	p := &PostgreSQLCursor{client: c, done: make(chan struct{})}
	p.mu.Lock()
	defer p.mu.Unlock()
	var result Result
	err := c.session.Raw(func(raw any) error {
		pg, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("PostgreSQL driver unavailable")
		}
		conn := pg.Conn()
		if conn.PgConn().TxStatus() != 'I' {
			return errors.New("finish the editor transaction before using cursor fetching, or turn cursor fetching off")
		}
		if _, err := conn.Exec(ctx, "BEGIN READ ONLY", pgx.QueryExecModeExec); err != nil {
			return cursorQueryError(conn, err)
		}
		// Extended protocol accepts exactly one statement. DECLARE accepts only
		// cursor-capable queries; READ ONLY also rejects writable CTEs/functions.
		const prefix = "DECLARE dbcraft_result NO SCROLL CURSOR WITHOUT HOLD FOR "
		declaration := prefix + statement
		if _, err := conn.PgConn().ExecParams(ctx, declaration, nil, nil, nil, nil).Close(); err != nil {
			p.rollback(conn)
			var position *pgconn.PgError
			if errors.As(err, &position) && position.Position > 0 {
				adjusted := *position
				adjusted.Position = max(0, adjusted.Position-int32(len(prefix)))
				err = &adjusted
			}
			return cursorQueryError(conn, fmt.Errorf("cursor mode needs one read-only SELECT, WITH, or VALUES query: %w", err))
		}
		var err error
		result, err = p.fetch(ctx, conn, MaxRows, maxResultBytes)
		if err != nil || !result.More {
			p.rollback(conn)
		}
		return cursorQueryError(conn, err)
	})
	if err != nil {
		return nil, result, err
	}
	c.cursorMu.Lock()
	c.cursor = p
	c.cursorMu.Unlock()
	if !p.closed {
		p.idleDeadline = time.Now().Add(cursorIdleTimeout)
		p.timer = time.AfterFunc(cursorIdleTimeout, p.expire)
	}
	return p, result, nil
}

func cursorQueryError(conn *pgx.Conn, err error) error {
	if err != nil && conn.IsClosed() {
		return errors.Join(err, driver.ErrBadConn)
	}
	return err
}

// rollback runs inside Raw, keeping driver access scoped to its callback.
func (p *PostgreSQLCursor) rollback(conn *pgx.Conn) {
	if p.closed {
		return
	}
	p.closed = true
	close(p.done)
	if p.timer != nil {
		p.timer.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.Exec(ctx, "ROLLBACK", pgx.QueryExecModeExec); err != nil {
		_ = conn.Close(ctx)
	}
}

func (p *PostgreSQLCursor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *PostgreSQLCursor) expire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if remaining := time.Until(p.idleDeadline); remaining > 0 {
		p.timer.Reset(remaining)
		return
	}
	_ = p.closeLocked()
}

func (p *PostgreSQLCursor) closeLocked() error {
	if p.closed {
		return nil
	}
	err := p.client.session.Raw(func(raw any) error {
		pg, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("PostgreSQL driver unavailable")
		}
		p.rollback(pg.Conn())
		return nil
	})
	if !p.closed {
		p.closed = true
		close(p.done)
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	return err
}

func (p *PostgreSQLCursor) Fetch(ctx context.Context, remainingRows, remainingBytes int) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return Result{}, errors.New("cursor closed or expired; rerun the query")
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	var result Result
	err := p.client.session.Raw(func(raw any) error {
		pg, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("PostgreSQL driver unavailable")
		}
		var err error
		result, err = p.fetch(ctx, pg.Conn(), min(MaxRows, remainingRows), remainingBytes)
		if err != nil || !result.More {
			p.rollback(pg.Conn())
		}
		return cursorQueryError(pg.Conn(), err)
	})
	if err != nil && !p.closed {
		p.closed = true
		close(p.done)
	}
	if !p.closed {
		p.idleDeadline = time.Now().Add(cursorIdleTimeout)
		p.timer.Reset(cursorIdleTimeout)
	}
	return result, err
}

func (p *PostgreSQLCursor) fetch(ctx context.Context, conn *pgx.Conn, limit, byteBudget int) (Result, error) {
	started := time.Now()
	out := Result{}
	if limit <= 0 || byteBudget <= 0 {
		out.Truncated = true
		return out, nil
	}
	count := limit + 1
	pending := p.pending
	p.pending = nil
	if pending != nil {
		count--
	}
	rows, err := conn.Query(ctx, fmt.Sprintf("FETCH FORWARD %d FROM dbcraft_result", count), pgx.QueryExecModeExec)
	if err != nil {
		return out, err
	}
	defer rows.Close() // At most 1,001 rows are ever in this wire result.
	fields := rows.FieldDescriptions()
	for _, field := range fields {
		out.Columns = append(out.Columns, field.Name)
		kind := ""
		if typ, ok := conn.TypeMap().TypeForOID(field.DataTypeOID); ok {
			kind = typ.Name
			if _, array := typ.Codec.(*pgtype.ArrayCodec); array {
				kind += "[]"
			}
		}
		out.ColumnTypes = append(out.ColumnTypes, kind)
	}
	bytes := 0
	appendRow := func(row []string) bool {
		if len(out.Rows) == limit {
			p.pending = row
			out.More = true
			return false
		}
		for _, cell := range row {
			bytes += len(cell)
		}
		if bytes > byteBudget {
			out.Truncated = true
			out.More = false
			p.pending = nil
			return false
		}
		out.Rows = append(out.Rows, row)
		return true
	}
	if pending != nil && !appendRow(pending) {
		return out, nil
	}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return out, err
		}
		row := make([]string, len(values))
		for i, value := range values {
			if value != nil && (fields[i].DataTypeOID == pgtype.JSONOID || fields[i].DataTypeOID == pgtype.JSONBOID) {
				value = string(rows.RawValues()[i])
			}
			row[i] = formatCell(value)
		}
		if !appendRow(row) {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if limit < MaxRows && out.More {
		out.More = false
		out.Truncated = true
		p.pending = nil
	}
	out.Elapsed = time.Since(started)
	return out, nil
}
