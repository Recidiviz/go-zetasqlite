package internal

import (
	"context"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strings"

	ast "github.com/vantaboard/go-googlesql/resolved_ast"
	"github.com/vantaboard/go-googlesql/types"
)

type StmtAction interface {
	Prepare(context.Context, *Conn) (driver.Stmt, error)
	ExecContext(context.Context, *Conn) (driver.Result, error)
	QueryContext(context.Context, *Conn) (*Rows, error)
	Cleanup(context.Context, *Conn) error
	Args() []interface{}
}

type NullStmtAction struct{}

const NullStatmentActionQuery = "SELECT 'unsupported statement';"

func (a *NullStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	stmt, err := conn.PrepareContext(ctx, NullStatmentActionQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare null statement action: %w", err)
	}
	return newQueryStmt(stmt, nil, NullStatmentActionQuery, []*ColumnSpec{
		&ColumnSpec{Name: "message", Type: &Type{Name: "string", Kind: types.STRING}},
	}), nil
}

func (a *NullStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	return &Result{conn: conn}, nil
}

func (a *NullStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	return &Rows{conn: conn}, nil
}

func (a *NullStmtAction) Args() []interface{} {
	return []interface{}{}
}

func (a *NullStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type CreateTableStmtAction struct {
	query           SQLFragment
	args            []interface{}
	spec            *TableSpec
	catalog         *Catalog
	isAutoIndexMode bool
}

func (a *CreateTableStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	if a.spec.CreateMode == ast.CreateOrReplaceMode {
		if _, err := conn.ExecContext(
			ctx,
			fmt.Sprintf("DROP TABLE IF EXISTS `%s`", a.spec.TableName()),
		); err != nil {
			return nil, err
		}
	}
	stmt, err := conn.PrepareContext(ctx, a.spec.SQLiteSchema())
	if err != nil {
		return nil, fmt.Errorf("failed to prepare %s: %w", a.query, err)
	}
	return newCreateTableStmt(stmt, conn, a.catalog, a.spec), nil
}

func (a *CreateTableStmtAction) createIndexAutomatically(ctx context.Context, conn *Conn) error {
	for _, col := range a.spec.Columns {
		if !col.Type.AvailableAutoIndex() {
			continue
		}
		indexName := fmt.Sprintf("googlesqlite_autoindex_%s_%s", col.Name, strings.Join(a.spec.NamePath, "_"))
		createIndexQuery := fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON `%s`(`%s`)",
			indexName,
			a.spec.TableName(),
			col.Name,
		)
		if _, err := conn.ExecContext(ctx, createIndexQuery); err != nil {
			return fmt.Errorf("failed to create index automatically %s: %w", createIndexQuery, err)
		}
	}
	return nil
}

func (a *CreateTableStmtAction) exec(ctx context.Context, conn *Conn) error {
	if a.spec.CreateMode == ast.CreateOrReplaceMode {
		if _, err := conn.ExecContext(
			ctx,
			fmt.Sprintf("DROP TABLE IF EXISTS `%s`", a.spec.TableName()),
		); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, a.spec.SQLiteSchema(), a.args...); err != nil {
		return fmt.Errorf("failed to exec %s: %w", a.query, err)
	}
	if a.isAutoIndexMode {
		if err := a.createIndexAutomatically(ctx, conn); err != nil {
			return err
		}
	}
	if err := a.catalog.AddNewTableSpec(ctx, conn, a.spec); err != nil {
		return fmt.Errorf("failed to add new table spec: %w", err)
	}
	if !a.spec.IsTemp {
		conn.addTable(a.spec)
	}
	return nil
}

func (a *CreateTableStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *CreateTableStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *CreateTableStmtAction) Args() []interface{} {
	return a.args
}

