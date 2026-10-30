package internal

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/goccy/go-zetasql/types"
)

var (
	createCatalogTableQuery = `
CREATE TABLE IF NOT EXISTS zetasqlite_catalog(
  name STRING NOT NULL PRIMARY KEY,
  kind STRING NOT NULL,
  spec STRING NOT NULL,
  updatedAt TIMESTAMP NOT NULL,
  createdAt TIMESTAMP NOT NULL
)
`
	indexCatalogTableQuery = `
CREATE INDEX IF NOT EXISTS catalog_last_updated_index ON zetasqlite_catalog(updatedAt DESC);
`

	upsertCatalogQuery = `
INSERT INTO zetasqlite_catalog (
  name,
  kind,
  spec,
  updatedAt,
  createdAt
) VALUES (
  @name,
  @kind,
  @spec,
  @updatedAt,
  @createdAt
) ON CONFLICT(name) DO UPDATE SET
  spec = @spec,
  updatedAt = @updatedAt
`
	deleteCatalogQuery = `
DELETE FROM zetasqlite_catalog WHERE name = @name
`
)

type CatalogSpecKind string

const (
	TableSpecKind    CatalogSpecKind = "table"
	ViewSpecKind     CatalogSpecKind = "view"
	FunctionSpecKind CatalogSpecKind = "function"
	catalogName                      = "zetasqlite"
)

type Catalog struct {
	db           *sql.DB
	lastSyncedAt time.Time
	mu           sync.RWMutex
	tables       []*catalogTable
	functions    []*catalogFunction
	tableMap     map[string]*catalogTable
	funcMap      map[string]*catalogFunction
	tableIndex   nameIndex[*types.SimpleTable]
	funcIndex    nameIndex[*types.Function]
	// catalog holds only the ZetaSQL builtin functions. Tables and user-defined functions are
	// resolved through the name indexes above instead, because the analyzer calls back into
	// this Go Catalog for name resolution and a ZetaSQL catalog can never be shrunk or freed.
	catalog *types.SimpleCatalog
}

// catalogTable pairs a spec with the ZetaSQL table built from it. The ZetaSQL table is C++
// memory that is never freed, so it is built once per schema and reused across lookups.
type catalogTable struct {
	spec  *TableSpec
	table *types.SimpleTable
}

type catalogFunction struct {
	spec *FunctionSpec
	fn   *types.Function
}

// zetaSQLCatalogsAllocated counts the ZetaSQL catalogs created by this process. Each one is
// C++ memory that go-zetasql never frees, so tests assert it stays flat as the catalog changes.
var zetaSQLCatalogsAllocated atomic.Int64

func newSimpleCatalog(name string) *types.SimpleCatalog {
	zetaSQLCatalogsAllocated.Add(1)
	catalog := types.NewSimpleCatalog(name)
	catalog.AddZetaSQLBuiltinFunctions(nil)
	return catalog
}

// nameIndex resolves the name paths the analyzer asks for. It stands in for nesting
// sub-catalogs inside the ZetaSQL catalog: a value registered under project.dataset.name is
// also reachable as dataset.name and as name, the first registration under a given name
// wins, and lookups are case-insensitive, all as SimpleCatalog behaves. Paths are keyed by
// their dot-joined form because a quoted identifier such as `project.dataset.name` reaches
// the catalog as a single path element.
type nameIndex[T any] map[string]T

func (i nameIndex[T]) add(namePath []string, v T) {
	for start := range namePath {
		key := indexKey(namePath[start:])
		if _, exists := i[key]; !exists {
			i[key] = v
		}
	}
}

func (i nameIndex[T]) find(path []string) (T, bool) {
	v, exists := i[indexKey(path)]
	return v, exists
}

func indexKey(path []string) string {
	return strings.ToLower(strings.Join(path, "."))
}

