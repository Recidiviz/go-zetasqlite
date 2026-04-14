package internal

import (
	"fmt"
	"strings"

	ast "github.com/vantaboard/go-googlesql/resolved_ast"
)

// MergeStmtTransformer handles transformation of MERGE statement nodes from  to SQLite.
//
// In BigQuery/, MERGE statements provide a way to conditionally INSERT, UPDATE, or DELETE
// rows based on whether they match between a target table and a source table/query. Since SQLite
// doesn't have native MERGE support, this transformer converts MERGE statements into a series of
// SQLite statements that achieve equivalent behavior.
//
// The transformation strategy is:
// 1. Create a temporary table with a FULL OUTER JOIN of target and source tables
// 2. Generate conditional INSERT/UPDATE/DELETE statements based on WHEN clauses
// 3. Clean up the temporary table
//
// This maintains the same semantics as the original visitor pattern implementation while
// integrating with the new transformer architecture.
type MergeStmtTransformer struct {
	coordinator Coordinator // For recursive transformation of expressions and scans
}

// NewMergeStmtTransformer creates a new MERGE statement transformer
func NewMergeStmtTransformer(coordinator Coordinator) *MergeStmtTransformer {
	return &MergeStmtTransformer{
		coordinator: coordinator,
	}
}

// Transform converts MERGE statement data to a collection of SQL statements that simulate MERGE behavior
func (t *MergeStmtTransformer) Transform(data StatementData, ctx TransformContext) (SQLFragment, error) {
	if data.Type != StatementTypeMerge || data.Merge == nil {
		return nil, fmt.Errorf("expected MERGE statement data, got %v", data.Type)
	}

	mergeData := data.Merge

	// Transform target table scan
	targetTable, err := t.coordinator.TransformScan(*mergeData.TargetScan, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to transform merge target table: %w", err)
	}

	// Transform source table/query scan
	sourceTable, err := t.coordinator.TransformScan(*mergeData.SourceScan, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to transform merge source table: %w", err)
	}

	// Transform merge expression (join condition)
	mergeExpr, err := t.coordinator.TransformExpression(mergeData.MergeExpr, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to transform merge expression: %w", err)
	}

	// Validate merge expression (conjunction of = or IS NOT DISTINCT FROM column pairs)
	if err := t.validateMergeExpression(mergeData.MergeExpr); err != nil {
		return nil, fmt.Errorf("unsupported merge expression: %w", err)
	}

	columnPairs, err := t.extractMergeColumnPairs(mergeData, mergeData.MergeExpr, mergeData.TargetTable)
	if err != nil {
		return nil, fmt.Errorf("failed to extract merge columns: %w", err)
	}

	// Create temporary merged table with FULL OUTER JOIN
	createTableStmt, columnMapping, err := t.createMergedTableStatement("googlesqlite_merged_table", sourceTable, targetTable, mergeData.SourceScan, mergeData.TargetScan, mergeExpr, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create merged table statement: %w", err)
	}

	mergeKeys, err := buildMergeKeys(columnPairs, columnMapping)
	if err != nil {
		return nil, err
	}

	// Build the list of SQL statements
	var statements []string

	// 1. Create temporary merged table
	statements = append(statements, createTableStmt.String())

	// 2. Generate conditional statements based on WHEN clauses
	for _, whenClause := range mergeData.WhenClauses {
		stmt, err := t.transformWhenClause(
			whenClause, mergeData.TargetTable,
			sourceTable,
			mergeKeys,
			columnMapping,
			ctx,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to transform WHEN clause: %w", err)
		}
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}

	// 3. Drop temporary table
	statements = append(statements, "DROP TABLE googlesqlite_merged_table")

	// Create a compound statement fragment that represents all the statements
	return NewCompoundSQLFragment(statements), nil
}

// mergeColumnPair holds one source/target key column pair from the MERGE ON clause (before merged-table aliases).
type mergeColumnPair struct {
	sourceCol *ColumnData
	targetCol *ColumnData
}

// mergeKeyPair carries merged-table column aliases for one key pair.
type mergeKeyPair struct {
	sourceCol        *ColumnData
	targetCol        *ColumnData
	mergedSourceName string
	mergedTargetName string
}

