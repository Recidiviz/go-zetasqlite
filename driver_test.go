package zetasqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/api/bigquery/v2"

	zetasqlite "github.com/goccy/go-zetasqlite"
	"github.com/google/go-cmp/cmp"
)

func TestDriver(t *testing.T) {
	db, err := sql.Open("zetasqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL,
  FirstName  STRING(1024),
  LastName   STRING(1024),
  SingerInfo BYTES(MAX)
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT Singers (SingerId, FirstName, LastName) VALUES (1, 'John', 'Titor')`); err != nil {
		t.Fatal(err)
	}
	row := db.QueryRow("SELECT SingerID, FirstName, LastName FROM Singers WHERE SingerId = @id", sql.Named("id", 1))
	if row.Err() != nil {
		t.Fatal(row.Err())
	}
	var (
		singerID  int64
		firstName string
		lastName  string
	)
	if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
		t.Fatal(err)
	}
	if singerID != 1 || firstName != "John" || lastName != "Titor" {
		t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
	}
	if _, err := db.Exec(`
CREATE VIEW IF NOT EXISTS SingerNames AS SELECT FirstName || ' ' || LastName AS Name FROM Singers`); err != nil {
		t.Fatal(err)
	}

	viewRow := db.QueryRow("SELECT Name FROM SingerNames LIMIT 1")
	if viewRow.Err() != nil {
		t.Fatal(viewRow.Err())
	}

	var name string

	if err := viewRow.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "John Titor" {
		t.Fatalf("failed to find view row")
	}

	result, err := db.Exec("DELETE FROM Singers WHERE SingerId = @id", sql.Named("id", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rowsAffected, err := result.RowsAffected(); err != nil {
		t.Fatal(err)
	} else if rowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got %d", rowsAffected)
	}

	_, err = db.Exec("DROP VIEW SingerNames")
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec("DROP TABLE Singers")
	if err != nil {
		t.Fatal(err)
	}
}

func configureParameters(conn *sql.Conn, parameters []*bigquery.QueryParameter) error {
	if err := conn.Raw(func(c interface{}) error {
		zetasqliteConn, ok := c.(*zetasqlite.ZetaSQLiteConn)
		if !ok {
			return fmt.Errorf("failed to get ZetaSQLiteConn from %T", c)
		}
		zetasqliteConn.SetQueryParameters(parameters)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to setup query parameters: %s", err)
	}
	return nil
}

func TestNamedParameters(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("zetasqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL,
  FirstName  STRING(1024),
  LastName   STRING(1024),
  SingerInfo BYTES(MAX)
)`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if _, err := conn.ExecContext(ctx, `INSERT Singers (SingerId, FirstName, LastName) VALUES (1, 'John', 'Titor')`); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Run("test multiple statements named params", func(t *testing.T) {
		err = configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "id",
				ParameterType: &bigquery.QueryParameterType{
					Type: "INT64",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "1",
				},
			},
			{
				Name: "name",
				ParameterType: &bigquery.QueryParameterType{
					Type: "STRING",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "John",
				},
			},
		})
		row := conn.QueryRowContext(ctx, "SELECT SingerID, FirstName, LastName FROM Singers WHERE SingerId = @id OR (@name is null OR FirstName = @name)", sql.Named("id", 1), sql.Named("name", "John"))
		if row.Err() != nil {
			t.Fatal(row.Err())
		}
		var (
			singerID  int64
			firstName string
			lastName  string
		)
		if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
			t.Fatal(err)
		}
		if singerID != 1 || firstName != "John" || lastName != "Titor" {
			t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
		}
	})

	t.Run("test array type", func(t *testing.T) {
		err = configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "names",
				ParameterType: &bigquery.QueryParameterType{
					Type: "ARRAY",
					ArrayType: &bigquery.QueryParameterType{
						Type: "STRING",
					},
				},
				ParameterValue: &bigquery.QueryParameterValue{
					ArrayValues: []*bigquery.QueryParameterValue{
						{Value: "John"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		row := conn.QueryRowContext(ctx, "SELECT SingerID, FirstName, LastName FROM Singers WHERE FirstName IN UNNEST(@names)", sql.Named("names", []string{
			"John",
		}))
		if row.Err() != nil {
			t.Fatal(row.Err())
		}
		var (
			singerID  int64
			firstName string
			lastName  string
		)
		if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
			t.Fatal(err)
		}
		if singerID != 1 || firstName != "John" || lastName != "Titor" {
			t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
		}
	})

	t.Run("test struct type", func(t *testing.T) {
		err = configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "names",
				ParameterType: &bigquery.QueryParameterType{
					Type: "STRUCT",
					StructTypes: []*bigquery.QueryParameterTypeStructTypes{
						{Name: "first", Type: &bigquery.QueryParameterType{Type: "STRING"}},
					},
				},
				ParameterValue: &bigquery.QueryParameterValue{
					StructValues: map[string]bigquery.QueryParameterValue{
						"first": {Value: "John"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		row := conn.QueryRowContext(ctx, "SELECT SingerID, FirstName, LastName FROM Singers WHERE FirstName = @names.first", sql.Named("names", map[string]string{
			"first": "John",
		}))
		if row.Err() != nil {
			t.Fatal(row.Err())
		}
		var (
			singerID  int64
			firstName string
			lastName  string
		)
		if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
			t.Fatal(err)
		}
		if singerID != 1 || firstName != "John" || lastName != "Titor" {
			t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
		}
	})

	t.Run("test parameter pollution type", func(t *testing.T) {
		param := "test_param"
		// re-using the same parameter name should with different types works across queries
		err = configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: param,
				ParameterType: &bigquery.QueryParameterType{
					Type: "STRUCT",
					StructTypes: []*bigquery.QueryParameterTypeStructTypes{
						{Name: "first", Type: &bigquery.QueryParameterType{Type: "STRING"}},
					},
				},
				ParameterValue: &bigquery.QueryParameterValue{
					StructValues: map[string]bigquery.QueryParameterValue{
						"first": {Value: "John"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		row := conn.QueryRowContext(ctx, "SELECT SingerID, FirstName, LastName FROM Singers WHERE FirstName = @test_param.first", sql.Named("test_param", map[string]string{
			"first": "John",
		}))
		if row.Err() != nil {
			t.Fatal(row.Err())
		}
		var (
			singerID  int64
			firstName string
			lastName  string
		)
		if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
			t.Fatal(err)
		}
		if singerID != 1 || firstName != "John" || lastName != "Titor" {
			t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
		}
		err = configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: param,
				ParameterType: &bigquery.QueryParameterType{
					Type: "STRING",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "John",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		row = conn.QueryRowContext(ctx, "SELECT SingerID, FirstName, LastName FROM Singers WHERE FirstName = @test_param", sql.Named("test_param", "John"))
		if row.Err() != nil {
			t.Fatal(row.Err())
		}
		if err := row.Scan(&singerID, &firstName, &lastName); err != nil {
			t.Fatal(err)
		}
		if singerID != 1 || firstName != "John" || lastName != "Titor" {
			t.Fatalf("failed to find row %v %v %v", singerID, firstName, lastName)
		}
	})
}

func TestRegisterCustomDriver(t *testing.T) {
	sql.Register("zetasqlite-custom", &zetasqlite.ZetaSQLiteDriver{
		ConnectHook: func(conn *zetasqlite.ZetaSQLiteConn) error {
			return conn.SetNamePath([]string{"project-id", "datasetID"})
		},
	})
	db, err := sql.Open("zetasqlite-custom", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tableID (Id INT64 NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT `project-id`.datasetID.tableID (Id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	row := db.QueryRow("SELECT * FROM project-id.datasetID.tableID WHERE Id = ?", 1)
	if row.Err() != nil {
		t.Fatal(row.Err())
	}
	var id int64
	if err := row.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("failed to find row %v", id)
	}
}

func TestChangedCatalog(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		result, err := db.Exec(`
CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL,
  FirstName  STRING(1024),
  LastName   STRING(1024),
  SingerInfo BYTES(MAX)
)`)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`DROP TABLE Singers`)
		if err != nil {
			t.Fatal(err)
		}
		resultCatalog, err := zetasqlite.ChangedCatalogFromResult(result)
		if err != nil {
			t.Fatal(err)
		}
		if !resultCatalog.Changed() {
			t.Fatal("failed to get changed catalog")
		}
		if len(resultCatalog.Table.Added) != 1 {
			t.Fatal("failed to get created table spec")
		}
		if diff := cmp.Diff(resultCatalog.Table.Added[0].NamePath, []string{"Singers"}); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
		rowsCatalog, err := zetasqlite.ChangedCatalogFromRows(rows)
		if err != nil {
			t.Fatal(err)
		}
		if !rowsCatalog.Changed() {
			t.Fatal("failed to get changed catalog")
		}
		if len(rowsCatalog.Table.Deleted) != 1 {
			t.Fatal("failed to get deleted table spec")
		}
		if diff := cmp.Diff(rowsCatalog.Table.Deleted[0].NamePath, []string{"Singers"}); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	})
	t.Run("function", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		result, err := db.ExecContext(context.Background(), `CREATE FUNCTION ANY_ADD(x ANY TYPE, y ANY TYPE) AS ((x + 4) / y)`)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(context.Background(), `DROP FUNCTION ANY_ADD`)
		if err != nil {
			t.Fatal(err)
		}
		resultCatalog, err := zetasqlite.ChangedCatalogFromResult(result)
		if err != nil {
			t.Fatal(err)
		}
		if !resultCatalog.Changed() {
			t.Fatal("failed to get changed catalog")
		}
		if len(resultCatalog.Function.Added) != 1 {
			t.Fatal("failed to get created function spec")
		}
		if diff := cmp.Diff(resultCatalog.Function.Added[0].NamePath, []string{"ANY_ADD"}); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
		rowsCatalog, err := zetasqlite.ChangedCatalogFromRows(rows)
		if err != nil {
			t.Fatal(err)
		}
		if !rowsCatalog.Changed() {
			t.Fatal("failed to get changed catalog")
		}
		if len(rowsCatalog.Function.Deleted) != 1 {
			t.Fatal("failed to get deleted function spec")
		}
		if diff := cmp.Diff(rowsCatalog.Function.Deleted[0].NamePath, []string{"ANY_ADD"}); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	})
}

func TestCreateTable(t *testing.T) {
	t.Run("primary keys", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL PRIMARY KEY,
  FirstName  STRING(1024),
  LastName   STRING(1024)
)`); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.Prepare("INSERT Singers (SingerId, FirstName, LastName) VALUES (@SingerID, @FirstName, @LastName)")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.Exec(sql.Named("SingerID", int64(1)), sql.Named("FirstName", "Kylie"), sql.Named("LastName", "Minogue"))
		if err != nil {
			t.Fatal(err)
		}

		_, err = stmt.Exec(sql.Named("SingerID", int64(1)), sql.Named("FirstName", "Miss"), sql.Named("LastName", "Kitten"))
		if !strings.Contains(err.Error(), "UNIQUE constraint failed: Singers.SingerId") {
			t.Fatalf("expected failed unique constraint err, got: %s", err)
		}
	})

	t.Run("create table/view in dataset (with hyphens)", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Raw(func(c interface{}) error {
			zetasqliteConn, ok := c.(*zetasqlite.ZetaSQLiteConn)
			if !ok {
				return fmt.Errorf("failed to get ZetaSQLiteConn from %T", c)
			}
			if err := zetasqliteConn.SetNamePath([]string{"project-hyphens", "dataset-with-hyphens"}); err != nil {
				return err
			}
			const maxNamePath = 3 // projectID and datasetID and tableID
			zetasqliteConn.SetMaxNamePath(maxNamePath)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL PRIMARY KEY,
  FirstName  STRING(1024),
  LastName   STRING(1024)
)`); err != nil {
			t.Fatal(err)
		}

		if _, err := conn.ExecContext(ctx, `CREATE VIEW IF NOT EXISTS SingerNames AS (SELECT FirstName FROM Singers)`); err != nil {
			t.Fatal(err)
		}

		if _, err := conn.QueryContext(ctx, "SELECT * FROM `project-hyphens`.`dataset-with-hyphens`.`SingerNames`"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPreparedStatements(t *testing.T) {
	t.Run("prepared select", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS Singers (
  SingerId   INT64 NOT NULL,
  FirstName  STRING(1024),
  LastName   STRING(1024),
  SingerInfo BYTES(MAX)
)`); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.Prepare("SELECT * FROM Singers WHERE SingerId = ?")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query("123")
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("found unexpected row; expected no rows")
		}
	})
	t.Run("prepared insert with named values", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Items (ItemId   INT64 NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT `Items` (`ItemId`) VALUES (123)"); err != nil {
			t.Fatal(err)
		}

		// Test that executing without args fails
		_, err = db.Exec("INSERT `Items` (`ItemId`) VALUES (?)")
		if err == nil {
			t.Fatal("expected error when inserting without args; got no error")
		}

		stmt, err := db.Prepare("INSERT `Items` (`ItemId`) VALUES (@itemID)")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.Exec(sql.Named("itemID", 456)); err != nil {
			t.Fatal(err)
		}

		stmt, err = db.PrepareContext(context.Background(), "INSERT `Items` (`ItemId`) VALUES (?)")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.Exec(456); err != nil {
			t.Fatal(err)
		}

		rows, err := db.Query("SELECT * FROM Items WHERE ItemId = 456")
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("expected no rows; expected one row")
		}
	})

	t.Run("prepared select with named values, formatting disabled, uppercased parameter", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		ctx := zetasqlite.WithQueryFormattingDisabled(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Items (ItemId   INT64 NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT `Items` (`ItemId`) VALUES (123)"); err != nil {
			t.Fatal(err)
		}

		stmt, err := db.PrepareContext(ctx, "SELECT `ItemID` FROM `Items` WHERE `ItemID` = @itemID AND @bool = TRUE")
		if err != nil {
			t.Fatal("unexpected error when preparing stmt; got %w", err)
		}

		var itemID string
		err = stmt.QueryRowContext(ctx, sql.Named("itemID", 123), sql.Named("bool", true)).Scan(&itemID)
		if err != nil {
			t.Fatal("expected one row; got error %w", err)
		}
	})

	t.Run("update from", func(t *testing.T) {
		db, err := sql.Open("zetasqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS Items (ItemId   INT64 NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT `Items` (`ItemId`) VALUES (123)"); err != nil {
			t.Fatal(err)
		}

		if _, err = db.Exec("UPDATE `Items` SET `ItemID` = ID FROM (SELECT 456 AS ID) WHERE true"); err != nil {
			t.Fatal(err)
		}

		// Would-be ambiguous column referenced by table -- updates value to 789
		if _, err = db.Exec("UPDATE `Items` SET `ItemID` = joined.ItemID FROM (SELECT 789 AS ItemID) joined WHERE true"); err != nil {
			t.Fatal(err)
		}

		// Joined FROM -- updates value to 1578
		if _, err = db.Exec("UPDATE Items i SET ItemId = i.ItemId + d.ItemId FROM Items d WHERE True"); err != nil {
			t.Fatal(err)
		}

		// Unnest -- updates value from 1578 to 123
		if _, err = db.Exec("UPDATE Items i SET ItemId = d.new__ItemId FROM UNNEST([STRUCT(1578 AS ItemId, 123 AS new__ItemId)]) d WHERE i.ItemId = d.ItemId"); err != nil {
			t.Fatal(err)
		}

		rows, err := db.Query("SELECT * FROM Items WHERE ItemId = 123")
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("expected one row; got no rows")
		}
	})
}

func TestQueryParametersWithAllowUndeclaredReset(t *testing.T) {
	// This test verifies that SetAllowUndeclaredParameters is properly managed:
	// - When explicit parameters are provided, undeclared params must be disabled
	// - When no parameters are provided, undeclared params should be allowed again
	ctx := context.Background()
	db, err := sql.Open("zetasqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create test table
	if _, err := db.Exec(`
CREATE TABLE TestTable (
  id INT64,
  name STRING(100),
  value INT64
)`); err != nil {
		t.Fatal(err)
	}

	// Insert test data
	if _, err := db.Exec(`INSERT TestTable (id, name, value) VALUES (1, 'Alice', 100), (2, 'Bob', 200)`); err != nil {
		t.Fatal(err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	t.Run("positional parameters with explicit types", func(t *testing.T) {
		// Configure positional parameters via BigQuery API
		err := configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "", // Empty name means positional
				ParameterType: &bigquery.QueryParameterType{
					Type: "INT64",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "1",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Execute query with positional parameter
		var name string
		row := conn.QueryRowContext(ctx, "SELECT name FROM TestTable WHERE id = ?", 1)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("failed to query with positional parameter: %v", err)
		}
		if name != "Alice" {
			t.Fatalf("expected 'Alice', got '%s'", name)
		}
	})

	t.Run("undeclared parameters work after explicit parameters", func(t *testing.T) {
		// This should work because each query gets a fresh AnalyzerOptions
		// with SetAllowUndeclaredParameters(true) by default
		var name string
		row := conn.QueryRowContext(ctx, "SELECT name FROM TestTable WHERE id = @id", sql.Named("id", 2))
		if err := row.Scan(&name); err != nil {
			t.Fatalf("failed to query with undeclared parameter after explicit parameters: %v", err)
		}
		if name != "Bob" {
			t.Fatalf("expected 'Bob', got '%s'", name)
		}
	})

	t.Run("named parameters with explicit types", func(t *testing.T) {
		// Configure named parameters via BigQuery API
		err := configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "searchId",
				ParameterType: &bigquery.QueryParameterType{
					Type: "INT64",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "1",
				},
			},
			{
				Name: "searchName",
				ParameterType: &bigquery.QueryParameterType{
					Type: "STRING",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "Alice",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Execute query with named parameters
		var value int64
		row := conn.QueryRowContext(ctx,
			"SELECT value FROM TestTable WHERE id = @searchId AND name = @searchName",
			sql.Named("searchId", 1),
			sql.Named("searchName", "Alice"),
		)
		if err := row.Scan(&value); err != nil {
			t.Fatalf("failed to query with named parameters: %v", err)
		}
		if value != 100 {
			t.Fatalf("expected value 100, got %d", value)
		}
	})

	t.Run("undeclared parameters work again after named parameters", func(t *testing.T) {
		// Verify that undeclared parameters still work after using explicit named parameters
		var count int64
		row := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM TestTable WHERE value > @minValue", sql.Named("minValue", 50))
		if err := row.Scan(&count); err != nil {
			t.Fatalf("failed to query with undeclared parameter after named parameters: %v", err)
		}
		if count != 2 {
			t.Fatalf("expected count 2, got %d", count)
		}
	})

	t.Run("multiple positional parameters", func(t *testing.T) {
		// Configure multiple positional parameters
		err := configureParameters(conn, []*bigquery.QueryParameter{
			{
				Name: "",
				ParameterType: &bigquery.QueryParameterType{
					Type: "INT64",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "1",
				},
			},
			{
				Name: "",
				ParameterType: &bigquery.QueryParameterType{
					Type: "INT64",
				},
				ParameterValue: &bigquery.QueryParameterValue{
					Value: "2",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Execute query with multiple positional parameters
		var count int64
		row := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM TestTable WHERE id IN (?, ?)", 1, 2)
		if err := row.Scan(&count); err != nil {
			t.Fatalf("failed to query with multiple positional parameters: %v", err)
		}
		if count != 2 {
			t.Fatalf("expected count 2, got %d", count)
		}
	})
}
