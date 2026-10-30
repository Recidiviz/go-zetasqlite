package internal

import (
	"context"
	"database/sql"
	"testing"

	"github.com/goccy/go-zetasql/types"
	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"
)

type testCatalog struct {
	*Catalog
	conn *Conn
}

func newTestCatalog(t *testing.T) *testCatalog {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sqlConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlConn.Close() })
	catalog, err := NewCatalog(db)
	if err != nil {
		t.Fatal(err)
	}
	conn := NewConn(sqlConn, nil)
	if err := catalog.Sync(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return &testCatalog{Catalog: catalog, conn: conn}
}

func (c *testCatalog) createTable(t *testing.T, namePath []string, columns ...string) *TableSpec {
	t.Helper()
	spec := newTestTableSpec(namePath, columns...)
	if err := c.AddNewTableSpec(context.Background(), c.conn, spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func (c *testCatalog) dropTable(t *testing.T, namePath []string) {
	t.Helper()
	if err := c.DeleteTableSpec(context.Background(), c.conn, formatPath(namePath)); err != nil {
		t.Fatal(err)
	}
}

func (c *testCatalog) createFunction(t *testing.T, namePath []string, args ...string) *FunctionSpec {
	t.Helper()
	spec := newTestFunctionSpec(namePath, args...)
	if err := c.AddNewFunctionSpec(context.Background(), c.conn, spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func (c *testCatalog) dropFunction(t *testing.T, namePath []string) {
	t.Helper()
	if err := c.DeleteFunctionSpec(context.Background(), c.conn, formatPath(namePath)); err != nil {
		t.Fatal(err)
	}
}

func newTestTableSpec(namePath []string, columns ...string) *TableSpec {
	spec := &TableSpec{NamePath: namePath}
	for _, column := range columns {
		spec.Columns = append(spec.Columns, &ColumnSpec{Name: column, Type: &Type{Kind: types.INT64}})
	}
	return spec
}

func newTestFunctionSpec(namePath []string, args ...string) *FunctionSpec {
	spec := &FunctionSpec{
		NamePath: namePath,
		Language: "SQL",
		Return:   &Type{Name: "INT64", Kind: types.INT64, SignatureKind: types.ArgTypeFixed},
	}
	for _, arg := range args {
		spec.Args = append(spec.Args, &NameWithType{
			Name: arg,
			Type: &Type{Name: arg, Kind: types.INT64, SignatureKind: types.ArgTypeFixed},
		})
	}
	return spec
}

func functionArgumentNames(t *testing.T, catalog *Catalog, path []string) []string {
	t.Helper()
	fn, err := catalog.FindFunction(path)
	if err != nil {
		t.Fatalf("FindFunction(%v): %v", path, err)
	}
	if fn.NumSignatures() != 1 {
		t.Fatalf("FindFunction(%v): expected one signature, got %d", path, fn.NumSignatures())
	}
	names := []string{}
	for _, arg := range fn.Signatures()[0].Arguments() {
		names = append(names, arg.ArgumentName())
	}
	return names
}

func columnNames(t *testing.T, table types.Table) []string {
	t.Helper()
	names := make([]string, 0, table.NumColumns())
	for i := 0; i < table.NumColumns(); i++ {
		names = append(names, table.Column(i).Name())
	}
	return names
}

func assertTableColumns(t *testing.T, catalog *Catalog, path []string, want []string) {
	t.Helper()
	table, err := catalog.FindTable(path)
	if err != nil {
		t.Fatalf("FindTable(%v): %v", path, err)
	}
	if diff := cmp.Diff(want, columnNames(t, table)); diff != "" {
		t.Errorf("FindTable(%v) columns (-want +got):\n%s", path, diff)
	}
}

func assertTableNotFound(t *testing.T, catalog *Catalog, path []string) {
	t.Helper()
	if table, err := catalog.FindTable(path); err == nil {
		t.Errorf("FindTable(%v): expected not found, got table %s", path, table.Name())
	}
}

func assertFunctionFound(t *testing.T, catalog *Catalog, path []string) {
	t.Helper()
	if _, err := catalog.FindFunction(path); err != nil {
		t.Errorf("FindFunction(%v): %v", path, err)
	}
}

func assertFunctionNotFound(t *testing.T, catalog *Catalog, path []string) {
	t.Helper()
	if _, err := catalog.FindFunction(path); err == nil {
		t.Errorf("FindFunction(%v): expected not found", path)
	}
}

// Every ZetaSQL catalog is C++ memory that go-zetasql never frees, and loading the builtin
// function set into one costs on the order of a megabyte. The catalog must therefore never
// allocate one in response to table or function changes.
func TestCatalog_TableChangesAllocateNoZetaSQLCatalogs(t *testing.T) {
	catalog := newTestCatalog(t)
	before := zetaSQLCatalogsAllocated.Load()

	for _, dataset := range []string{"ds1", "ds2", "ds3"} {
		catalog.createTable(t, []string{"proj", dataset, "a"}, "id")
		catalog.createTable(t, []string{"proj", dataset, "b"}, "id")
	}
	for _, dataset := range []string{"ds1", "ds2", "ds3"} {
		catalog.dropTable(t, []string{"proj", dataset, "a"})
	}
	catalog.createTable(t, []string{"proj", "ds1", "a"}, "id", "renamed")

	if got := zetaSQLCatalogsAllocated.Load() - before; got != 0 {
		t.Errorf("table changes allocated %d ZetaSQL catalogs, want 0", got)
	}
}

func TestCatalog_FunctionChangesAllocateNoZetaSQLCatalogs(t *testing.T) {
	catalog := newTestCatalog(t)
	before := zetaSQLCatalogsAllocated.Load()

	catalog.createFunction(t, []string{"proj", "ds1", "f"}, "x")
	catalog.createFunction(t, []string{"proj", "ds2", "f"}, "x")
	catalog.dropFunction(t, []string{"proj", "ds1", "f"})

	if got := zetaSQLCatalogsAllocated.Load() - before; got != 0 {
		t.Errorf("function changes allocated %d ZetaSQL catalogs, want 0", got)
	}
}

func TestCatalog_DroppedTableIsNotFound(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createTable(t, []string{"proj", "ds1", "t"}, "from_ds1")
	catalog.createTable(t, []string{"proj", "ds2", "t"}, "from_ds2")
	catalog.createTable(t, []string{"proj", "ds1", "t_kept"}, "kept_id")

	catalog.dropTable(t, []string{"proj", "ds1", "t"})

	assertTableNotFound(t, catalog.Catalog, []string{"proj", "ds1", "t"})
	assertTableNotFound(t, catalog.Catalog, []string{"proj.ds1.t"})
	assertTableNotFound(t, catalog.Catalog, []string{"ds1", "t"})
	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds2", "t"}, []string{"from_ds2"})
	assertTableColumns(t, catalog.Catalog, []string{"ds2.t"}, []string{"from_ds2"})
	// An unqualified name now resolves to the only remaining table with that name.
	assertTableColumns(t, catalog.Catalog, []string{"t"}, []string{"from_ds2"})
	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds1", "t_kept"}, []string{"kept_id"})
	// Wildcard tables are built from the live specs, so the dropped table must not contribute.
	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds1", "t*"}, []string{"kept_id", tableSuffixColumnName})
}

func TestCatalog_RecreatedTableResolvesNewSchema(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createTable(t, []string{"proj", "ds", "t"}, "old_column")
	catalog.dropTable(t, []string{"proj", "ds", "t"})

	catalog.createTable(t, []string{"proj", "ds", "t"}, "new_column")

	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds", "t"}, []string{"new_column"})
	assertTableColumns(t, catalog.Catalog, []string{"t"}, []string{"new_column"})
}

// CREATE OR REPLACE TABLE re-adds a spec under an existing name rather than dropping it first.
func TestCatalog_ReplacedTableResolvesNewSchema(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createTable(t, []string{"proj", "ds", "t"}, "old_column")

	catalog.createTable(t, []string{"proj", "ds", "t"}, "new_column")

	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds", "t"}, []string{"new_column"})
}

func TestCatalog_TableLookupIsCaseInsensitive(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createTable(t, []string{"Proj", "DS", "MyTable"}, "id")

	assertTableColumns(t, catalog.Catalog, []string{"proj", "ds", "mytable"}, []string{"id"})
	assertTableColumns(t, catalog.Catalog, []string{"PROJ.DS.MYTABLE"}, []string{"id"})
}

func TestCatalog_DroppedFunctionIsNotFound(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createFunction(t, []string{"proj", "ds1", "f"}, "x")
	catalog.createFunction(t, []string{"proj", "ds2", "g"}, "x")

	catalog.dropFunction(t, []string{"proj", "ds1", "f"})

	assertFunctionNotFound(t, catalog.Catalog, []string{"proj", "ds1", "f"})
	assertFunctionNotFound(t, catalog.Catalog, []string{"ds1", "f"})
	assertFunctionNotFound(t, catalog.Catalog, []string{"f"})
	assertFunctionFound(t, catalog.Catalog, []string{"proj", "ds2", "g"})
	assertFunctionFound(t, catalog.Catalog, []string{"ds2", "g"})
	assertFunctionFound(t, catalog.Catalog, []string{"g"})
	// Builtins and the functions patched in by NewCatalog survive a drop.
	assertFunctionFound(t, catalog.Catalog, []string{"concat"})
	assertFunctionFound(t, catalog.Catalog, []string{"contains_substr"})
}

// CREATE OR REPLACE FUNCTION re-adds a spec under an existing name rather than dropping it first.
func TestCatalog_ReplacedFunctionResolvesNewSignature(t *testing.T) {
	catalog := newTestCatalog(t)
	catalog.createFunction(t, []string{"proj", "ds", "f"}, "x")

	catalog.createFunction(t, []string{"proj", "ds", "f"}, "x", "y")

	if diff := cmp.Diff([]string{"x", "y"}, functionArgumentNames(t, catalog.Catalog, []string{"proj", "ds", "f"})); diff != "" {
		t.Errorf("FindFunction arguments (-want +got):\n%s", diff)
	}
}