// flattenMergeAnd unwraps nested googlesqlite_and into a flat list of comparison leaves.
func flattenMergeAnd(expr ExpressionData) []ExpressionData {
	if expr.Type != ExpressionTypeFunction || expr.Function == nil {
		return []ExpressionData{expr}
	}
	if expr.Function.Name != "googlesqlite_and" {
		return []ExpressionData{expr}
	}
	var out []ExpressionData
	for _, arg := range expr.Function.Arguments {
		out = append(out, flattenMergeAnd(arg)...)
	}
	return out
}

// validateMergeExpression ensures the merge ON clause is a conjunction of = or IS NOT DISTINCT FROM
// comparisons between source and target column references.
func (t *MergeStmtTransformer) validateMergeExpression(mergeExpr ExpressionData) error {
	leaves := flattenMergeAnd(mergeExpr)
	if len(leaves) == 0 {
		return fmt.Errorf("empty merge expression")
	}
	for i, leaf := range leaves {
		if err := validateMergeLeaf(leaf); err != nil {
			return fmt.Errorf("merge ON part %d: %w", i+1, err)
		}
	}
	return nil
}

func validateMergeLeaf(leaf ExpressionData) error {
	if leaf.Type != ExpressionTypeFunction || leaf.Function == nil {
		return fmt.Errorf("expected comparison, got %v", leaf.Type)
	}
	switch leaf.Function.Name {
	case "googlesqlite_equal", "googlesqlite_is_not_distinct_from":
	default:
		return fmt.Errorf("unsupported comparison %q (use = or IS NOT DISTINCT FROM between columns)", leaf.Function.Name)
	}
	if len(leaf.Function.Arguments) != 2 {
		return fmt.Errorf("expected 2 arguments, got %d", len(leaf.Function.Arguments))
	}
	for i, arg := range leaf.Function.Arguments {
		if arg.Type != ExpressionTypeColumn {
			return fmt.Errorf("expected column reference at position %d, got %v", i, arg.Type)
		}
	}
	return nil
}

// extractMergeColumnPairs extracts source/target column pairs from the MERGE ON expression tree.
func (t *MergeStmtTransformer) extractMergeColumnPairs(mergeData *MergeData, mergeExpr ExpressionData, targetTableName string) ([]mergeColumnPair, error) {
	leaves := flattenMergeAnd(mergeExpr)
	pairs := make([]mergeColumnPair, 0, len(leaves))
	seen := make(map[string]struct{})
	for _, leaf := range leaves {
		src, tgt, err := t.extractMergePairFromLeaf(mergeData, leaf, targetTableName)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d:%d", tgt.ID, src.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pairs = append(pairs, mergeColumnPair{sourceCol: src, targetCol: tgt})
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no merge key columns extracted")
	}
	return pairs, nil
}

func (t *MergeStmtTransformer) extractMergePairFromLeaf(mergeData *MergeData, leaf ExpressionData, targetTableName string) (*ColumnData, *ColumnData, error) {
	if leaf.Type != ExpressionTypeFunction || leaf.Function == nil {
		return nil, nil, fmt.Errorf("invalid merge ON leaf")
	}
	switch leaf.Function.Name {
	case "googlesqlite_equal", "googlesqlite_is_not_distinct_from":
	default:
		return nil, nil, fmt.Errorf("unsupported merge ON leaf %q", leaf.Function.Name)
	}
	args := leaf.Function.Arguments
	if len(args) != 2 {
		return nil, nil, fmt.Errorf("merge comparison must have exactly 2 arguments")
	}
	return t.disambiguateSourceTarget(mergeData, args[0], args[1], targetTableName)
}