// These function definitions are missing from go-zetasql, either due to being on an older version
// or upstream issues (i.e. google/zetasql#135)
var MISSING_FUNCTIONS = []*FunctionSpec{
	// Add missing CONTAINS_SUBSTR function to the catalog
	// https://github.com/google/zetasql/issues/135#issuecomment-1490908494
	{
		IsTemp:   false,
		NamePath: []string{"contains_substr"},
		Language: "SQL",
		Args: []*NameWithType{
			{Name: "expression", Type: &Type{Name: "expression", Kind: types.STRING, SignatureKind: types.ArgTypeArbitrary}},
			{Name: "search_value_literal", Type: &Type{Name: "search_value_literal", Kind: types.STRING, SignatureKind: types.ArgTypeFixed}},
		},
		Return: &Type{Name: "BOOL", Kind: types.BOOL, SignatureKind: types.ArgTypeFixed},
	},
	{
		IsTemp:   false,
		NamePath: []string{"array_slice"},
		Language: "SQL",
		Args: []*NameWithType{
			{Name: "array_to_slice", Type: &Type{
				Name:          "array_to_slice",
				Kind:          types.ARRAY,
				SignatureKind: types.ArgArrayTypeAny1,
			}},
			{Name: "start_offset", Type: &Type{Name: "start_offset", Kind: types.INT64, SignatureKind: types.ArgTypeFixed}},
			{Name: "end_offset", Type: &Type{Name: "end_offset", Kind: types.INT64, SignatureKind: types.ArgTypeFixed}},
		},
		Return: &Type{Kind: types.ARRAY, SignatureKind: types.ArgArrayTypeAny1},
	},
	{
		IsTemp:   false,
		NamePath: []string{"array_first"},
		Language: "SQL",
		Args: []*NameWithType{
			{Name: "array", Type: &Type{Name: "array", Kind: types.ARRAY, SignatureKind: types.ArgArrayTypeAny1}},
		},
		Return: &Type{SignatureKind: types.ArgTypeAny1},
	},
	{
		IsTemp:   false,
		NamePath: []string{"array_last"},
		Language: "SQL",
		Args: []*NameWithType{
			{Name: "array", Type: &Type{Name: "array", Kind: types.ARRAY, SignatureKind: types.ArgArrayTypeAny1}},
		},
		Return: &Type{SignatureKind: types.ArgTypeAny1},
	},
	// MIN_BY and MAX_BY are aliases for ANY_VALUE(x HAVING MIN/MAX y)
	// https://github.com/goccy/bigquery-emulator/issues/388
	{
		IsTemp:      false,
		NamePath:    []string{"min_by"},
		Language:    "SQL",
		IsAggregate: true,
		Args: []*NameWithType{
			{Name: "x", Type: &Type{Name: "x", SignatureKind: types.ArgTypeAny1}},
			{Name: "y", Type: &Type{Name: "y", SignatureKind: types.ArgTypeAny2}},
		},
		Return: &Type{SignatureKind: types.ArgTypeAny1},
	},
	{
		IsTemp:      false,
		NamePath:    []string{"max_by"},
		Language:    "SQL",
		IsAggregate: true,
		Args: []*NameWithType{
			{Name: "x", Type: &Type{Name: "x", SignatureKind: types.ArgTypeAny1}},
			{Name: "y", Type: &Type{Name: "y", SignatureKind: types.ArgTypeAny2}},
		},
		Return: &Type{SignatureKind: types.ArgTypeAny1},
	},
}

func NewCatalog(db *sql.DB) (*Catalog, error) {
	catalog := &Catalog{
		db:         db,
		catalog:    newSimpleCatalog(catalogName),
		tableMap:   map[string]*catalogTable{},
		funcMap:    map[string]*catalogFunction{},
		tableIndex: nameIndex[*types.SimpleTable]{},
		funcIndex:  nameIndex[*types.Function]{},
	}

	for _, spec := range MISSING_FUNCTIONS {
		if err := catalog.addFunctionSpec(spec); err != nil {
			return nil, err
		}
	}

	return catalog, nil
}

func (c *Catalog) FullName() string {
	return c.catalog.FullName()
}

func (c *Catalog) FindTable(path []string) (types.Table, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.isWildcardTable(path) {
		return c.createWildcardTable(path)
	}
	if table, exists := c.tableIndex.find(path); exists {
		return table, nil
	}
	return nil, fmt.Errorf("table not found: %s", strings.Join(path, "."))
}

// tableSpecByName returns the spec registered under the given TableName, if any.
func (c *Catalog) tableSpecByName(name string) *TableSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if table, exists := c.tableMap[name]; exists {
		return table.spec
	}
	return nil
}

func (c *Catalog) FindModel(path []string) (types.Model, error) {
	return c.catalog.FindModel(path)
}

