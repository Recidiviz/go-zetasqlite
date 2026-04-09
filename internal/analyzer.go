package internal

import (
	"google.golang.org/api/bigquery/v2"

	"context"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/vantaboard/go-googlesql"
	parsed_ast "github.com/vantaboard/go-googlesql/ast"
	ast "github.com/vantaboard/go-googlesql/resolved_ast"
	"github.com/vantaboard/go-googlesql/types"
)

type Analyzer struct {
	namePath        *NamePath
	isAutoIndexMode bool
	isExplainMode   bool
	catalog         *Catalog
	opt             *googlesql.AnalyzerOptions
	queryParameters []*bigquery.QueryParameter
}

type DisableQueryFormattingKey struct{}

var invalidInt64CastPattern = regexp.MustCompile(`(?is)\bCAST\s*\(\s*("[^"]*"|'[^']*')\s+AS\s+INT64\s*\)`)
var invalidSafeInt64CastPattern = regexp.MustCompile(`(?is)\bSAFE_CAST\s*\(\s*("[^"]*"|'[^']*')\s+AS\s+INT64\s*\)`)

func NewAnalyzer(catalog *Catalog) (*Analyzer, error) {
	opt, err := newAnalyzerOptions()
	if err != nil {
		return nil, err
	}
	return &Analyzer{
		catalog:  catalog,
		opt:      opt,
		namePath: &NamePath{},
	}, nil
}

func newAnalyzerOptions() (*googlesql.AnalyzerOptions, error) {
	langOpt := googlesql.NewLanguageOptions()
	langOpt.SetNameResolutionMode(googlesql.NameResolutionDefault)
	langOpt.SetProductMode(types.ProductInternal)
	langOpt.SetEnabledLanguageFeatures([]googlesql.LanguageFeature{
		googlesql.FeatureAnalyticFunctions,
		googlesql.FeatureNamedArguments,
		googlesql.FeatureNumericType,
		googlesql.FeatureBignumericType,
		googlesql.FeatureV13DecimalAlias,
		googlesql.FeatureCreateTableNotNull,
		googlesql.FeatureParameterizedTypes,
		googlesql.FeatureTablesample,
		googlesql.FeatureTimestampNanos,
		googlesql.FeatureV11HavingInAggregate,
		googlesql.FeatureV11NullHandlingModifierInAggregate,
		googlesql.FeatureV11NullHandlingModifierInAnalytic,
		googlesql.FeatureV11OrderByCollate,
		googlesql.FeatureV11SelectStarExceptReplace,
		googlesql.FeatureV12SafeFunctionCall,
		googlesql.FeatureJsonType,
		googlesql.FeatureJsonArrayFunctions,
		googlesql.FeatureJsonConstructorFunctions,
		googlesql.FeatureJsonMutatorFunctions,
		googlesql.FeatureJsonKeysFunction,
		googlesql.FeatureJsonLaxValueExtractionFunctions,
		googlesql.FeatureJsonStrictNumberParsing,
		googlesql.FeatureV13IsDistinct,
		googlesql.FeatureV13FormatInCast,
		googlesql.FeatureV13DateArithmetics,
		googlesql.FeatureV11OrderByInAggregate,
		googlesql.FeatureV11LimitInAggregate,
		googlesql.FeatureV13DateTimeConstructors,
		googlesql.FeatureV13ExtendedDateTimeSignatures,
		googlesql.FeatureV12CivilTime,
		googlesql.FeatureV12WeekWithWeekday,
		googlesql.FeatureIntervalType,
		googlesql.FeatureGroupByRollup,
		googlesql.FeatureV13NullsFirstLastInOrderBy,
		googlesql.FeatureV13Qualify,
		googlesql.FeatureV13AllowDashesInTableName,
		googlesql.FeatureGeography,
		googlesql.FeatureV13ExtendedGeographyParsers,
		googlesql.FeatureTemplateFunctions,
		googlesql.FeatureV11WithOnSubquery,
		googlesql.FeatureV13Pivot,
		googlesql.FeatureV13Unpivot,
		googlesql.FeatureDMLUpdateWithJoin,
		googlesql.FeatureV13OmitInsertColumnList,
		googlesql.FeatureV13WithRecursive,
		googlesql.FeatureV12GroupByArray,
		googlesql.FeatureV12GroupByStruct,
		// v1.4 builtins (numeric ids until named in go-googlesql enum): FIRST/LAST N, NULLIFZERO/ZEROIFNULL, PI
		googlesql.LanguageFeature(14027),
		googlesql.LanguageFeature(14028),
		googlesql.LanguageFeature(14029),
		// 2023.09.1 options.proto: singleton UNNEST alias, ARRAY_ZIP, multiway UNNEST
		googlesql.LanguageFeature(14031),
		googlesql.LanguageFeature(14032),
		googlesql.LanguageFeature(14033),
	})
	langOpt.SetSupportedStatementKinds([]ast.Kind{
		ast.BeginStmt,
		ast.CommitStmt,
		ast.MergeStmt,
		ast.QueryStmt,
		ast.InsertStmt,
		ast.UpdateStmt,
		ast.DeleteStmt,
		ast.DropStmt,
		ast.TruncateStmt,
		ast.CreateTableStmt,
		ast.CreateTableAsSelectStmt,
		ast.CreateProcedureStmt,
		ast.CreateFunctionStmt,
		ast.CreateTableFunctionStmt,
		ast.CreateViewStmt,
		ast.DropFunctionStmt,
		ast.DropRowAccessPolicyStmt,
		ast.CreateRowAccessPolicyStmt,
	})
	// Enable QUALIFY without WHERE
	//https://github.com/google/googlesql/issues/124
	err := langOpt.EnableReservableKeyword("QUALIFY", true)
	if err != nil {
		return nil, err
	}
	opt := googlesql.NewAnalyzerOptions()
	opt.SetAllowUndeclaredParameters(true)
	opt.SetLanguage(langOpt)
	opt.SetParseLocationRecordType(googlesql.ParseLocationRecordFullNodeScope)
	return opt, nil
}