func (t *MergeStmtTransformer) disambiguateSourceTarget(mergeData *MergeData, colA, colB ExpressionData, targetTableName string) (*ColumnData, *ColumnData, error) {
	if colA.Type != ExpressionTypeColumn || colA.Column == nil || colB.Type != ExpressionTypeColumn || colB.Column == nil {
		return nil, nil, fmt.Errorf("expected two column references")
	}
	idA := colA.Column.ColumnID
	idB := colB.Column.ColumnID

	inScan := func(s *ScanData, id int) bool { return s != nil && s.FindColumnByID(id) != nil }
	aInT, aInS := inScan(mergeData.TargetScan, idA), inScan(mergeData.SourceScan, idA)
	bInT, bInS := inScan(mergeData.TargetScan, idB), inScan(mergeData.SourceScan, idB)

	// Prefer scan membership: ON clause may use aliases (target/source) while TargetTable is a flattened name.
	if aInT && !aInS && bInS && !bInT {
		return mergeData.SourceScan.FindColumnByID(idB), mergeData.TargetScan.FindColumnByID(idA), nil
	}
	if bInT && !bInS && aInS && !aInT {
		return mergeData.SourceScan.FindColumnByID(idA), mergeData.TargetScan.FindColumnByID(idB), nil
	}

	// Legacy: resolved table name matches merge target table string
	if colA.Column.TableName == targetTableName {
		source := mergeData.SourceScan.FindColumnByID(idB)
		target := mergeData.TargetScan.FindColumnByID(idA)
		if source != nil && target != nil {
			return source, target, nil
		}
	}
	if colB.Column.TableName == targetTableName {
		source := mergeData.SourceScan.FindColumnByID(idA)
		target := mergeData.TargetScan.FindColumnByID(idB)
		if source != nil && target != nil {
			return source, target, nil
		}
	}

	return nil, nil, fmt.Errorf("could not determine source and target columns for %q and %q (target table %q)",
		colA.Column.ColumnName, colB.Column.ColumnName, targetTableName)
}

func buildMergeKeys(pairs []mergeColumnPair, columnMapping *ColumnMapping) ([]mergeKeyPair, error) {
	keys := make([]mergeKeyPair, 0, len(pairs))
	for _, p := range pairs {
		ms, ok := columnMapping.LookupName(p.sourceCol)
		if !ok {
			return nil, fmt.Errorf("failed to lookup merged source column name for %s", p.sourceCol.Name)
		}
		mt, ok := columnMapping.LookupName(p.targetCol)
		if !ok {
			return nil, fmt.Errorf("failed to lookup merged target column name for %s", p.targetCol.Name)
		}
		keys = append(keys, mergeKeyPair{
			sourceCol:        p.sourceCol,
			targetCol:        p.targetCol,
			mergedSourceName: ms,
			mergedTargetName: mt,
		})
	}
	return keys, nil
}

func chainAndExprs(parts []*SQLExpression) *SQLExpression {
	if len(parts) == 0 {
		return nil
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out = NewBinaryExpression(out, "AND", parts[i])
	}
	return out
}

// mergeWhenFilter builds the row filter on googlesqlite_merged_table for the given match type and key columns.
// Predicates correlate to the outer DML row by comparing merged_* aliases to bare target/source column names
// (e.g. `id`) so EXISTS subqueries in DELETE/UPDATE behave correctly.
func mergeWhenFilter(matchType ast.MatchType, keys []mergeKeyPair) (*SQLExpression, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("no merge keys")
	}
	var parts []*SQLExpression
	switch matchType {
	case ast.MatchTypeMatched:
		// Join succeeded: both sides match the outer row's key (same shape as legacy merged_source = k AND merged_target = k).
		for _, k := range keys {
			tn := k.targetCol.Name
			parts = append(parts,
				NewBinaryExpression(NewColumnExpression(k.mergedSourceName), "IS NOT DISTINCT FROM", NewColumnExpression(tn)),
				NewBinaryExpression(NewColumnExpression(k.mergedTargetName), "IS NOT DISTINCT FROM", NewColumnExpression(tn)),
			)
		}
	case ast.MatchTypeNotMatchedBySource:
		// Target-only row for this outer key: source side null, target side populated and equals outer key.
		for _, k := range keys {
			tn := k.targetCol.Name
			parts = append(parts,
				NewBinaryExpression(NewColumnExpression(k.mergedTargetName), "IS NOT", NewLiteralExpression("NULL")),
				NewBinaryExpression(NewColumnExpression(k.mergedSourceName), "IS", NewLiteralExpression("NULL")),
				NewBinaryExpression(NewColumnExpression(k.mergedTargetName), "IS NOT DISTINCT FROM", NewColumnExpression(tn)),
			)
		}
	case ast.MatchTypeNotMatchedByTarget:
		// Source-only row for this outer key: target side null, source side populated and equals outer source key.
		for _, k := range keys {
			sn := k.sourceCol.Name
			parts = append(parts,
				NewBinaryExpression(NewColumnExpression(k.mergedTargetName), "IS", NewLiteralExpression("NULL")),
				NewBinaryExpression(NewColumnExpression(k.mergedSourceName), "IS NOT", NewLiteralExpression("NULL")),
				NewBinaryExpression(NewColumnExpression(k.mergedSourceName), "IS NOT DISTINCT FROM", NewColumnExpression(sn)),
			)
		}
	default:
		return nil, fmt.Errorf("unsupported match type: %v", matchType)
	}
	return chainAndExprs(parts), nil
}