func (c *Catalog) FindConnection(path []string) (types.Connection, error) {
	return c.catalog.FindConnection(path)
}

func (c *Catalog) FindFunction(path []string) (*types.Function, error) {
	// Builtins take precedence over user-defined functions of the same name.
	if fn, err := c.catalog.FindFunction(path); err == nil {
		return fn, nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if fn, exists := c.funcIndex.find(path); exists {
		return fn, nil
	}
	return nil, fmt.Errorf("function not found: %s", strings.Join(path, "."))
}

func (c *Catalog) FindTableValuedFunction(path []string) (types.TableValuedFunction, error) {
	return c.catalog.FindTableValuedFunction(path)
}

func (c *Catalog) FindProcedure(path []string) (*types.Procedure, error) {
	return c.catalog.FindProcedure(path)
}

func (c *Catalog) FindType(path []string) (types.Type, error) {
	return c.catalog.FindType(path)
}

func (c *Catalog) FindConstant(path []string) (types.Constant, int, error) {
	return c.catalog.FindConstant(path)
}

func (c *Catalog) FindConversion(from, to types.Type) (types.Conversion, error) {
	return c.catalog.FindConversion(from, to)
}

func (c *Catalog) ExtendedTypeSuperTypes(typ types.Type) (*types.TypeListView, error) {
	return c.catalog.ExtendedTypeSuperTypes(typ)
}

func (c *Catalog) SuggestTable(mistypedPath []string) string {
	return c.catalog.SuggestTable(mistypedPath)
}

func (c *Catalog) SuggestModel(mistypedPath []string) string {
	return c.catalog.SuggestModel(mistypedPath)
}

func (c *Catalog) SuggestFunction(mistypedPath []string) string {
	return c.catalog.SuggestFunction(mistypedPath)
}

func (c *Catalog) SuggestTableValuedFunction(mistypedPath []string) string {
	return c.catalog.SuggestTableValuedFunction(mistypedPath)
}

func (c *Catalog) SuggestConstant(mistypedPath []string) string {
	return c.catalog.SuggestConstant(mistypedPath)
}

func (c *Catalog) formatNamePath(path []string) string {
	return strings.Join(path, "_")
}

func (c *Catalog) getFunctions(namePath *NamePath) []*FunctionSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()

	specs := make([]*FunctionSpec, 0, len(c.functions))
	if namePath.empty() {
		for _, function := range c.functions {
			specs = append(specs, function.spec)
		}
		return specs
	}
	key := c.formatNamePath(namePath.path)
	for _, function := range c.functions {
		fn := function.spec
		if len(fn.NamePath) == 1 {
			// function name only
			specs = append(specs, fn)
			continue
		}
		pathPrefixKey := c.formatNamePath(c.trimmedLastPath(fn.NamePath))
		if strings.Contains(pathPrefixKey, key) {
			specs = append(specs, fn)
		}
	}
	return specs
}

func (c *Catalog) Sync(ctx context.Context, conn *Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.createCatalogTablesIfNotExists(ctx, conn); err != nil {
		return fmt.Errorf("failed to create catalog tables: %w", err)
	}
	now := time.Now()
	rows, err := conn.QueryContext(
		ctx,
		`SELECT name, kind, spec FROM zetasqlite_catalog WHERE updatedAt >= @lastUpdatedAt`,
		sql.Named("lastUpdatedAt", c.lastSyncedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to query load catalog: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name string
			kind CatalogSpecKind
			spec string
		)
		if err := rows.Scan(&name, &kind, &spec); err != nil {
			return fmt.Errorf("failed to scan catalog values: %w", err)
		}
		switch kind {
		case TableSpecKind, ViewSpecKind:
			if err := c.loadTableSpec(spec); err != nil {
				return fmt.Errorf("failed to load table spec: %w", err)
			}
		case FunctionSpecKind:
			if err := c.loadFunctionSpec(spec); err != nil {
				return fmt.Errorf("failed to load function spec: %w", err)
			}
		default:
			return fmt.Errorf("unknown catalog spec kind %s", kind)
		}
	}
	c.lastSyncedAt = now
	return nil
}