func (a *Analyzer) SetAutoIndexMode(enabled bool) {
	a.isAutoIndexMode = enabled
}

func (a *Analyzer) SetExplainMode(enabled bool) {
	a.isExplainMode = enabled
}

func (a *Analyzer) NamePath() []string {
	return a.namePath.path
}

func (a *Analyzer) SetQueryParameters(parameters []*bigquery.QueryParameter) {
	a.queryParameters = parameters
}

func (a *Analyzer) PopQueryParameters() []*bigquery.QueryParameter {
	parameters := a.queryParameters
	a.SetQueryParameters(nil)
	return parameters
}

func (a *Analyzer) SetNamePath(path []string) error {
	return a.namePath.setPath(path)
}

func (a *Analyzer) SetMaxNamePath(num int) {
	a.namePath.setMaxNum(num)
}

func (a *Analyzer) MaxNamePath() int {
	return a.namePath.maxNum
}

func (a *Analyzer) AddNamePath(path string) error {
	return a.namePath.addPath(path)
}

func (a *Analyzer) parseScript(query string) ([]parsed_ast.StatementNode, error) {
	loc := googlesql.NewParseResumeLocation(query)
	var stmts []parsed_ast.StatementNode
	for {
		stmt, isEnd, err := googlesql.ParseNextScriptStatement(loc, a.opt.ParserOptions())
		if err != nil {
			return nil, fmt.Errorf("failed to parse statement: %w", err)
		}
		switch s := stmt.(type) {
		case *parsed_ast.BeginEndBlockNode:
			stmts = append(stmts, s.StatementList()...)
		default:
			stmts = append(stmts, s)
		}
		if isEnd {
			break
		}
	}
	return stmts, nil
}