func (t *MergeStmtTransformer) applyMergedColumnAliases(sql string, columnMapping *ColumnMapping, ctx TransformContext) (string, error) {
	out := sql
	for column, mapping := range columnMapping.AllColumnMap {
		expr, err := t.coordinator.TransformExpression(ExpressionData{
			Type:   ExpressionTypeColumn,
			Column: &ColumnRefData{ColumnID: column.ID},
		}, ctx)
		if err != nil {
			return "", fmt.Errorf("failed to transform column for merge WHEN alias: %w", err)
		}
		out = strings.ReplaceAll(out, expr.String(), fmt.Sprintf("`%s`", mapping))
	}
	return out, nil
}

// transformWhenClause transforms a single WHEN clause into an appropriate SQL statement
func (t *MergeStmtTransformer) transformWhenClause(
	whenClause *MergeWhenClauseData,
	targetTableName string,
	sourceTable SQLFragment,
	mergeKeys []mergeKeyPair,
	columnMapping *ColumnMapping,
	ctx TransformContext,
) (string, error) {

	fromFilter, err := mergeWhenFilter(whenClause.MatchType, mergeKeys)
	if err != nil {
		return "", err
	}

	combinedWhere := fromFilter
	if whenClause.Condition != nil {
		condExpr, err := t.coordinator.TransformExpression(*whenClause.Condition, ctx)
		if err != nil {
			return "", fmt.Errorf("failed to transform WHEN condition: %w", err)
		}
		condStr, err := t.applyMergedColumnAliases(condExpr.String(), columnMapping, ctx)
		if err != nil {
			return "", err
		}
		combinedWhere = NewBinaryExpression(fromFilter, "AND", &SQLExpression{
			Type:  ExpressionTypeLiteral,
			Value: "(" + condStr + ")",
		})
	}

	// Create WHERE clause with existence check
	subq := NewSelectStatement()
	subq.FromClause = &FromItem{Type: FromItemTypeTable, TableName: "googlesqlite_merged_table"}
	subq.SelectList = []*SelectListItem{{Expression: NewLiteralExpression("1")}}
	subq.WhereClause = combinedWhere
	existsStmt := NewExistsExpression(subq)

	// Generate the appropriate statement based on action type
	switch whenClause.ActionType {
	case ast.ActionTypeInsert:
		return t.transformInsertAction(whenClause, targetTableName, sourceTable, existsStmt.String(), columnMapping, ctx)
	case ast.ActionTypeUpdate:
		return t.transformUpdateAction(whenClause, targetTableName, combinedWhere.String(), columnMapping, ctx)
	case ast.ActionTypeDelete:
		return (&DeleteStatement{
			Table:     &FromItem{TableName: targetTableName},
			WhereExpr: existsStmt,
		}).String(), nil
	default:
		return "", fmt.Errorf("unsupported action type: %v", whenClause.ActionType)
	}
}