func (a *CreateTableStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	if !a.spec.IsTemp {
		return nil
	}

	if _, err := conn.ExecContext(
		ctx,
		fmt.Sprintf("DROP TABLE IF EXISTS `%s`", a.spec.TableName()),
	); err != nil {
		return fmt.Errorf("failed to cleanup table %s: %w", a.spec.TableName(), err)
	}
	if err := a.catalog.DeleteTableSpec(ctx, conn, a.spec.TableName()); err != nil {
		return fmt.Errorf("failed to delete table spec: %w", err)
	}
	return nil
}

type CreateViewStmtAction struct {
	query   SQLFragment
	spec    *TableSpec
	catalog *Catalog
}

func (a *CreateViewStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	if a.spec.CreateMode == ast.CreateOrReplaceMode {
		if _, err := conn.ExecContext(
			ctx,
			fmt.Sprintf("DROP VIEW IF EXISTS `%s`", a.spec.TableName()),
		); err != nil {
			return nil, err
		}
	}
	stmt, err := conn.PrepareContext(ctx, a.query.String())
	if err != nil {
		return nil, fmt.Errorf("failed to prepare %s: %w", a.query, err)
	}
	return newCreateViewStmt(stmt, conn, a.catalog, a.spec), nil
}

func (a *CreateViewStmtAction) exec(ctx context.Context, conn *Conn) error {
	if a.spec.CreateMode == ast.CreateOrReplaceMode {
		if _, err := conn.ExecContext(
			ctx,
			fmt.Sprintf("DROP VIEW IF EXISTS `%s`", a.spec.TableName()),
		); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, a.query.String()); err != nil {
		return fmt.Errorf("failed to exec %s: %w", a.query, err)
	}

	if err := a.catalog.AddNewTableSpec(ctx, conn, a.spec); err != nil {
		return fmt.Errorf("failed to add new view spec: %w", err)
	}
	return nil
}

func (a *CreateViewStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *CreateViewStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *CreateViewStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	if !a.spec.IsTemp {
		conn.addTable(a.spec)
		return nil
	}
	if _, err := conn.ExecContext(
		ctx,
		fmt.Sprintf("DROP VIEW IF EXISTS `%s`", a.spec.TableName()),
	); err != nil {
		return fmt.Errorf("failed to cleanup view %s: %w", a.spec.TableName(), err)
	}
	if err := a.catalog.DeleteTableSpec(ctx, conn, a.spec.TableName()); err != nil {
		return fmt.Errorf("failed to delete table spec: %w", err)
	}
	return nil
}

func (a *CreateViewStmtAction) Args() []interface{} {
	return nil
}

type CreateFunctionStmtAction struct {
	spec    *FunctionSpec
	catalog *Catalog
	funcMap map[string]*FunctionSpec
}

func (a *CreateFunctionStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return newCreateFunctionStmt(conn, a.catalog, a.spec), nil
}

func (a *CreateFunctionStmtAction) exec(ctx context.Context, conn *Conn) error {
	if err := a.catalog.AddNewFunctionSpec(ctx, conn, a.spec); err != nil {
		return fmt.Errorf("failed to add new function spec: %w", err)
	}
	a.funcMap[a.spec.FuncName()] = a.spec
	conn.addFunction(a.spec)
	return nil
}

func (a *CreateFunctionStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *CreateFunctionStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *CreateFunctionStmtAction) Args() []interface{} {
	return nil
}

func (a *CreateFunctionStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	if !a.spec.IsTemp {
		return nil
	}
	funcName := a.spec.FuncName()
	if err := a.catalog.DeleteFunctionSpec(ctx, conn, funcName); err != nil {
		return fmt.Errorf("failed to delete function spec: %w", err)
	}
	delete(a.funcMap, funcName)
	return nil
}

type DropStmtAction struct {
	name           string
	objectType     string
	funcMap        map[string]*FunctionSpec
	catalog        *Catalog
	query          string
	formattedQuery string
	args           []interface{}
}