func normalizePositionalParameters(query string, parameters []*bigquery.QueryParameter) (string, []*bigquery.QueryParameter) {
	if !strings.Contains(query, "?") {
		return query, parameters
	}

	var (
		builder          strings.Builder
		positionalIdx    int
		inSingleQuote    bool
		inDoubleQuote    bool
		inBacktick       bool
		normalizedParams []*bigquery.QueryParameter
	)
	if len(parameters) > 0 {
		normalizedParams = make([]*bigquery.QueryParameter, 0, len(parameters))
	}

	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch ch {
		case '\'':
			if !inDoubleQuote && !inBacktick {
				inSingleQuote = !inSingleQuote
			}
			builder.WriteByte(ch)
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
			builder.WriteByte(ch)
		case '`':
			if !inSingleQuote && !inDoubleQuote {
				inBacktick = !inBacktick
			}
			builder.WriteByte(ch)
		case '?':
			if inSingleQuote || inDoubleQuote || inBacktick {
				builder.WriteByte(ch)
				continue
			}
			positionalIdx++
			builder.WriteString(fmt.Sprintf("@p%d", positionalIdx))
		default:
			builder.WriteByte(ch)
		}
	}

	if len(parameters) == 0 {
		return builder.String(), nil
	}
	positionalIdx = 0
	for _, parameter := range parameters {
		if parameter.Name == "" {
			positionalIdx++
			copied := *parameter
			copied.Name = fmt.Sprintf("p%d", positionalIdx)
			normalizedParams = append(normalizedParams, &copied)
			continue
		}
		normalizedParams = append(normalizedParams, parameter)
	}
	return builder.String(), normalizedParams
}

func validateLiteralCast(stmtQuery string) error {
	match := invalidInt64CastPattern.FindStringSubmatchIndex(stmtQuery)
	if match == nil {
		return nil
	}
	literal := stmtQuery[match[2]:match[3]]
	unquoted, err := strconv.Unquote(literal)
	if err != nil {
		return nil
	}
	if _, err := strconv.ParseInt(unquoted, 10, 64); err == nil {
		return nil
	}
	return fmt.Errorf(
		`INVALID_ARGUMENT: Could not cast literal %s to type INT64 [at 1:%d]`,
		literal,
		match[2]+1,
	)
}

func normalizeSafeLiteralCasts(query string) string {
	return invalidSafeInt64CastPattern.ReplaceAllStringFunc(query, func(match string) string {
		submatches := invalidSafeInt64CastPattern.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		unquoted, err := strconv.Unquote(submatches[1])
		if err != nil {
			return match
		}
		if _, err := strconv.ParseInt(unquoted, 10, 64); err == nil {
			return match
		}
		return "CAST(NULL AS INT64)"
	})
}

func (a *Analyzer) getParameterMode(stmt parsed_ast.StatementNode) (googlesql.ParameterMode, error) {
	var (
		enabledNamedParameter      bool
		enabledPositionalParameter bool
	)
	_ = parsed_ast.Walk(stmt, func(node parsed_ast.Node) error {
		switch n := node.(type) {
		case *parsed_ast.ParameterExprNode:
			if n.Name() == nil {
				enabledPositionalParameter = true
			}
			if n.Name() != nil {
				enabledNamedParameter = true
			}
		}
		return nil
	})
	if enabledNamedParameter && enabledPositionalParameter {
		return googlesql.ParameterNone, fmt.Errorf("named parameter and positional parameter cannot be used together")
	}

	if enabledPositionalParameter {
		return googlesql.ParameterPositional, nil
	}
	return googlesql.ParameterNamed, nil
}

type StmtActionFunc func() (StmtAction, error)

