package internal

import (
	"context"
	"fmt"
	"reflect"

	ast "github.com/goccy/go-zetasql/resolved_ast"
)

// QueryTransformFactory creates and configures the complete transformation pipeline
type QueryTransformFactory struct {
	config *TransformConfig
}

// NewQueryTransformFactory creates a new factory with the given configuration
func NewQueryTransformFactory(config *TransformConfig) *QueryTransformFactory {
	if config == nil {
		config = DefaultTransformConfig()
	}

	return &QueryTransformFactory{
		config: config,
	}
}

// CreateCoordinator creates a fully configured coordinator with all transformers registered
func (f *QueryTransformFactory) CreateCoordinator() Coordinator {
	extractor := NewNodeExtractor()
	coord := NewQueryCoordinator(extractor)

	// Set circular reference
	extractor.SetCoordinator(coord)

	// Register expression transformers
	f.registerExpressionTransformers(coord)

	// Register statement transformers
	f.registerStatementTransformers(coord)

	// Register scan transformers
	f.registerScanTransformers(coord)

	return coord
}

// registerExpressionTransformers registers all expression transformers
func (f *QueryTransformFactory) registerExpressionTransformers(coord *QueryCoordinator) {
	// Literal transformer
	literalTransformer := NewLiteralTransformer()
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.LiteralNode{}), literalTransformer)

	// FunctionCall call transformer
	functionTransformer := NewFunctionCallTransformer(coord)
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.FunctionCallNode{}), functionTransformer)

	// Cast transformer
	castTransformer := NewCastTransformer(coord)
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.CastNode{}), castTransformer)

	// Column reference transformer
	columnTransformer := NewColumnRefTransformer(coord)
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.ColumnRefNode{}), columnTransformer)

	// Aggregate function transformer
	//aggTransformer := NewAggregateFunctionTransformer(f.config.Function, coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.AggregateFunctionCallNode{}), aggTransformer)

	// Analytic function transformer
	//analyticTransformer := NewAnalyticFunctionTransformer(f.config.Function, coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.AnalyticFunctionCallNode{}), analyticTransformer)

	// Subquery expression transformer
	subqueryTransformer := NewSubqueryTransformer(coord)
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.SubqueryExprNode{}), subqueryTransformer)

	// Parameter transformer
	parameterTransformer := NewParameterTransformer()
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.ParameterNode{}), parameterTransformer)
	coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.ArgumentRefNode{}), parameterTransformer)

	// DML transformers
	//dmlDefaultTransformer := NewDMLDefaultTransformer()
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.DMLDefaultNode{}), dmlDefaultTransformer)

	//dmlValueTransformer := NewDMLValueTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.DMLValueNode{}), dmlValueTransformer)

	// Struct transformers
	//makeStructTransformer := NewMakeStructTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.MakeStructNode{}), makeStructTransformer)

	//getStructFieldTransformer := NewGetStructFieldTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.GetStructFieldNode{}), getStructFieldTransformer)

	// JSON transformers
	//getJsonFieldTransformer := NewGetJsonFieldTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.GetJsonFieldNode{}), getJsonFieldTransformer)

	// Computed/Output column transformers
	//computedColumnTransformer := NewComputedColumnTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.ComputedColumnNode{}), computedColumnTransformer)

	//outputColumnTransformer := NewOutputColumnTransformer(coord)
	//coord.RegisterExpressionTransformer(reflect.TypeOf(&ast.OutputColumnNode{}), outputColumnTransformer)
}

// registerStatementTransformers registers all statement transformers
func (f *QueryTransformFactory) registerStatementTransformers(coord *QueryCoordinator) {
	// Query statement transformer
	queryTransformer := NewQueryStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.QueryStmtNode{}), queryTransformer)

	// TODO:
	// SELECT statement transformer
	//selectTransformer := NewSelectTransformer(f.config.SQL, coord)
	//coord.RegisterStatementTransformer(reflect.TypeOf(&ast.SelectStatement{}), selectTransformer)

	// DDL transformers
	//createTableTransformer := NewCreateTableTransformer(f.config.SQL, coord)
	//coord.RegisterStatementTransformer(reflect.TypeOf(&ast.CreateTableStmtNode{}), createTableTransformer)

	createTableAsSelectTransformer := NewCreateTableAsSelectStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.CreateTableAsSelectStmtNode{}), createTableAsSelectTransformer)

	//createFunctionTransformer := NewCreateFunctionTransformer(f.config.SQL, coord)
	//coord.RegisterStatementTransformer(reflect.TypeOf(&ast.CreateFunctionStmtNode{}), createFunctionTransformer)

	createViewTransformer := NewCreateViewStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.CreateViewStmtNode{}), createViewTransformer)

	dropTransformer := NewDropStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.DropStmtNode{}), dropTransformer)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.DropFunctionStmtNode{}), dropTransformer)

	// DML transformers
	dmlTransformer := NewDMLStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.InsertStmtNode{}), dmlTransformer)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.UpdateStmtNode{}), dmlTransformer)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.DeleteStmtNode{}), dmlTransformer)

	// MERGE transformer
	mergeTransformer := NewMergeStmtTransformer(coord)
	coord.RegisterStatementTransformer(reflect.TypeOf(&ast.MergeStmtNode{}), mergeTransformer)

	// Other statement transformers
	//truncateTransformer := NewTruncateTransformer(f.config.SQL)
	//coord.RegisterStatementTransformer(reflect.TypeOf(&ast.TruncateStmtNode{}), truncateTransformer)
}