func (a *DropStmtAction) exec(ctx context.Context, conn *Conn) error {
	switch a.objectType {
	case "TABLE", "VIEW":
		if _, err := conn.ExecContext(ctx, a.formattedQuery, a.args...); err != nil {
			return fmt.Errorf("failed to exec %s: %w", a.query, err)
		}
		spec := a.catalog.tableMap[a.name]
		if err := a.catalog.DeleteTableSpec(ctx, conn, a.name); err != nil {
			return fmt.Errorf("failed to delete table spec: %w", err)
		}
		conn.deleteTable(spec)
	case "FUNCTION":
		if err := a.catalog.DeleteFunctionSpec(ctx, conn, a.name); err != nil {
			return fmt.Errorf("failed to delete function spec: %w", err)
		}
		conn.deleteFunction(a.funcMap[a.name])
		delete(a.funcMap, a.name)
	default:
		return fmt.Errorf("currently unsupported DROP %s statement", a.objectType)
	}
	return nil
}

func (a *DropStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *DropStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *DropStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *DropStmtAction) Args() []interface{} {
	return nil
}

func (a *DropStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type DMLStmtAction struct {
	query          string
	params         []*ast.ParameterNode
	args           []interface{}
	formattedQuery string
	catalog        *Catalog
}

var missingObjectPattern = regexp.MustCompile(`no such (table|function): ([^ )]+)`)

func (a *DMLStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	s, err := conn.PrepareContext(ctx, a.formattedQuery)
	if err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, a.formattedQuery, err)
		if retryErr == nil && retriedQuery != a.formattedQuery {
			s, err = conn.PrepareContext(ctx, retriedQuery)
			if err == nil {
				return newDMLStmt(s, a.params, retriedQuery), nil
			}
		}
		return nil, fmt.Errorf("failed to prepare %s: %w", a.query, err)
	}
	return newDMLStmt(s, a.params, a.formattedQuery), nil
}

func (a *DMLStmtAction) exec(ctx context.Context, conn *Conn) (driver.Result, error) {
	result, err := conn.ExecContext(ctx, a.formattedQuery, a.args...)
	if err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, a.formattedQuery, err)
		if retryErr == nil && retriedQuery != a.formattedQuery {
			result, err = conn.ExecContext(ctx, retriedQuery, a.args...)
			if err == nil {
				return result, nil
			}
		}
		return nil, fmt.Errorf("failed to exec %s: %w", a.formattedQuery, err)
	}
	return result, nil
}

func rewriteMissingTableQuery(catalog *Catalog, query string, execErr error) (string, error) {
	match := missingObjectPattern.FindStringSubmatch(execErr.Error())
	if len(match) < 3 {
		return query, execErr
	}
	kind := strings.TrimSpace(match[1])
	missingObject := strings.TrimSpace(match[2])
	if missingObject == "" {
		return query, execErr
	}

	candidate, ok := resolveMissingCatalogObject(catalog, kind, missingObject)
	if !ok {
		return query, execErr
	}
	if kind == "function" {
		replaced := strings.ReplaceAll(query, missingObject+"(", candidate+"(")
		replaced = strings.ReplaceAll(replaced, fmt.Sprintf("`%s`", missingObject), fmt.Sprintf("`%s`", candidate))
		return replaced, nil
	}
	return strings.ReplaceAll(query, fmt.Sprintf("`%s`", missingObject), fmt.Sprintf("`%s`", candidate)), nil
}