func (a *Analyzer) configureQueryParameters(options *googlesql.AnalyzerOptions) error {
	parameters := a.PopQueryParameters()
	if parameters == nil {
		return nil
	}

	// When we have explicit query parameters, we must disable undeclared parameters
	// otherwise AddPositionalQueryParameter will fail
	options.SetAllowUndeclaredParameters(false)

	for _, parameter := range parameters {
		parameterType, err := GoogleSQLTypeFromBigQueryType(parameter.ParameterType)
		if err != nil {
			return err
		}

		if parameter.Name == "" {
			err = options.AddPositionalQueryParameter(parameterType)
			if err != nil {
				return err
			}
		} else {
			err = options.AddQueryParameter(parameter.Name, parameterType)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Analyzer) Analyze(ctx context.Context, conn *Conn, query string, args []driver.NamedValue) ([]StmtActionFunc, error) {
	query = normalizeSafeLiteralCasts(query)
	query, a.queryParameters = normalizePositionalParameters(query, a.queryParameters)
	if err := validateLiteralCast(query); err != nil {
		return nil, fmt.Errorf("failed to analyze: %w", err)
	}
	if err := a.catalog.Sync(ctx, conn); err != nil {
		return nil, fmt.Errorf("failed to sync catalog: %w", err)
	}
	stmts, err := a.parseScript(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse statements: %w", err)
	}
	funcMap := map[string]*FunctionSpec{}
	for _, spec := range a.catalog.getFunctions(a.namePath) {
		funcMap[spec.FuncName()] = spec
	}
	options, err := newAnalyzerOptions()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize analyzer options")
	}
	err = a.configureQueryParameters(options)
	if err != nil {
		return nil, fmt.Errorf("failed to configure query parameter types: %s", err)
	}
	actionFuncs := make([]StmtActionFunc, 0, len(stmts))
	for _, stmt := range stmts {
		stmt := stmt
		actionFuncs = append(actionFuncs, func() (StmtAction, error) {
			stmtQuery := query
			if locRange := stmt.ParseLocationRange(); locRange != nil && locRange.Start() != nil && locRange.End() != nil {
				start := locRange.Start().ByteOffset()
				end := locRange.End().ByteOffset()
				if 0 <= start && start <= end && end <= len(query) {
					stmtQuery = query[start:end]
				}
			}
			if err := validateLiteralCast(stmtQuery); err != nil {
				return nil, fmt.Errorf("failed to analyze: %w", err)
			}
			alignedStmt, err := googlesql.ParseStatement(stmtQuery, a.opt.ParserOptions())
			if err != nil {
				return nil, fmt.Errorf("failed to parse statement for analysis alignment: %w", err)
			}
			mode, err := a.getParameterMode(stmt)
			if err != nil {
				return nil, err
			}
			options.SetParameterMode(mode)
			out, err := googlesql.AnalyzeStatement(stmtQuery, a.catalog, options)
			if err != nil {
				return nil, fmt.Errorf("failed to analyze: %w", err)
			}
			stmtNode := out.Statement()
			ctx = a.context(ctx, funcMap, stmtNode, alignedStmt)
			action, err := a.newStmtAction(ctx, query, args, stmtNode)
			if err != nil {
				return nil, err
			}
			if mode == googlesql.ParameterPositional {
				args = args[len(action.Args()):]
			}
			return action, nil
		})
	}
	return actionFuncs, nil
}

func GoogleSQLTypeFromBigQueryType(t *bigquery.QueryParameterType) (types.Type, error) {
	// Generates GoogleSQL annotated types from a list of bigquery query parameters
	if t.Type == "ARRAY" {
		element, err := GoogleSQLTypeFromBigQueryType(t.ArrayType)
		if err != nil {
			return nil, err
		}
		return types.NewArrayType(element)
	}

	if t.Type == "STRUCT" {
		fields := []*types.StructField{}
		for _, field := range t.StructTypes {
			element, err := GoogleSQLTypeFromBigQueryType(field.Type)
			if err != nil {
				return nil, err
			}

			fields = append(fields, types.NewStructField(field.Name, element))
		}
		return types.NewStructType(fields)
	}

	var googlesqlType types.Type
	switch t.Type {
	case "INT32":
		googlesqlType = types.Int32Type()
	case "INT64":
		googlesqlType = types.Int64Type()
	case "UINT32":
		googlesqlType = types.Uint32Type()
	case "UINT64":
		googlesqlType = types.Uint64Type()
	case "BOOL":
		googlesqlType = types.BoolType()
	case "FLOAT", "FLOAT32":
		googlesqlType = types.FloatType()
	case "FLOAT64", "DOUBLE":
		googlesqlType = types.DoubleType()
	case "STRING":
		googlesqlType = types.StringType()
	case "BYTES":
		googlesqlType = types.BytesType()
	case "DATE":
		googlesqlType = types.DateType()
	case "TIMESTAMP":
		googlesqlType = types.TimestampType()
	case "TIME":
		googlesqlType = types.TimeType()
	case "DATETIME":
		googlesqlType = types.DatetimeType()
	case "GEOGRAPHY":
		googlesqlType = types.GeographyType()
	case "NUMERIC", "DECIMAL":
		googlesqlType = types.NumericType()
	case "BIGDECIMAL", "BIGNUMERIC":
		googlesqlType = types.BigNumericType()
	case "JSON":
		googlesqlType = types.JsonType()
	case "INTERVAL":
		googlesqlType = types.IntervalType()
	default:
		return nil, fmt.Errorf("unsupported query parameter type: %s", t.Type)
	}
	return googlesqlType, nil

}

func (a *Analyzer) context(
	ctx context.Context,
	funcMap map[string]*FunctionSpec,
	stmtNode ast.StatementNode,
	stmt parsed_ast.StatementNode) context.Context {
	ctx = withAnalyzer(ctx, a)
	ctx = withNamePath(ctx, a.namePath)
	ctx = withFuncMap(ctx, funcMap)
	ctx = withNodeMap(ctx, googlesql.NewNodeMap(stmtNode, stmt))
	return ctx
}

func (a *Analyzer) analyzeTemplatedFunctionWithRuntimeArgument(ctx context.Context, query string) (*FunctionSpec, error) {
	out, err := googlesql.AnalyzeStatement(query, a.catalog, a.opt)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze: %w", err)
	}
	node := out.Statement()
	stmt, ok := node.(*ast.CreateFunctionStmtNode)
	if !ok {
		return nil, fmt.Errorf("unexpected create function query %s", query)
	}
	spec, err := newFunctionSpec(ctx, a.namePath, stmt)
	if err != nil {
		return nil, fmt.Errorf("failed to create function spec: %w", err)
	}
	return spec, nil
}