// registerScanTransformers registers all scan transformers
func (f *QueryTransformFactory) registerScanTransformers(coord *QueryCoordinator) {
	// Table scan transformer
	tableScanTransformer := NewTableScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.TableScanNode{}), tableScanTransformer)

	// Join scan transformer
	joinScanTransformer := NewJoinScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.JoinScanNode{}), joinScanTransformer)

	// Filter scan transformer
	filterScanTransformer := NewFilterScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.FilterScanNode{}), filterScanTransformer)

	// Project scan transformer
	projectScanTransformer := NewProjectScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.ProjectScanNode{}), projectScanTransformer)

	// Aggregate scan transformer
	aggregateScanTransformer := NewAggregateScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.AggregateScanNode{}), aggregateScanTransformer)

	// Order by scan transformer
	orderByScanTransformer := NewOrderByScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.OrderByScanNode{}), orderByScanTransformer)

	// Limit scan transformer
	limitScanTransformer := NewLimitScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.LimitOffsetScanNode{}), limitScanTransformer)

	// Set operation scan transformer
	setOpScanTransformer := NewSetOperationScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.SetOperationScanNode{}), setOpScanTransformer)

	// Single row scan transformer
	singleRowScanTransformer := NewSingleRowScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.SingleRowScanNode{}), singleRowScanTransformer)

	// With scan transformer
	withScanTransformer := NewWithScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.WithScanNode{}), withScanTransformer)

	withRefScanTransformer := NewWithRefScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.WithRefScanNode{}), withRefScanTransformer)

	// Array scan transformer (for UNNEST operations)
	arrayScanTransformer := NewArrayScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.ArrayScanNode{}), arrayScanTransformer)

	// Analytic scan transformer (for windowed function selects)
	analyticScanTransformer := NewAnalyticScanTransformer(coord)
	coord.RegisterScanTransformer(reflect.TypeOf(&ast.AnalyticScanNode{}), analyticScanTransformer)

}

// CreateTransformContext creates a transform context with the factory's configuration
func (f *QueryTransformFactory) CreateTransformContext(ctx context.Context) TransformContext {
	return NewDefaultTransformContext(ctx, f.config)
}

// TransformQuery is a convenience method that transforms a complete querybuilder
func (f *QueryTransformFactory) TransformQuery(ctx context.Context, queryNode ast.Node) (*TransformResult, error) {
	coordinator := f.CreateCoordinator()
	transformCtx := f.CreateTransformContext(ctx)

	// Transform the querybuilder
	fragment, err := coordinator.TransformStatementNode(queryNode, transformCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to transform querybuilder: %w", err)
	}

	result := NewTransformResult(fragment)

	return result, nil
}

// GetRegisteredTransformers returns information about registered transformers
func (f *QueryTransformFactory) GetRegisteredTransformers() map[string][]string {
	coord := f.CreateCoordinator()
	queryCoord := coord.(*QueryCoordinator)

	result := make(map[string][]string)

	// Expression transformers
	exprTypes := queryCoord.GetRegisteredExpressionTypes()
	exprNames := make([]string, len(exprTypes))
	for i, t := range exprTypes {
		exprNames[i] = t
	}
	result["expressions"] = exprNames

	// Statement transformers
	stmtTypes := queryCoord.GetRegisteredStatementTypes()
	stmtNames := make([]string, len(stmtTypes))
	for i, t := range stmtTypes {
		stmtNames[i] = t
	}
	result["statements"] = stmtNames

	// Scan transformers
	scanTypes := queryCoord.GetRegisteredScanTypes()
	scanNames := make([]string, len(scanTypes))
	for i, t := range scanTypes {
		scanNames[i] = t
	}
	result["scans"] = scanNames

	return result
}

// Placeholder transformer constructors - these would be implemented in separate files
func NewAggregateFunctionTransformer(coord Coordinator) ExpressionTransformer {
	return nil
}
func NewAnalyticFunctionTransformer(coord Coordinator) ExpressionTransformer {
	return nil
}
func NewSubqueryExprTransformer(coord Coordinator) ExpressionTransformer   { return nil }
func NewDMLDefaultTransformer() ExpressionTransformer                      { return nil }
func NewDMLValueTransformer(coord Coordinator) ExpressionTransformer       { return nil }
func NewMakeStructTransformer(coord Coordinator) ExpressionTransformer     { return nil }
func NewGetStructFieldTransformer(coord Coordinator) ExpressionTransformer { return nil }
func NewGetJsonFieldTransformer(coord Coordinator) ExpressionTransformer   { return nil }
func NewComputedColumnTransformer(coord Coordinator) ExpressionTransformer { return nil }
func NewOutputColumnTransformer(coord Coordinator) ExpressionTransformer   { return nil }

// Statement transformer placeholders
func NewCreateTableTransformer(coord Coordinator) StatementTransformer { return nil }
func NewCreateFunctionTransformer(coord Coordinator) StatementTransformer {
	return nil
}
func NewInsertTransformer(coord Coordinator) StatementTransformer { return nil }
func NewUpdateTransformer(coord Coordinator) StatementTransformer { return nil }
func NewDeleteTransformer(coord Coordinator) StatementTransformer { return nil }
func NewTruncateTransformer() StatementTransformer                { return nil }
