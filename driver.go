package googlesqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"google.golang.org/api/bigquery/v2"
	internal "github.com/vantaboard/go-googlesqlite/internal"
	_ "modernc.org/sqlite"
	"sync"
)

var (
	_ driver.Driver = &GoogleSQLiteDriver{}
	_ driver.Conn   = &GoogleSQLiteConn{}
	_ driver.Tx     = &GoogleSQLiteTx{}
)

var (
	nameToCatalogMap = map[string]*internal.Catalog{}
	nameToDBMap      = map[string]*sql.DB{}
	nameToValueMapMu sync.Mutex
)

func init() {
	if err := internal.RegisterFunctions(); err != nil {
		fmt.Printf("failed to register functions: %s", err)
	}

	sql.Register("googlesqlite", &GoogleSQLiteDriver{})
}

func newDBAndCatalog(name string) (*sql.DB, *internal.Catalog, error) {
	nameToValueMapMu.Lock()
	defer nameToValueMapMu.Unlock()
	db, exists := nameToDBMap[name]
	if exists {
		return db, nameToCatalogMap[name], nil
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database by %s: %w", name, err)
	}
	catalog, err := internal.NewCatalog(db)
	if err != nil {
		return nil, nil, fmt.Errorf("failed open database by %s: failed to initialize catalog: %w", name, err)
	}
	nameToDBMap[name] = db
	nameToCatalogMap[name] = catalog
	return db, catalog, nil
}

type GoogleSQLiteDriver struct {
	ConnectHook func(*GoogleSQLiteConn) error
}

func (d *GoogleSQLiteDriver) Open(name string) (driver.Conn, error) {
	db, catalog, err := newDBAndCatalog(name)
	if err != nil {
		return nil, err
	}
	conn, err := newGoogleSQLiteConn(db, catalog)
	if err != nil {
		return nil, err
	}
	if d.ConnectHook != nil {
		if err := d.ConnectHook(conn); err != nil {
			return nil, err
		}
	}
	return conn, nil
}

type GoogleSQLiteConn struct {
	conn     *sql.Conn
	tx       *sql.Tx
	analyzer *internal.Analyzer
}

func newGoogleSQLiteConn(db *sql.DB, catalog *internal.Catalog) (*GoogleSQLiteConn, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get sqlite3 connection: %w", err)
	}
	analyzer, err := internal.NewAnalyzer(catalog)
	if err != nil {
		return nil, fmt.Errorf("failed to create analyzer: %w", err)
	}
	return &GoogleSQLiteConn{
		conn:     conn,
		analyzer: analyzer,
	}, nil
}

func (c *GoogleSQLiteConn) SetAutoIndexMode(enabled bool) {
	c.analyzer.SetAutoIndexMode(enabled)
}

func (c *GoogleSQLiteConn) SetExplainMode(enabled bool) {
	c.analyzer.SetExplainMode(enabled)
}

// SetMaxNamePath specifies the maximum value of name path.
// If the name path in the query is the maximum value, the name path set as prefix is not used.
// Effective only when a value greater than zero is specified ( default zero ).
func (c *GoogleSQLiteConn) SetMaxNamePath(num int) {
	c.analyzer.SetMaxNamePath(num)
}

// MaxNamePath returns maximum value of name path.
func (c *GoogleSQLiteConn) MaxNamePath() int {
	return c.analyzer.MaxNamePath()
}

// SetNamePath set path to name path to be set as prefix.
// If max name path is specified, an error is returned if the number is exceeded.
func (c *GoogleSQLiteConn) SetNamePath(path []string) error {
	return c.analyzer.SetNamePath(path)
}

// NamePath returns path to name path to be set as prefix.
func (c *GoogleSQLiteConn) NamePath() []string {
	return c.analyzer.NamePath()
}

// AddNamePath add path to name path to be set as prefix.
// If max name path is specified, an error is returned if the number is exceeded.
func (c *GoogleSQLiteConn) AddNamePath(path string) error {
	return c.analyzer.AddNamePath(path)
}

func (c *GoogleSQLiteConn) SetQueryParameters(parameters []*bigquery.QueryParameter) {
	c.analyzer.SetQueryParameters(parameters)
}

func (s *GoogleSQLiteConn) CheckNamedValue(value *driver.NamedValue) error {
	return nil
}

func (c *GoogleSQLiteConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.PrepareContext(context.Background(), query)
	return stmt, err
}

func (c *GoogleSQLiteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	conn := internal.NewConn(c.conn, c.tx)
	actionFuncs, err := c.analyzer.Analyze(ctx, conn, query, nil)
	if err != nil {
		return nil, err
	}
	var stmt driver.Stmt
	for _, actionFunc := range actionFuncs {
		action, err := actionFunc()
		if err != nil {
			return nil, err
		}
		s, err := action.Prepare(ctx, conn)
		if err != nil {
			return nil, err
		}
		stmt = s
	}
	return stmt, nil
}

func (c *GoogleSQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, e error) {
	conn := internal.NewConn(c.conn, c.tx)
	actionFuncs, err := c.analyzer.Analyze(ctx, conn, query, args)
	if err != nil {
		return nil, err
	}
	var actions []internal.StmtAction
	defer func() {
		eg := new(internal.ErrorGroup)
		eg.Add(e)
		for _, action := range actions {
			eg.Add(action.Cleanup(ctx, conn))
		}
		if eg.HasError() {
			e = eg
		}
	}()

	var result driver.Result
	for _, actionFunc := range actionFuncs {
		action, err := actionFunc()
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
		r, err := action.ExecContext(ctx, conn)
		if err != nil {
			return nil, err
		}
		result = r
	}
	return result, nil
}

func (c *GoogleSQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Rows, e error) {
	conn := internal.NewConn(c.conn, c.tx)
	actionFuncs, err := c.analyzer.Analyze(ctx, conn, query, args)
	if err != nil {
		return nil, err
	}
	var (
		actions []internal.StmtAction
		rows    *internal.Rows
	)
	defer func() {
		if rows != nil {
			// If we call cleanup action at the end of QueryContext function,
			// there is a possibility that the deleted table will be referenced when scanning from Rows,
			// so cleanup action should be executed in the Close() process of Rows.
			// For that, let Rows have a reference to actions ( and connection ).
			rows.SetActions(actions)
		}
	}()
	for _, actionFunc := range actionFuncs {
		action, err := actionFunc()
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
		queryRows, err := action.QueryContext(ctx, conn)
		if err != nil {
			return nil, err
		}
		rows = queryRows
	}
	return rows, nil
}

func (c *GoogleSQLiteConn) Close() error {
	return c.conn.Close()
}

func (c *GoogleSQLiteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.conn.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.IsolationLevel(opts.Isolation),
		ReadOnly:  opts.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	c.tx = tx
	return &GoogleSQLiteTx{
		tx:   tx,
		conn: c,
	}, nil
}

func (c *GoogleSQLiteConn) Begin() (driver.Tx, error) {
	tx, err := c.conn.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	c.tx = tx
	return &GoogleSQLiteTx{
		tx:   tx,
		conn: c,
	}, nil
}

type GoogleSQLiteTx struct {
	tx   *sql.Tx
	conn *GoogleSQLiteConn
}

func (tx *GoogleSQLiteTx) Commit() error {
	defer func() {
		tx.conn.tx = nil
	}()
	return tx.tx.Commit()
}

func (tx *GoogleSQLiteTx) Rollback() error {
	defer func() {
		tx.conn.tx = nil
	}()
	return tx.tx.Rollback()
}