func (a *Analyzer) newStmtAction(ctx context.Context, query string, args []driver.NamedValue, node ast.StatementNode) (StmtAction, error) {
	switch node.Kind() {
	case ast.CreateTableStmt:
		return a.newCreateTableStmtAction(ctx, args, node.(*ast.CreateTableStmtNode))
	case ast.CreateTableAsSelectStmt:
		return a.newCreateTableAsSelectStmtAction(ctx, query, args, node.(*ast.CreateTableAsSelectStmtNode))
	case ast.CreateFunctionStmt:
		return a.newCreateFunctionStmtAction(ctx, query, args, node.(*ast.CreateFunctionStmtNode))
	case ast.CreateViewStmt:
		return a.newCreateViewStmtAction(ctx, query, args, node.(*ast.CreateViewStmtNode))
	case ast.DropStmt:
		return a.newDropStmtAction(ctx, query, args, node.(*ast.DropStmtNode))
	case ast.DropFunctionStmt:
		return a.newDropFunctionStmtAction(ctx, query, args, node.(*ast.DropFunctionStmtNode))
	case ast.InsertStmt, ast.UpdateStmt, ast.DeleteStmt:
		return a.newDMLStmtAction(ctx, query, args, node)
	case ast.TruncateStmt:
		return a.newTruncateStmtAction(ctx, query, args, node.(*ast.TruncateStmtNode))
	case ast.MergeStmt:
		return a.newMergeStmtAction(ctx, query, args, node.(*ast.MergeStmtNode))
	case ast.QueryStmt:
		return a.newQueryStmtAction(ctx, query, args, node.(*ast.QueryStmtNode))
	case ast.BeginStmt:
		return a.newBeginStmtAction(ctx, query, args, node)
	case ast.CommitStmt:
		return a.newCommitStmtAction(ctx, query, args, node)
	case ast.CreateRowAccessPolicyStmt, ast.DropRowAccessPolicyStmt:
		return a.newNullStmtAction(ctx, query, args, node)
	}
	return nil, fmt.Errorf("unsupported stmt %s", node.DebugString())
}

func (a *Analyzer) newNullStmtAction(_ context.Context, query string, args []driver.NamedValue, node interface{}) (*NullStmtAction, error) {
	return &NullStmtAction{}, nil
}

func (a *Analyzer) newCreateTableStmtAction(ctx context.Context, args []driver.NamedValue, node *ast.CreateTableStmtNode) (*CreateTableStmtAction, error) {
	spec := newTableSpec(a.namePath, node)
	params := getParamsFromNode(node)
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}
	return &CreateTableStmtAction{
		query:           nil,
		spec:            spec,
		args:            queryArgs,
		catalog:         a.catalog,
		isAutoIndexMode: a.isAutoIndexMode,
	}, nil
}

func (a *Analyzer) newCreateTableAsSelectStmtAction(ctx context.Context, _ string, args []driver.NamedValue, node *ast.CreateTableAsSelectStmtNode) (*CreateTableStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", "CREATE TABLE AS SELECT", err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format CREATE TABLE AS SELECT query")
	}

	// Extract the transformed CREATE TABLE statement
	createTableStmt, ok := result.Fragment.(*CreateTableStatement)
	if !ok {
		return nil, fmt.Errorf("expected CreateTableStatement from transformer, got %T", result.Fragment)
	}

	query := createTableStmt.AsSelect
	spec := newTableAsSelectSpec(a.namePath, query, node)
	params := getParamsFromNode(node)
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}
	return &CreateTableStmtAction{
		query:           query,
		spec:            spec,
		args:            queryArgs,
		catalog:         a.catalog,
		isAutoIndexMode: a.isAutoIndexMode,
	}, nil
}