// transformInsertAction transforms an INSERT action within a WHEN clause
func (t *MergeStmtTransformer) transformInsertAction(whenClause *MergeWhenClauseData, targetTableName string,
	sourceTable SQLFragment, whereStmt string, columnMapping *ColumnMapping, ctx TransformContext) (string, error) {

	values := make([]string, 0, len(whenClause.InsertValues))
	columns := make([]string, 0, len(whenClause.InsertColumns))
	for i, col := range whenClause.InsertColumns {
		// Format column names
		columns = append(columns, fmt.Sprintf("`%s`", col.Name))
		// Transform INSERT values
		value := whenClause.InsertValues[i]
		valueExpr, err := t.coordinator.TransformExpression(value, ctx)
		if err != nil {
			return "", fmt.Errorf("failed to transform insert value: %w", err)
		}

		valueExpr.Alias = col.Name
		values = append(values, valueExpr.String())
	}

	// Build INSERT statement
	return fmt.Sprintf(
		"INSERT INTO `%s` (%s) SELECT %s FROM %s",
		targetTableName,
		strings.Join(columns, ","),
		strings.Join(values, ","),
		sourceTable.String(),
	), nil
}

// transformUpdateAction transforms an UPDATE action within a WHEN clause
func (t *MergeStmtTransformer) transformUpdateAction(whenClause *MergeWhenClauseData, targetTableName, whereStmt string,
	columnMapping *ColumnMapping, ctx TransformContext) (string, error) {

	// Transform SET items
	setItems := make([]string, 0, len(whenClause.SetItems))
	for _, item := range whenClause.SetItems {
		valueExpr, err := t.coordinator.TransformExpression(item.Value, ctx)
		if err != nil {
			return "", fmt.Errorf("failed to transform update value: %w", err)
		}

		// Replace column references with mapped names from the temporary table
		valueString := valueExpr.String()
		for column, mapping := range columnMapping.AllColumnMap {
			expr, err := t.coordinator.TransformExpression(ExpressionData{
				Type:   ExpressionTypeColumn,
				Column: &ColumnRefData{ColumnID: column.ID},
			}, ctx)
			if err != nil {
				return "", fmt.Errorf("failed to transform update value: %w", err)
			}

			valueString = strings.ReplaceAll(valueString, expr.String(), fmt.Sprintf("`%s`", mapping))
		}

		setItems = append(setItems, fmt.Sprintf("`%s`= %s", item.Column, valueString))
	}

	// Build UPDATE statement
	return fmt.Sprintf(
		"UPDATE `%s` SET %s FROM googlesqlite_merged_table WHERE %s",
		targetTableName,
		strings.Join(setItems, ","),
		whereStmt,
	), nil
}

// CreateMergedTableStatement creates a CREATE TABLE AS SELECT statement using the merged table pattern
// for MERGE operations with distinct column naming. This generates the SQL pattern:
// CREATE TABLE tableName AS SELECT DISTINCT sourceCol1 AS merged_sourceCol1, targetCol1 AS merged_targetCol1, ... FROM (
//
//	SELECT * FROM sourceTable LEFT JOIN targetTable ON joinCondition
//	UNION ALL
//	SELECT * FROM targetTable LEFT JOIN sourceTable ON joinCondition
//
// )
// Returns the CreateTableStatement and a mapping of original -> new column names
func (t *MergeStmtTransformer) createMergedTableStatement(tableName string, sourceTable, targetTable *FromItem, sourceTableData, targetTableData *ScanData, joinCondition *SQLExpression, ctx TransformContext) (*CreateTableStatement, *ColumnMapping, error) {
	// Create distinct column mappings
	columnMapping := t.createColumnMapping(sourceTableData.ColumnList, targetTableData.ColumnList)

	// Create the inner subquery with LEFT JOIN and explicit column selection
	leftJoin, err := t.createJoinWithColumnMapping(&FromItem{
		Type: FromItemTypeJoin,
		Join: &JoinClause{
			Type:      JoinTypeLeft,
			Left:      sourceTable,
			Right:     targetTable,
			Condition: joinCondition,
		},
	}, columnMapping, ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create left join: %w", err)
	}

	rightJoin, err := t.createJoinWithColumnMapping(&FromItem{
		Type: FromItemTypeJoin,
		Join: &JoinClause{
			Type:      JoinTypeLeft,
			Left:      targetTable,
			Right:     sourceTable,
			Condition: joinCondition,
		},
	}, columnMapping, ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create right join: %w", err)
	}

	// Create the UNION ALL operation
	unionOperation := &SetOperation{
		Type:     "UNION",
		Modifier: "ALL",
		Items:    []*SelectStatement{leftJoin, rightJoin},
	}

	// Create the outer subquery with UNION ALL
	unionStatement := NewSelectStatement()
	unionStatement.SetOperation = unionOperation

	// Create the final SELECT DISTINCT * from the UNION subquery
	distinctQuery := &SelectStatement{
		SelectType: SelectTypeDistinct,
		SelectList: []*SelectListItem{
			{Expression: NewStarExpression()},
		},
		FromClause: NewSubqueryFromItem(unionStatement, "merged_union"),
	}

	// Create the CREATE TABLE AS SELECT statement
	return &CreateTableStatement{
		TableName: tableName,
		AsSelect:  distinctQuery,
	}, columnMapping, nil
}