func resolveMissingCatalogObject(catalog *Catalog, kind, missingObject string) (string, bool) {
	if catalog == nil {
		return "", false
	}
	var available []string
	switch kind {
	case "table":
		for _, table := range catalog.tables {
			available = append(available, table.TableName())
		}
	case "function":
		for _, fn := range catalog.functions {
			available = append(available, fn.FuncName())
		}
	default:
		return "", false
	}

	parts := strings.Split(missingObject, "_")
	for start := 0; start < len(parts); start++ {
		suffix := strings.Join(parts[start:], "_")
		var candidates []string
		for _, name := range available {
			if name == suffix || strings.HasSuffix(name, "_"+suffix) {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) == 1 {
			return candidates[0], true
		}
	}
	return "", false
}

func (a *DMLStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	result, err := a.exec(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &Result{conn: conn, result: result}, nil
}

func (a *DMLStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if _, err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *DMLStmtAction) Args() []interface{} {
	return nil
}

func (a *DMLStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type QueryStmtAction struct {
	query          string
	params         []*ast.ParameterNode
	args           []interface{}
	formattedQuery string
	outputColumns  []*ColumnSpec
	isExplainMode  bool
	catalog        *Catalog
}

func (a *QueryStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	formattedQuery := expandCatalogFunctions(a.formattedQuery, a.catalog)
	s, err := conn.PrepareContext(ctx, formattedQuery)
	if err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, formattedQuery, err)
		if retryErr == nil && retriedQuery != formattedQuery {
			s, err = conn.PrepareContext(ctx, retriedQuery)
			if err == nil {
				return newQueryStmt(s, a.params, retriedQuery, a.outputColumns), nil
			}
		}
		return nil, fmt.Errorf("failed to prepare %s: %w", a.query, err)
	}
	return newQueryStmt(s, a.params, formattedQuery, a.outputColumns), nil
}

func (a *QueryStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	formattedQuery := expandCatalogFunctions(a.formattedQuery, a.catalog)
	if _, err := conn.ExecContext(ctx, formattedQuery, a.args...); err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, formattedQuery, err)
		if retryErr == nil && retriedQuery != formattedQuery {
			if _, err := conn.ExecContext(ctx, retriedQuery, a.args...); err == nil {
				return &Result{conn: conn}, nil
			}
		}
		return nil, fmt.Errorf("failed to query %s: %w", a.query, err)
	}
	return &Result{conn: conn}, nil
}

func (a *QueryStmtAction) ExplainQueryPlan(ctx context.Context, conn *Conn) error {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("EXPLAIN QUERY PLAN %s", a.formattedQuery), a.args...)
	if err != nil {
		return fmt.Errorf("failed to explain query plan: %w", err)
	}
	defer rows.Close()
	fmt.Println("|selectid|order|from|detail|")
	fmt.Println("----------------------------")
	for rows.Next() {
		var (
			selectID, order, from int
			detail                string
		)
		if err := rows.Scan(&selectID, &order, &from, &detail); err != nil {
			return fmt.Errorf("failed to scan: %w", err)
		}
		fmt.Printf("|%d|%d|%d|%s|\n", selectID, order, from, detail)
	}
	return nil
}

func (a *QueryStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if a.isExplainMode {
		if err := a.ExplainQueryPlan(ctx, conn); err != nil {
			return nil, err
		}
		return &Rows{}, nil
	}
	formattedQuery := expandCatalogFunctions(a.formattedQuery, a.catalog)
	rows, err := conn.QueryContext(ctx, formattedQuery, a.args...)
	if err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, formattedQuery, err)
		if retryErr == nil && retriedQuery != formattedQuery {
			rows, err = conn.QueryContext(ctx, retriedQuery, a.args...)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", a.query, err)
	}
	return &Rows{conn: conn, rows: rows, columns: a.outputColumns}, nil
}

func expandCatalogFunctions(query string, catalog *Catalog) string {
	if catalog == nil {
		return query
	}
	expanded := query
	for _, spec := range catalog.functions {
		if spec == nil || spec.Body == nil {
			continue
		}
		expanded = inlineFunctionCalls(expanded, spec)
	}
	return expanded
}

func inlineFunctionCalls(query string, spec *FunctionSpec) string {
	for _, name := range functionNameAliases(spec.FuncName()) {
		pattern := regexp.MustCompile(fmt.Sprintf("`?%s`?\\s*\\(", regexp.QuoteMeta(name)))
		for {
			loc := pattern.FindStringIndex(query)
			if loc == nil {
				break
			}
			start := loc[0]
			openRel := strings.Index(query[loc[0]:loc[1]], "(")
			if openRel < 0 {
				return query
			}
			argsStart := loc[0] + openRel
			argsEnd := findMatchingParen(query, argsStart)
			if argsEnd < 0 {
				return query
			}
			args := splitFunctionArgs(query[argsStart+1 : argsEnd])
			if len(args) != len(spec.Args) {
				return query
			}
			body := spec.Body.String()
			for i, arg := range spec.Args {
				body = strings.ReplaceAll(body, "@"+arg.Name, strings.TrimSpace(args[i]))
			}
			query = query[:start] + "( " + body + " )" + query[argsEnd+1:]
		}
	}
	return query
}