func (c *Catalog) AddNewTableSpec(ctx context.Context, conn *Conn, spec *TableSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.addTableSpec(spec); err != nil {
		return err
	}
	if !spec.IsTemp {
		if err := c.saveTableSpec(ctx, conn, spec); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) AddNewFunctionSpec(ctx context.Context, conn *Conn, spec *FunctionSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.addFunctionSpec(spec); err != nil {
		return err
	}
	if !spec.IsTemp {
		if err := c.saveFunctionSpec(ctx, conn, spec); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) DeleteTableSpec(ctx context.Context, conn *Conn, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.deleteTableSpecByName(name); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, deleteCatalogQuery, sql.Named("name", name)); err != nil {
		return err
	}
	return nil
}

func (c *Catalog) DeleteFunctionSpec(ctx context.Context, conn *Conn, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.deleteFunctionSpecByName(name); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, deleteCatalogQuery, sql.Named("name", name)); err != nil {
		return err
	}
	return nil
}

func (c *Catalog) deleteTableSpecByName(name string) error {
	if _, exists := c.tableMap[name]; !exists {
		return fmt.Errorf("failed to find table spec from map by %s", name)
	}
	delete(c.tableMap, name)
	c.tables = slices.DeleteFunc(c.tables, func(table *catalogTable) bool {
		return table.spec.TableName() == name
	})
	c.rebuildTableIndex()
	return nil
}

func (c *Catalog) deleteFunctionSpecByName(name string) error {
	if _, exists := c.funcMap[name]; !exists {
		return fmt.Errorf("failed to find function spec from map by %s", name)
	}
	delete(c.funcMap, name)
	c.functions = slices.DeleteFunc(c.functions, func(function *catalogFunction) bool {
		return function.spec.FuncName() == name
	})
	c.rebuildFunctionIndex()
	return nil
}

// rebuildTableIndex re-registers the live tables in creation order, so that a name
// previously shadowed by a dropped or replaced table resolves to the next one claiming it.
func (c *Catalog) rebuildTableIndex() {
	clear(c.tableIndex)
	for _, table := range c.tables {
		c.tableIndex.add(table.spec.NamePath, table.table)
	}
}

func (c *Catalog) rebuildFunctionIndex() {
	clear(c.funcIndex)
	for _, function := range c.functions {
		c.funcIndex.add(function.spec.NamePath, function.fn)
	}
}

func (c *Catalog) saveTableSpec(ctx context.Context, conn *Conn, spec *TableSpec) error {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to encode table spec: %w", err)
	}
	now := time.Now()
	kind := string(TableSpecKind)
	if spec.IsView {
		kind = string(ViewSpecKind)
	}
	if _, err := conn.ExecContext(
		ctx,
		upsertCatalogQuery,
		sql.Named("name", spec.TableName()),
		sql.Named("kind", kind),
		sql.Named("spec", string(encoded)),
		sql.Named("updatedAt", now),
		sql.Named("createdAt", now),
	); err != nil {
		return fmt.Errorf("failed to save a new table spec: %w", err)
	}
	return nil
}

func (c *Catalog) saveFunctionSpec(ctx context.Context, conn *Conn, spec *FunctionSpec) error {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to encode function spec: %w", err)
	}
	now := time.Now()
	if _, err := conn.ExecContext(
		ctx,
		upsertCatalogQuery,
		sql.Named("name", spec.FuncName()),
		sql.Named("kind", string(FunctionSpecKind)),
		sql.Named("spec", string(encoded)),
		sql.Named("updatedAt", now),
		sql.Named("createdAt", now),
	); err != nil {
		return fmt.Errorf("failed to save a new function spec: %w", err)
	}
	return nil
}

