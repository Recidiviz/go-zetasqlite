package zetasqlite

import (
	"context"
	"database/sql"
	"github.com/google/go-cmp/cmp"
	"math"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestCodec(t *testing.T) {
	os.Setenv("TZ", "UTC")
	now := time.Now()
	ctx := context.Background()
	ctx = WithCurrentTime(ctx, now)
	db, err := sql.Open("zetasqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	floatCmpOpt := cmp.Comparer(func(x, y float64) bool {
		if x == y {
			return true
		}
		delta := math.Abs(x - y)
		mean := math.Abs(x+y) / 2.0
		return delta/mean < 0.00001
	})
	for _, test := range []struct {
		name         string
		query        string
		args         []interface{}
		expectedRows [][]interface{}
		expectedErr  string
	}{

		// Regression test for https://github.com/goccy/go-zetasqlite/issues/191
		{
			name: "distinct union",
			query: `WITH toks AS (SELECT true AS x, 1 AS y)
					SELECT DISTINCT x, x as y FROM toks`,
			expectedRows: [][]interface{}{{true, true}},
		},
		{
			name: "with scan union all",
			query: `(WITH toks AS (SELECT 1 AS x) SELECT x FROM toks)
UNION ALL
(WITH toks2 AS (SELECT 2 AS x) SELECT x FROM toks2)`,
			expectedRows: [][]interface{}{{int64(1)}, {int64(2)}},
		},
		{
			name: "having with union all",
			query: `(WITH toks AS (SELECT 1 AS x) SELECT COUNT(x) AS total_rows FROM toks WHERE x > 0 HAVING total_rows >= 0)
UNION ALL
(WITH toks2 AS (SELECT 2 AS x) SELECT COUNT(x) AS total_rows FROM toks2 WHERE x > 0 HAVING total_rows >= 0)`,
			expectedRows: [][]interface{}{{int64(1)}, {int64(1)}},
		},
		// priority 2 operator
		{
			name:         "unary plus operator",
			query:        "SELECT +1",
			expectedRows: [][]interface{}{{int64(1)}},
		},
		{
			name:         "unary minus operator",
			query:        "SELECT -2",
			expectedRows: [][]interface{}{{int64(-2)}},
		},
		{
			name:         "bit not operator",
			query:        "SELECT ~1",
			expectedRows: [][]interface{}{{int64(-2)}},
		},
	} {
		test := test

		t.Run(test.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, test.query, test.args...)
			if err != nil {
				if test.expectedErr == "" {
					t.Fatal(err)
				} else {
					return
				}
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			columnNum := len(columns)
			args := []interface{}{}
			for i := 0; i < columnNum; i++ {
				var v interface{}
				args = append(args, &v)
			}
			rowNum := 0
			for rows.Next() {
				if err := rows.Scan(args...); err != nil {
					t.Fatal(err)
				}
				derefArgs := []interface{}{}
				for i := 0; i < len(args); i++ {
					value := reflect.ValueOf(args[i]).Elem().Interface()
					derefArgs = append(derefArgs, value)
				}
				if len(test.expectedRows) <= rowNum {
					t.Fatalf("unexpected row %v. expected row num %d but got next row", derefArgs, len(test.expectedRows))
				}
				expectedRow := test.expectedRows[rowNum]
				if len(derefArgs) != len(expectedRow) {
					t.Fatalf("failed to get columns. expected %d but got %d", len(expectedRow), len(derefArgs))
				}
				if diff := cmp.Diff(expectedRow, derefArgs, floatCmpOpt); diff != "" {
					t.Errorf("[%d]: (-want +got):\n%s", rowNum, diff)
				}
				rowNum++
			}
			rowsErr := rows.Err()
			if test.expectedErr != "" {
				if test.expectedErr != rowsErr.Error() {
					t.Fatalf("unexpected error message: expected [%s] but got [%s]", test.expectedErr, rowsErr.Error())
				}
			} else {
				if rowsErr != nil {
					t.Fatal(rowsErr)
				}
			}
			if len(test.expectedRows) != rowNum {
				t.Fatalf("failed to get rows. expected %d but got %d", len(test.expectedRows), rowNum)
			}
		})
	}
	os.Unsetenv("TZ")
}
