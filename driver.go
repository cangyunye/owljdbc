package owljdbc

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

func init() {
	sql.Register("owljdbc", &Driver{})
}

type Driver struct{}

func (d *Driver) Open(dsn string) (driver.Conn, error) {
	cfg, err := DecodeDSN(dsn)
	if err != nil {
		return nil, err
	}
	return (&Connector{cfg: cfg}).Connect(context.Background())
}

func (d *Driver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := DecodeDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &Connector{cfg: cfg}, nil
}

type Connector struct{ cfg Config }

func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	s, err := DefaultManager.Acquire(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{sess: s, cfg: c.cfg}, nil
}

func (c *Connector) Driver() driver.Driver { return &Driver{} }

type Conn struct {
	sess *Session
	cfg  Config
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return &Stmt{conn: c, query: query}, nil
}

func (c *Conn) Close() error {
	err := c.sess.Close()
	DefaultManager.Release(c.cfg)
	return err
}

func (c *Conn) Begin() (driver.Tx, error) {
	_, err := c.sess.Exec(context.Background(), ControlRequest{Op: "BEGIN"}, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{sess: c.sess}, nil
}

func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	resp, err := c.sess.Exec(ctx, ControlRequest{Op: "EXEC", SQL: query, Family: c.sess.family}, toAny(args))
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return result{affected: resp.Affected}, nil
}

func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qs, err := c.sess.Query(ctx, ControlRequest{Op: "QUERY", SQL: query, Family: c.sess.family}, toAny(args))
	if err != nil {
		return nil, err
	}
	return &Rows{qs: qs}, nil
}

type Stmt struct {
	conn  *Conn
	query string
}

func (s *Stmt) Close() error  { return nil }
func (s *Stmt) NumInput() int { return -1 }

func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.conn.ExecContext(context.Background(), s.query, namedValues(args))
}
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.QueryContext(context.Background(), s.query, namedValues(args))
}
func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

type Tx struct{ sess *Session }

func (t *Tx) Commit() error {
	_, err := t.sess.Exec(context.Background(), ControlRequest{Op: "COMMIT"}, nil)
	return err
}
func (t *Tx) Rollback() error {
	_, err := t.sess.Exec(context.Background(), ControlRequest{Op: "ROLLBACK"}, nil)
	return err
}

type result struct{ affected int64 }

func (r result) LastInsertId() (int64, error) { return 0, nil }
func (r result) RowsAffected() (int64, error) { return r.affected, nil }

type Rows struct{ qs *QueryStream }

func (r *Rows) Columns() []string {
	cols := r.qs.Cols()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}
func (r *Rows) Close() error { return r.qs.Close() }
func (r *Rows) Next(dest []driver.Value) error {
	row, err := r.qs.Next()
	if err == io.EOF {
		return io.EOF
	}
	if err != nil {
		return err
	}
	for i, v := range row {
		if i < len(dest) {
			dest[i] = v
		}
	}
	return nil
}

func toAny(args []driver.NamedValue) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

func namedValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Value: a}
	}
	return out
}