func (c *Catalog) createCatalogTablesIfNotExists(ctx context.Context, conn *Conn) error {
	if _, err := conn.ExecContext(ctx, createCatalogTableQuery); err != nil {
		return fmt.Errorf("failed to create catalog table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, indexCatalogTableQuery); err != nil {
		return fmt.Errorf("failed to index catalog table: %w", err)
	}
	return nil
}

func (c *Catalog) loadTableSpec(spec string) error {
	var v TableSpec
	if err := json.Unmarshal([]byte(spec), &v); err != nil {
		return fmt.Errorf("failed to decode table spec: %w", err)
	}
	if err := c.addTableSpec(&v); err != nil {
		return fmt.Errorf("failed to add table spec to catalog: %w", err)
	}
	return nil
}

func (c *Catalog) loadFunctionSpec(spec string) error {
	var v FunctionSpec
	if err := json.Unmarshal([]byte(spec), &v); err != nil {
		return fmt.Errorf("failed to decode function spec: %w", err)
	}
	if err := c.addFunctionSpec(&v); err != nil {
		return fmt.Errorf("failed to add function spec to catalog: %w", err)
	}
	return nil
}

func (c *Catalog) trimmedLastPath(path []string) []string {
	if len(path) == 0 {
		return path
	}
	return path[:len(path)-1]
}

func (c *Catalog) addFunctionSpec(spec *FunctionSpec) error {
	funcName := spec.FuncName()
	if function, exists := c.funcMap[funcName]; exists {
		return c.replaceFunctionSpec(function, spec)
	}
	fn, err := c.createFunction(spec)
	if err != nil {
		return err
	}
	function := &catalogFunction{spec: spec, fn: fn}
	c.functions = append(c.functions, function)
	c.funcMap[funcName] = function
	c.funcIndex.add(spec.NamePath, fn)
	return nil
}

func (c *Catalog) addTableSpec(spec *TableSpec) error {
	tableName := spec.TableName()
	if table, exists := c.tableMap[tableName]; exists {
		return c.replaceTableSpec(table, spec)
	}
	zetaTable, err := c.createSimpleTable(spec)
	if err != nil {
		return err
	}
	table := &catalogTable{spec: spec, table: zetaTable}
	c.tables = append(c.tables, table)
	c.tableMap[tableName] = table
	c.tableIndex.add(spec.NamePath, zetaTable)
	return nil
}

// replaceTableSpec updates a table that is re-added under its existing name, as
// CREATE OR REPLACE TABLE and the catalog sync do. The ZetaSQL table is only rebuilt when the
// schema changed, since every one built is C++ memory that stays allocated.
func (c *Catalog) replaceTableSpec(table *catalogTable, spec *TableSpec) error {
	sameSchema := table.spec.sameSchema(spec)
	table.spec = spec
	if sameSchema {
		return nil
	}
	zetaTable, err := c.createSimpleTable(spec)
	if err != nil {
		return err
	}
	table.table = zetaTable
	c.rebuildTableIndex()
	return nil
}

// replaceFunctionSpec is the function counterpart of replaceTableSpec: the ZetaSQL function is
// only rebuilt when its signature changed.
func (c *Catalog) replaceFunctionSpec(function *catalogFunction, spec *FunctionSpec) error {
	sameSignature := function.spec.sameSignature(spec)
	function.spec = spec
	if sameSignature {
		return nil
	}
	fn, err := c.createFunction(spec)
	if err != nil {
		return err
	}
	function.fn = fn
	c.rebuildFunctionIndex()
	return nil
}

func (c *Catalog) createSimpleTable(spec *TableSpec) (*types.SimpleTable, error) {
	if len(spec.NamePath) == 0 {
		return nil, fmt.Errorf("table name is not found")
	}
	tableName := strings.Join(spec.NamePath, ".")
	columns := []types.Column{}
	for _, column := range spec.Columns {
		typ, err := column.Type.ToZetaSQLType()
		if err != nil {
			return nil, err
		}
		columns = append(columns, types.NewSimpleColumn(
			tableName, column.Name, typ,
		))
	}
	return types.NewSimpleTable(tableName, columns), nil
}

func (c *Catalog) createFunction(spec *FunctionSpec) (*types.Function, error) {
	if len(spec.NamePath) == 0 {
		return nil, fmt.Errorf("function name is not found")
	}
	funcName := spec.NamePath[len(spec.NamePath)-1]
	argTypes := []*types.FunctionArgumentType{}
	for _, arg := range spec.Args {
		argType, err := arg.FunctionArgumentType()
		if err != nil {
			return nil, err
		}
		argTypes = append(argTypes, argType)
	}
	retType, err := spec.Return.FunctionArgumentType()
	if err != nil {
		return nil, err
	}
	sig := types.NewFunctionSignature(retType, argTypes)

	// Use AggregateMode for aggregate functions, ScalarMode otherwise
	mode := types.ScalarMode
	if spec.IsAggregate {
		mode = types.AggregateMode
	}

	return types.NewFunction([]string{funcName}, "", mode, []*types.FunctionSignature{sig}), nil
}