func (a *Analyzer) newCreateFunctionStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.CreateFunctionStmtNode) (*CreateFunctionStmtAction, error) {
	var spec *FunctionSpec
	if a.resultTypeIsTemplatedType(node.Signature()) {
		realStmts, err := a.inferTemplatedTypeByRealType(query, node)
		if err != nil {
			return nil, err
		}
		templatedFuncSpec, err := newTemplatedFunctionSpec(ctx, a.namePath, node, realStmts)
		if err != nil {
			return nil, err
		}
		spec = templatedFuncSpec
	} else {
		funcSpec, err := newFunctionSpec(ctx, a.namePath, node)
		if err != nil {
			return nil, fmt.Errorf("failed to create function spec: %w", err)
		}
		spec = funcSpec
	}
	return &CreateFunctionStmtAction{
		spec:    spec,
		catalog: a.catalog,
		funcMap: funcMapFromContext(ctx),
	}, nil
}

func (a *Analyzer) newCreateViewStmtAction(ctx context.Context, _ string, args []driver.NamedValue, node *ast.CreateViewStmtNode) (*CreateViewStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", "CREATE VIEW", err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format CREATE VIEW query")
	}

	// Extract the transformed CREATE VIEW statement
	createViewStmt, ok := result.Fragment.(*CreateViewStatement)
	if !ok {
		return nil, fmt.Errorf("expected CreateViewStatement from transformer, got %T", result.Fragment)
	}

	spec := newTableAsViewSpec(a.namePath, createViewStmt.Query, node)
	return &CreateViewStmtAction{
		query:   createViewStmt,
		spec:    spec,
		catalog: a.catalog,
	}, nil
}

func (a *Analyzer) resultTypeIsTemplatedType(sig *types.FunctionSignature) bool {
	if !sig.IsTemplated() {
		return false
	}
	return sig.ResultType().IsTemplated()
}

var inferTypes = []string{
	"INT64", "DOUBLE", "BOOL", "STRING", "BYTES",
	"JSON", "DATE", "DATETIME", "TIME", "TIMESTAMP",
	"INTERVAL", "GEOGRAPHY",
	"STRUCT<>",
}

func (a *Analyzer) inferTemplatedTypeByRealType(query string, node *ast.CreateFunctionStmtNode) ([]*ast.CreateFunctionStmtNode, error) {
	var stmts []*ast.CreateFunctionStmtNode
	for _, typ := range inferTypes {
		if out, err := googlesql.AnalyzeStatement(a.buildScalarTypeFuncFromTemplatedFunc(node, typ), a.catalog, a.opt); err == nil {
			stmts = append(stmts, out.Statement().(*ast.CreateFunctionStmtNode))
		}
	}
	if len(stmts) != 0 {
		return stmts, nil
	}
	for _, typ := range inferTypes {
		if out, err := googlesql.AnalyzeStatement(a.buildArrayTypeFuncFromTemplatedFunc(node, typ), a.catalog, a.opt); err == nil {
			stmts = append(stmts, out.Statement().(*ast.CreateFunctionStmtNode))
		}
	}
	if len(stmts) != 0 {
		return stmts, nil
	}
	return nil, fmt.Errorf("failed to infer templated function result type for %s", query)
}

func (a *Analyzer) buildScalarTypeFuncFromTemplatedFunc(node *ast.CreateFunctionStmtNode, realType string) string {
	signature := node.Signature()
	var args []string
	for _, arg := range signature.Arguments() {
		typ := realType
		if !arg.IsTemplated() {
			typ = newType(arg.Type()).FormatType()
		}
		args = append(args, fmt.Sprintf("%s %s", arg.ArgumentName(), typ))
	}
	return fmt.Sprintf(
		"CREATE TEMP FUNCTION __googlesqlite_func__(%s) as (%s)",
		strings.Join(args, ","),
		node.Code(),
	)
}