func functionNameAliases(name string) []string {
	aliases := []string{name}
	parts := strings.Split(name, "_")
	if len(parts) <= 2 {
		return aliases
	}
	for i := 1; i < len(parts)-1; i++ {
		alias := strings.Join(append(append([]string{}, parts[:i]...), parts[i+1:]...), "_")
		aliases = append(aliases, alias)
	}
	return aliases
}

func findMatchingParen(query string, openIdx int) int {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	for i := openIdx; i < len(query); i++ {
		switch query[i] {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '(':
			if !inSingleQuote && !inDoubleQuote {
				depth++
			}
		case ')':
			if !inSingleQuote && !inDoubleQuote {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func splitFunctionArgs(args string) []string {
	var (
		parts         []string
		start         int
		depth         int
		inSingleQuote bool
		inDoubleQuote bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '(':
			if !inSingleQuote && !inDoubleQuote {
				depth++
			}
		case ')':
			if !inSingleQuote && !inDoubleQuote {
				depth--
			}
		case ',':
			if !inSingleQuote && !inDoubleQuote && depth == 0 {
				parts = append(parts, args[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, args[start:])
	return parts
}

func (a *QueryStmtAction) Args() []interface{} {
	return nil
}

func (a *QueryStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type BeginStmtAction struct{}

func (a *BeginStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *BeginStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	return &Result{conn: conn}, nil
}

func (a *BeginStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	return &Rows{conn: conn}, nil
}

func (a *BeginStmtAction) Args() []interface{} {
	return nil
}

func (a *BeginStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type CommitStmtAction struct{}

func (a *CommitStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *CommitStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	return &Result{conn: conn}, nil
}

func (a *CommitStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	return &Rows{conn: conn}, nil
}

func (a *CommitStmtAction) Args() []interface{} {
	return nil
}

func (a *CommitStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type TruncateStmtAction struct {
	query   string
	catalog *Catalog
}

func (a *TruncateStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *TruncateStmtAction) exec(ctx context.Context, conn *Conn) error {
	if _, err := conn.ExecContext(ctx, a.query); err != nil {
		retriedQuery, retryErr := rewriteMissingTableQuery(a.catalog, a.query, err)
		if retryErr == nil && retriedQuery != a.query {
			if _, err := conn.ExecContext(ctx, retriedQuery); err == nil {
				return nil
			}
		}
		return fmt.Errorf("failed to truncate %s: %w", a.query, err)
	}
	return nil
}

func (a *TruncateStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *TruncateStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *TruncateStmtAction) Args() []interface{} {
	return nil
}

func (a *TruncateStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}

type MergeStmtAction struct {
	stmts []string
}

func (a *MergeStmtAction) Prepare(ctx context.Context, conn *Conn) (driver.Stmt, error) {
	return nil, nil
}

func (a *MergeStmtAction) exec(ctx context.Context, conn *Conn) error {
	for _, stmt := range a.stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to exec merge statement %s: %w", stmt, err)
		}
	}
	return nil
}

func (a *MergeStmtAction) ExecContext(ctx context.Context, conn *Conn) (driver.Result, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Result{conn: conn}, nil
}

func (a *MergeStmtAction) QueryContext(ctx context.Context, conn *Conn) (*Rows, error) {
	if err := a.exec(ctx, conn); err != nil {
		return nil, err
	}
	return &Rows{conn: conn}, nil
}

func (a *MergeStmtAction) Args() []interface{} {
	return nil
}

func (a *MergeStmtAction) Cleanup(ctx context.Context, conn *Conn) error {
	return nil
}