// createColumnMapping creates distinct column names and mappings
func (t *MergeStmtTransformer) createColumnMapping(sourceColumns, targetColumns []*ColumnData) *ColumnMapping {
	mapping := &ColumnMapping{
		SourceColumnMap: make(map[*ColumnData]string),
		TargetColumnMap: make(map[*ColumnData]string),
		AllColumnMap:    make(map[*ColumnData]string),
	}

	usedNames := make(map[string]bool)

	// Process source columns
	for _, col := range sourceColumns {
		newName := fmt.Sprintf("merged_source_%s", col.Name)
		counter := 1
		originalNewName := newName

		// Ensure uniqueness
		for usedNames[newName] {
			newName = fmt.Sprintf("%s_%d", originalNewName, counter)
			counter++
		}

		usedNames[newName] = true
		mapping.SourceColumnMap[col] = newName
		mapping.AllColumnMap[col] = newName
	}

	// Process target columns
	for _, col := range targetColumns {
		newName := fmt.Sprintf("merged_target_%s", col.Name)
		counter := 1
		originalNewName := newName

		// Ensure uniqueness
		for usedNames[newName] {
			newName = fmt.Sprintf("%s_%d", originalNewName, counter)
			counter++
		}

		usedNames[newName] = true
		mapping.TargetColumnMap[col] = newName
		mapping.AllColumnMap[col] = newName
	}

	return mapping
}

// createJoinWithColumnMapping creates a SELECT statement with explicit column mapping for joins
func (t *MergeStmtTransformer) createJoinWithColumnMapping(joinFromItem *FromItem, mapping *ColumnMapping, ctx TransformContext) (*SelectStatement, error) {
	stmt := NewSelectStatement()
	stmt.FromClause = joinFromItem

	// Build explicit SELECT list with column mappings
	stmt.SelectList = []*SelectListItem{}

	// Add source columns
	for col, newName := range mapping.SourceColumnMap {
		exprData := ExpressionData{
			Type:   ExpressionTypeColumn,
			Column: &ColumnRefData{ColumnID: col.ID},
		}

		expr, err := t.coordinator.TransformExpression(exprData, ctx)
		if err != nil {
			return nil, err
		}

		stmt.SelectList = append(stmt.SelectList, &SelectListItem{
			Expression: expr,
			Alias:      newName,
		})
	}

	// Add target columns
	for col, newName := range mapping.TargetColumnMap {
		exprData := ExpressionData{
			Type:   ExpressionTypeColumn,
			Column: &ColumnRefData{ColumnID: col.ID},
		}

		expr, err := t.coordinator.TransformExpression(exprData, ctx)
		if err != nil {
			return nil, err
		}

		stmt.SelectList = append(stmt.SelectList, &SelectListItem{
			Expression: expr,
			Alias:      newName,
		})
	}

	return stmt, nil
}

// ColumnMapping represents the mapping between original and new column names
type ColumnMapping struct {
	SourceColumnMap map[*ColumnData]string // original column -> new column name for source table
	TargetColumnMap map[*ColumnData]string // original column -> new column name for target table
	AllColumnMap    map[*ColumnData]string // all original column  -> new column names
}

func (m ColumnMapping) LookupName(column *ColumnData) (string, bool) {
	name, found := m.AllColumnMap[column]
	return name, found
}