func (a *Analyzer) buildArrayTypeFuncFromTemplatedFunc(node *ast.CreateFunctionStmtNode, realType string) string {
	signature := node.Signature()
	var args []string
	for _, arg := range signature.Arguments() {
		typ := fmt.Sprintf("ARRAY<%s>", realType)
		if !arg.IsTemplated() {
			typ = newType(arg.Type()).FormatType()
		}
		args = append(args, fmt.Sprintf("%s %s", arg.ArgumentName(), typ))
	}
	return fmt.Sprintf(
		"CREATE TEMP FUNCTION __googlesqlite_func__(%s) as (%s)",
		strings.Join(args, ","),
		node.Code(),
	)
}

func (a *Analyzer) newDropStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.DropStmtNode) (*DropStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", query, err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format query %s", query)
	}
	params := getParamsFromNode(node)
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}
	objectType := node.ObjectType()
	name := a.namePath.format(node.NamePath())
	return &DropStmtAction{
		name:           name,
		objectType:     objectType,
		funcMap:        funcMapFromContext(ctx),
		catalog:        a.catalog,
		query:          query,
		formattedQuery: result.Fragment.String(),
		args:           queryArgs,
	}, nil
}

func (a *Analyzer) newDropFunctionStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.DropFunctionStmtNode) (*DropStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", query, err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format query %s", query)
	}
	params := getParamsFromNode(node)
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}
	name := a.namePath.format(node.NamePath())
	return &DropStmtAction{
		name:           name,
		objectType:     "FUNCTION",
		funcMap:        funcMapFromContext(ctx),
		catalog:        a.catalog,
		query:          query,
		formattedQuery: result.Fragment.String(),
		args:           queryArgs,
	}, nil
}

func (a *Analyzer) newDMLStmtAction(ctx context.Context, query string, args []driver.NamedValue, node ast.Node) (*DMLStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", query, err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format query %s", query)
	}

	params := getParamsFromNode(node)
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}

	return &DMLStmtAction{
		query:          query,
		params:         params,
		args:           queryArgs,
		formattedQuery: result.Fragment.String(),
		catalog:        a.catalog,
	}, nil
}

func (a *Analyzer) newQueryStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.QueryStmtNode) (*QueryStmtAction, error) {
	outputColumns := []*ColumnSpec{}
	for _, col := range node.OutputColumnList() {
		outputColumns = append(outputColumns, &ColumnSpec{
			Name: col.Name(),
			Type: newType(col.Column().Type()),
		})
	}
	var formattedQuery string
	params := getParamsFromNode(node)
	if disabledFormatting, ok := ctx.Value(DisableQueryFormattingKey{}).(bool); ok && disabledFormatting {
		formattedQuery = query
		// GoogleSQL will always lowercase parameter names, so we must match it in the query
		queryBytes := []byte(query)
		for _, param := range params {
			location := param.ParseLocationRange()
			start := location.Start().ByteOffset()
			end := location.End().ByteOffset()
			// Finds the parameter including its prefix i.e. @itemID
			parameter := string(queryBytes[start:end])
			formattedQuery = strings.ReplaceAll(formattedQuery, parameter, strings.ToLower(parameter))
		}
	} else {
		var err error
		result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
		if err != nil {
			return nil, fmt.Errorf("failed to format query %s: %w", query, err)
		}

		formattedQuery = result.Fragment.String()
	}

	if formattedQuery == "" {
		return nil, fmt.Errorf("failed to format query %s", query)
	}
	queryArgs, err := getArgsFromParams(args, params)
	if err != nil {
		return nil, err
	}
	return &QueryStmtAction{
		query:          query,
		params:         params,
		args:           queryArgs,
		formattedQuery: formattedQuery,
		outputColumns:  outputColumns,
		isExplainMode:  a.isExplainMode,
		catalog:        a.catalog,
	}, nil
}

func (a *Analyzer) newBeginStmtAction(ctx context.Context, query string, args []driver.NamedValue, node ast.Node) (*BeginStmtAction, error) {
	return &BeginStmtAction{}, nil
}

func (a *Analyzer) newCommitStmtAction(ctx context.Context, query string, args []driver.NamedValue, node ast.Node) (*CommitStmtAction, error) {
	return &CommitStmtAction{}, nil
}

func (a *Analyzer) newTruncateStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.TruncateStmtNode) (*TruncateStmtAction, error) {
	unresolvedNodes := nodeMapFromContext(ctx).FindNodeFromResolvedNode(node)
	namePath := resolvedTableNamePath(node.TableScan())
	if len(namePath) == 0 {
		namePath = []string{node.TableScan().Table().Name()}
	}
	var truncateNode parsed_ast.Node
	for _, unresolvedNode := range unresolvedNodes {
		if unresolvedNode.Kind() == parsed_ast.TrucateStatement {
			truncateNode = unresolvedNode
			break
		}
	}
	if truncateNode != nil {
		for i := 0; i < truncateNode.NumChildren(); i++ {
			if pathExpressionNode, ok := truncateNode.Child(i).(*parsed_ast.PathExpressionNode); ok {
				var err error
				namePath, err = getPathFromNode(pathExpressionNode)
				if err != nil {
					return nil, fmt.Errorf("failed to get truncate path from node %d: %w ", i, err)
				}
			}
		}
	}
	return &TruncateStmtAction{
		query:   fmt.Sprintf("DELETE FROM `%s`", a.namePath.format(namePath)),
		catalog: a.catalog,
	}, nil
}

func (a *Analyzer) newMergeStmtAction(ctx context.Context, query string, args []driver.NamedValue, node *ast.MergeStmtNode) (*MergeStmtAction, error) {
	result, err := GetGlobalQueryTransformFactory().TransformQuery(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to format query %s: %w", "MERGE", err)
	}
	if result == nil || result.Fragment == nil {
		return nil, fmt.Errorf("failed to format MERGE query")
	}

	// Extract the transformed statements from the compound fragment
	var stmts []string
	if compoundFragment, ok := result.Fragment.(*CompoundSQLFragment); ok {
		stmts = compoundFragment.GetStatements()
	} else {
		// Fallback to single statement
		stmts = []string{result.Fragment.String()}
	}

	return &MergeStmtAction{stmts: stmts}, nil
}

func getParamsFromNode(node ast.Node) []*ast.ParameterNode {
	var (
		params       []*ast.ParameterNode
		paramNameMap = map[string]struct{}{}
	)
	_ = ast.Walk(node, func(n ast.Node) error {
		param, ok := n.(*ast.ParameterNode)
		if ok {
			name := param.Name()
			if name != "" {
				if _, exists := paramNameMap[name]; !exists {
					params = append(params, param)
					paramNameMap[name] = struct{}{}
				}
			} else {
				params = append(params, param)
			}
		}
		return nil
	})
	return params
}

func getArgsFromParams(values []driver.NamedValue, params []*ast.ParameterNode) ([]interface{}, error) {
	if values == nil {
		return nil, nil
	}
	argNum := len(params)
	if len(values) < argNum {
		return nil, fmt.Errorf("not enough query arguments")
	}
	namedValuesMap := map[string]driver.NamedValue{}
	for _, value := range values {
		// Name() value of ast.ParameterNode always returns lowercase name.
		namedValuesMap[strings.ToLower(value.Name)] = value
	}
	var namedValues []driver.NamedValue
	for idx, param := range params {
		name := param.Name()
		if name != "" {
			value, exists := namedValuesMap[name]
			if exists {
				namedValues = append(namedValues, value)
			} else {
				fallbackIdx := idx
				if positionalIdx, ok := synthesizedPositionalParamIndex(name); ok {
					fallbackIdx = positionalIdx
				}
				if fallbackIdx >= len(values) {
					return nil, fmt.Errorf("not enough query arguments")
				}
				fallback := values[fallbackIdx]
				fallback.Name = name
				namedValues = append(namedValues, fallback)
			}
		} else {
			namedValues = append(namedValues, values[idx])
		}
	}
	newNamedValues, err := EncodeNamedValues(namedValues, params)
	if err != nil {
		return nil, err
	}
	args := make([]interface{}, 0, argNum)
	for _, newNamedValue := range newNamedValues {
		args = append(args, newNamedValue)
	}
	return args, nil
}

func synthesizedPositionalParamIndex(name string) (int, bool) {
	if len(name) < 2 || name[0] != 'p' {
		return 0, false
	}
	idx, err := strconv.Atoi(name[1:])
	if err != nil || idx <= 0 {
		return 0, false
	}
	return idx - 1, true
}
