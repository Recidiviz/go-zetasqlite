package internal

import (
	"database/sql/driver"
	"fmt"
	"sync"

	"github.com/goccy/go-json"
	"modernc.org/sqlite"
)

var normalFuncs = map[string]*FuncInfo{
	"add":                   {Name: "add", BindFunc: bindAdd},
	"subtract":              {Name: "subtract", BindFunc: bindSub},
	"multiply":              {Name: "multiply", BindFunc: bindMul},
	"divide":                {Name: "divide", BindFunc: bindOpDiv},
	"equal":                 {Name: "equal", BindFunc: bindEqual},
	"not_equal":             {Name: "not_equal", BindFunc: bindNotEqual},
	"greater":               {Name: "greater", BindFunc: bindGreater},
	"greater_or_equal":      {Name: "greater_or_equal", BindFunc: bindGreaterOrEqual},
	"less":                  {Name: "less", BindFunc: bindLess},
	"less_or_equal":         {Name: "less_or_equal", BindFunc: bindLessOrEqual},
	"bitwise_not":           {Name: "bitwise_not", BindFunc: bindBitNot},
	"bitwise_left_shift":    {Name: "bitwise_left_shift", BindFunc: bindBitLeftShift},
	"bitwise_right_shift":   {Name: "bitwise_right_shift", BindFunc: bindBitRightShift},
	"bitwise_and":           {Name: "bitwise_and", BindFunc: bindBitAnd},
	"bitwise_or":            {Name: "bitwise_or", BindFunc: bindBitOr},
	"bitwise_xor":           {Name: "bitwise_xor", BindFunc: bindBitXor},
	"in_array":              {Name: "in_array", BindFunc: bindInArray},
	"get_struct_field":      {Name: "get_struct_field", BindFunc: bindStructField},
	"get_json_field":        {Name: "get_json_field", BindFunc: bindJsonField},
	"subscript":             {Name: "subscript", BindFunc: bindSubscript},
	"array_at_offset":       {Name: "array_at_offset", BindFunc: bindArrayAtOffset},
	"array_at_ordinal":      {Name: "array_at_ordinal", BindFunc: bindArrayAtOrdinal},
	"safe_array_at_offset":  {Name: "safe_array_at_offset", BindFunc: bindSafeArrayAtOffset},
	"safe_array_at_ordinal": {Name: "safe_array_at_ordinal", BindFunc: bindSafeArrayAtOrdinal},
	"is_distinct_from":      {Name: "is_distinct_from", BindFunc: bindIsDistinctFrom},
	"is_not_distinct_from":  {Name: "is_not_distinct_from", BindFunc: bindIsNotDistinctFrom},

	// security functions
	"session_user": {Name: "session_user", BindFunc: bindSessionUser},

	// uuid functions
	"generate_uuid": {Name: "generate_uuid", BindFunc: bindGenerateUUID},

	// debugging functions
	"error": {Name: "error", BindFunc: bindError},

	// date functions
	"current_date":        {Name: "current_date", BindFunc: bindCurrentDate},
	"extract":             {Name: "extract", BindFunc: bindExtract},
	"extract_date":        {Name: "extract_date", BindFunc: bindExtractDate},
	"date":                {Name: "date", BindFunc: bindDate},
	"date_add":            {Name: "date_add", BindFunc: bindDateAdd},
	"date_sub":            {Name: "date_sub", BindFunc: bindDateSub},
	"date_diff":           {Name: "date_diff", BindFunc: bindDateDiff},
	"date_trunc":          {Name: "date_trunc", BindFunc: bindDateTrunc},
	"date_from_unix_date": {Name: "date_from_unix_date", BindFunc: bindDateFromUnixDate},
	"format_date":         {Name: "format_date", BindFunc: bindFormatDate},
	"last_day":            {Name: "last_day", BindFunc: bindLastDay},
	"parse_date":          {Name: "parse_date", BindFunc: bindParseDate},
	"unix_date":           {Name: "unix_date", BindFunc: bindUnixDate},

	// datetime functions
	"current_datetime": {Name: "current_datetime", BindFunc: bindCurrentDatetime},
	"datetime":         {Name: "datetime", BindFunc: bindDatetime},
	"datetime_add":     {Name: "datetime_add", BindFunc: bindDatetimeAdd},
	"datetime_sub":     {Name: "datetime_sub", BindFunc: bindDatetimeSub},
	"datetime_diff":    {Name: "datetime_diff", BindFunc: bindDatetimeDiff},
	"datetime_trunc":   {Name: "datetime_trunc", BindFunc: bindDatetimeTrunc},
	"format_datetime":  {Name: "format_datetime", BindFunc: bindFormatDatetime},
	"parse_datetime":   {Name: "parse_datetime", BindFunc: bindParseDatetime},

	// time functions
	"current_time": {Name: "current_time", BindFunc: bindCurrentTime},
	"time":         {Name: "time", BindFunc: bindTime},
	"time_add":     {Name: "time_add", BindFunc: bindTimeAdd},
	"time_sub":     {Name: "time_sub", BindFunc: bindTimeSub},
	"time_diff":    {Name: "time_diff", BindFunc: bindTimeDiff},
	"time_trunc":   {Name: "time_trunc", BindFunc: bindTimeTrunc},
	"format_time":  {Name: "format_time", BindFunc: bindFormatTime},
	"parse_time":   {Name: "parse_time", BindFunc: bindParseTime},

	// timestamp functions
	"current_timestamp": {Name: "current_timestamp", BindFunc: bindCurrentTimestamp},
	"string":            {Name: "string", BindFunc: bindString},
	"timestamp":         {Name: "timestamp", BindFunc: bindTimestamp},
	"timestamp_add":     {Name: "timestamp_add", BindFunc: bindTimestampAdd},
	"timestamp_sub":     {Name: "timestamp_sub", BindFunc: bindTimestampSub},
	"timestamp_diff":    {Name: "timestamp_diff", BindFunc: bindTimestampDiff},
	"timestamp_trunc":   {Name: "timestamp_trunc", BindFunc: bindTimestampTrunc},
	"format_timestamp":  {Name: "format_timestamp", BindFunc: bindFormatTimestamp},
	"parse_timestamp":   {Name: "parse_timestamp", BindFunc: bindParseTimestamp},
	"timestamp_seconds": {Name: "timestamp_seconds", BindFunc: bindTimestampSeconds},
	"timestamp_millis":  {Name: "timestamp_millis", BindFunc: bindTimestampMillis},
	"timestamp_micros":  {Name: "timestamp_micros", BindFunc: bindTimestampMicros},
	"unix_seconds":      {Name: "unix_seconds", BindFunc: bindUnixSeconds},
	"unix_millis":       {Name: "unix_millis", BindFunc: bindUnixMillis},
	"unix_micros":       {Name: "unix_micros", BindFunc: bindUnixMicros},
	"like":              {Name: "like", BindFunc: bindLike},
	"between":           {Name: "between", BindFunc: bindBetween},
	"in":                {Name: "in", BindFunc: bindIn},
	"is_null":           {Name: "is_null", BindFunc: bindIsNull},
	"is_true":           {Name: "is_true", BindFunc: bindIsTrue},
	"is_false":          {Name: "is_false", BindFunc: bindIsFalse},
	"not":               {Name: "not", BindFunc: bindNot},
	"and":               {Name: "and", BindFunc: bindAnd},
	"or":                {Name: "or", BindFunc: bindOr},
	"coalesce":          {Name: "coalesce", BindFunc: bindCoalesce},
	"if":                {Name: "if", BindFunc: bindIf},
	"ifnull":            {Name: "ifnull", BindFunc: bindIfNull},
	"nullif":            {Name: "nullif", BindFunc: bindNullIf},
	"length":            {Name: "length", BindFunc: bindLength},
	"cast":              {Name: "cast", BindFunc: bindCast},

	// interval functions
	"interval":         {Name: "interval", BindFunc: bindInterval},
	"make_interval":    {Name: "make_interval", BindFunc: bindMakeInterval},
	"justify_days":     {Name: "justify_days", BindFunc: bindJustifyDays},
	"justify_hours":    {Name: "justify_hours", BindFunc: bindJustifyHours},
	"justify_interval": {Name: "justify_interval", BindFunc: bindJustifyInterval},

	// numeric/bignumeric functions
	"parse_numeric":    {Name: "parse_numeric", BindFunc: bindParseNumeric},
	"parse_bignumeric": {Name: "parse_bignumeric", BindFunc: bindParseBigNumeric},

	// hash functions
	"farm_fingerprint": {Name: "farm_fingerprint", BindFunc: bindFarmFingerprint},
	"md5":              {Name: "md5", BindFunc: bindMD5},
	"sha1":             {Name: "sha1", BindFunc: bindSha1},
	"sha256":           {Name: "sha256", BindFunc: bindSha256},
	"sha512":           {Name: "sha512", BindFunc: bindSha512},

	// string functions
	"ascii":                        {Name: "ascii", BindFunc: bindAscii},
	"byte_length":                  {Name: "byte_length", BindFunc: bindByteLength},
	"char_length":                  {Name: "char_length", BindFunc: bindCharLength},
	"chr":                          {Name: "chr", BindFunc: bindChr},
	"code_points_to_bytes":         {Name: "code_points_to_bytes", BindFunc: bindCodePointsToBytes},
	"code_points_to_string":        {Name: "code_points_to_string", BindFunc: bindCodePointsToString},
	"collate":                      {Name: "collate", BindFunc: bindCollate},
	"concat":                       {Name: "concat", BindFunc: bindConcat},
	"contains_substr":              {Name: "contains_substr", BindFunc: bindContainsSubstr},
	"ends_with":                    {Name: "ends_with", BindFunc: bindEndsWith},
	"format":                       {Name: "format", BindFunc: bindFormat},
	"from_base32":                  {Name: "from_base32", BindFunc: bindFromBase32},
	"from_base64":                  {Name: "from_base64", BindFunc: bindFromBase64},
	"from_hex":                     {Name: "from_hex", BindFunc: bindFromHex},
	"initcap":                      {Name: "initcap", BindFunc: bindInitcap},
	"instr":                        {Name: "instr", BindFunc: bindInstr},
	"left":                         {Name: "left", BindFunc: bindLeft},
	"lpad":                         {Name: "lpad", BindFunc: bindLpad},
	"lower":                        {Name: "lower", BindFunc: bindLower},
	"ltrim":                        {Name: "ltrim", BindFunc: bindLtrim},
	"normalize":                    {Name: "normalize", BindFunc: bindNormalize},
	"normalize_and_casefold":       {Name: "normalize_and_casefold", BindFunc: bindNormalizeAndCasefold},
	"regexp_contains":              {Name: "regexp_contains", BindFunc: bindRegexpContains},
	"regexp_extract":               {Name: "regexp_extract", BindFunc: bindRegexpExtract},
	"regexp_extract_all":           {Name: "regexp_extract_all", BindFunc: bindRegexpExtractAll},
	"regexp_instr":                 {Name: "regexp_instr", BindFunc: bindRegexpInstr},
	"regexp_replace":               {Name: "regexp_replace", BindFunc: bindRegexpReplace},
	"replace":                      {Name: "replace", BindFunc: bindReplace},
	"repeat":                       {Name: "repeat", BindFunc: bindRepeat},
	"reverse":                      {Name: "reverse", BindFunc: bindReverse},
	"right":                        {Name: "right", BindFunc: bindRight},
	"rpad":                         {Name: "rpad", BindFunc: bindRpad},
	"rtrim":                        {Name: "rtrim", BindFunc: bindRtrim},
	"safe_convert_bytes_to_string": {Name: "safe_convert_bytes_to_string", BindFunc: bindSafeConvertBytesToString},
	"soundex":                      {Name: "soundex", BindFunc: bindSoundex},
	"split":                        {Name: "split", BindFunc: bindSplit},
	"starts_with":                  {Name: "starts_with", BindFunc: bindStartsWith},
	"strpos":                       {Name: "strpos", BindFunc: bindStrpos},
	"substr":                       {Name: "substr", BindFunc: bindSubstr},
	"to_base32":                    {Name: "to_base32", BindFunc: bindToBase32},
	"to_base64":                    {Name: "to_base64", BindFunc: bindToBase64},
	"to_code_points":               {Name: "to_code_points", BindFunc: bindToCodePoints},
	"to_hex":                       {Name: "to_hex", BindFunc: bindToHex},
	"translate":                    {Name: "translate", BindFunc: bindTranslate},
	"trim":                         {Name: "trim", BindFunc: bindTrim},
	"unicode":                      {Name: "unicode", BindFunc: bindUnicode},
	"upper":                        {Name: "upper", BindFunc: bindUpper},

	// json functions
	"json_extract":              {Name: "json_extract", BindFunc: bindJsonExtract},
	"json_extract_scalar":       {Name: "json_extract_scalar", BindFunc: bindJsonExtractScalar},
	"json_extract_array":        {Name: "json_extract_array", BindFunc: bindJsonExtractArray},
	"json_extract_string_array": {Name: "json_extract_string_array", BindFunc: bindJsonExtractStringArray},
	"json_query":                {Name: "json_query", BindFunc: bindJsonQuery},
	"json_value":                {Name: "json_value", BindFunc: bindJsonValue},
	"json_query_array":          {Name: "json_query_array", BindFunc: bindJsonQueryArray},
	"json_value_array":          {Name: "json_value_array", BindFunc: bindJsonValueArray},
	"parse_json":                {Name: "parse_json", BindFunc: bindParseJson},
	"to_json":                   {Name: "to_json", BindFunc: bindToJson},
	"to_json_string":            {Name: "to_json_string", BindFunc: bindToJsonString},
	"bool":                      {Name: "bool", BindFunc: bindBool},
	"int64":                     {Name: "int64", BindFunc: bindInt64},
	"double":                    {Name: "double", BindFunc: bindDouble},
	"json_type":                 {Name: "json_type", BindFunc: bindJsonType},

	// math functions

	"abs":           {Name: "abs", BindFunc: bindAbs},
	"sign":          {Name: "sign", BindFunc: bindSign},
	"is_inf":        {Name: "is_inf", BindFunc: bindIsInf},
	"is_nan":        {Name: "is_nan", BindFunc: bindIsNaN},
	"ieee_divide":   {Name: "ieee_divide", BindFunc: bindIEEEDivide},
	"rand":          {Name: "rand", BindFunc: bindRand},
	"sqrt":          {Name: "sqrt", BindFunc: bindSqrt},
	"pow":           {Name: "pow", BindFunc: bindPow},
	"power":         {Name: "power", BindFunc: bindPow},
	"exp":           {Name: "exp", BindFunc: bindExp},
	"ln":            {Name: "ln", BindFunc: bindLn},
	"log":           {Name: "log", BindFunc: bindLog},
	"log10":         {Name: "log10", BindFunc: bindLog10},
	"greatest":      {Name: "greatest", BindFunc: bindGreatest},
	"least":         {Name: "least", BindFunc: bindLeast},
	"div":           {Name: "div", BindFunc: bindDiv},
	"safe_divide":   {Name: "safe_divide", BindFunc: bindSafeDivide},
	"safe_multiply": {Name: "safe_multiply", BindFunc: bindSafeMultiply},
	"safe_negate":   {Name: "safe_negate", BindFunc: bindSafeNegate},
	"safe_add":      {Name: "safe_add", BindFunc: bindSafeAdd},
	"safe_subtract": {Name: "safe_subtract", BindFunc: bindSafeSubtract},
	"mod":           {Name: "mod", BindFunc: bindMod},
	"round":         {Name: "round", BindFunc: bindRound},
	"trunc":         {Name: "trunc", BindFunc: bindTrunc},
	"ceil":          {Name: "ceil", BindFunc: bindCeil},
	"ceiling":       {Name: "ceiling", BindFunc: bindCeil},
	"floor":         {Name: "floor", BindFunc: bindFloor},
	"cos":           {Name: "cos", BindFunc: bindCos},
	"cosh":          {Name: "cosh", BindFunc: bindCosh},
	"acos":          {Name: "acos", BindFunc: bindAcos},
	"acosh":         {Name: "acosh", BindFunc: bindAcosh},
	"sin":           {Name: "sin", BindFunc: bindSin},
	"sinh":          {Name: "sinh", BindFunc: bindSinh},
	"asin":          {Name: "asin", BindFunc: bindAsin},
	"asinh":         {Name: "asinh", BindFunc: bindAsinh},
	"tan":           {Name: "tan", BindFunc: bindTan},
	"tanh":          {Name: "tanh", BindFunc: bindTanh},
	"atan":          {Name: "atan", BindFunc: bindAtan},
	"atanh":         {Name: "atanh", BindFunc: bindAtanh},
	"atan2":         {Name: "atan2", BindFunc: bindAtan2},
	"range_bucket":  {Name: "range_bucket", BindFunc: bindRangeBucket},

	// array functions
	"array_concat":             {Name: "array_concat", BindFunc: bindArrayConcat},
	"array_length":             {Name: "array_length", BindFunc: bindArrayLength},
	"array_to_string":          {Name: "array_to_string", BindFunc: bindArrayToString},
	"generate_array":           {Name: "generate_array", BindFunc: bindGenerateArray},
	"generate_date_array":      {Name: "generate_date_array", BindFunc: bindGenerateDateArray},
	"generate_timestamp_array": {Name: "generate_timestamp_array", BindFunc: bindGenerateTimestampArray},
	"array_reverse":            {Name: "array_reverse", BindFunc: bindArrayReverse},
	"make_array":               {Name: "make_array", BindFunc: bindMakeArray},
	"make_struct":              {Name: "make_struct", BindFunc: bindMakeStruct},

	// hyperloglog++ functions
	"hll_count_extract": {Name: "hll_count_extract", BindFunc: bindHllCountExtract},

	// bit functions
	"bit_count": {Name: "bit_count", BindFunc: bindBitCount},

	// aggregate option funcs
	"distinct":     {Name: "distinct", BindFunc: bindDistinct},
	"limit":        {Name: "limit", BindFunc: bindLimit},
	"order_by":     {Name: "order_by", BindFunc: bindOrderBy},
	"ignore_nulls": {Name: "ignore_nulls", BindFunc: bindIgnoreNulls},

	// javascript funcs
	"eval_javascript": {Name: "eval_javascript", BindFunc: bindEvalJavaScript},

	// net funcs
	"net_host":                {Name: "net_host", BindFunc: bindNetHost},
	"net_ip_from_string":      {Name: "net_ip_from_string", BindFunc: bindNetIpFromString},
	"net_ip_net_mask":         {Name: "net_ip_net_mask", BindFunc: bindNetIpNetMask},
	"net_ip_to_string":        {Name: "net_ip_to_string", BindFunc: bindNetIpToString},
	"net_ip_trunc":            {Name: "net_ip_trunc", BindFunc: bindNetIpTrunc},
	"net_ipv4_from_int64":     {Name: "net_ipv4_from_int64", BindFunc: bindNetIpv4FromInt64},
	"net_ipv4_to_int64":       {Name: "net_ipv4_to_int64", BindFunc: bindNetIpv4ToInt64},
	"net_public_suffix":       {Name: "net_public_suffix", BindFunc: bindNetPublicSuffix},
	"net_reg_domain":          {Name: "net_reg_domain", BindFunc: bindNetRegDomain},
	"net_safe_ip_from_string": {Name: "net_safe_ip_from_string", BindFunc: bindNetSafeIpFromString},
}

var aggregateFuncs = []*AggregateFuncInfo{
	{Name: "array", BindFunc: bindArray},

	// aggregate functions
	{Name: "any_value", BindFunc: bindAnyValue},
	{Name: "array_agg", BindFunc: bindArrayAgg},
	{Name: "array_concat_agg", BindFunc: bindArrayConcatAgg},
	{Name: "avg", BindFunc: bindAvg},
	{Name: "count", BindFunc: bindCount},
	{Name: "count_star", BindFunc: bindCountStar},
	{Name: "bit_and", BindFunc: bindBitAndAgg},
	{Name: "bit_or", BindFunc: bindBitOrAgg},
	{Name: "bit_xor", BindFunc: bindBitXorAgg},
	{Name: "countif", BindFunc: bindCountIf},
	{Name: "logical_and", BindFunc: bindLogicalAnd},
	{Name: "logical_or", BindFunc: bindLogicalOr},
	{Name: "max", BindFunc: bindMax},
	{Name: "min", BindFunc: bindMin},
	{Name: "string_agg", BindFunc: bindStringAgg},
	{Name: "sum", BindFunc: bindSum},

	// statistical aggregate functions
	{Name: "corr", BindFunc: bindCorr},
	{Name: "covar_pop", BindFunc: bindCovarPop},
	{Name: "covar_samp", BindFunc: bindCovarSamp},
	{Name: "stddev_pop", BindFunc: bindStddevPop},
	{Name: "stddev_samp", BindFunc: bindStddevSamp},
	{Name: "stddev", BindFunc: bindStddev},
	{Name: "var_pop", BindFunc: bindVarPop},
	{Name: "var_samp", BindFunc: bindVarSamp},
	{Name: "variance", BindFunc: bindVariance},

	// approximate aggregate functions
	{Name: "approx_count_distinct", BindFunc: bindApproxCountDistinct},
	{Name: "approx_quantiles", BindFunc: bindApproxQuantiles},
	{Name: "approx_top_count", BindFunc: bindApproxTopCount},
	{Name: "approx_top_sum", BindFunc: bindApproxTopSum},

	// hyperloglog++ functions
	{Name: "hll_count_init", BindFunc: bindHllCountInit},
	{Name: "hll_count_merge", BindFunc: bindHllCountMerge},
	{Name: "hll_count_merge_partial", BindFunc: bindHllCountMergePartial},
}

var windowFuncs = []*WindowFuncInfo{
	// aggregate functions
	{Name: "any_value", BindFunc: bindWindowAnyValue},
	{Name: "array_agg", BindFunc: bindWindowArrayAgg},
	{Name: "avg", BindFunc: bindWindowAvg},
	{Name: "count", BindFunc: bindWindowCount},
	{Name: "count_star", BindFunc: bindWindowCountStar},
	{Name: "countif", BindFunc: bindWindowCountIf},
	{Name: "logical_and", BindFunc: bindWindowLogicalAnd},
	{Name: "logical_or", BindFunc: bindWindowLogicalOr},
	{Name: "max", BindFunc: bindWindowMax},
	{Name: "min", BindFunc: bindWindowMin},
	{Name: "string_agg", BindFunc: bindWindowStringAgg},
	{Name: "sum", BindFunc: bindWindowSum},

	// statistical aggregate functions
	{Name: "corr", BindFunc: bindWindowCorr},
	{Name: "covar_pop", BindFunc: bindWindowCovarPop},
	{Name: "covar_samp", BindFunc: bindWindowCovarSamp},
	{Name: "stddev_pop", BindFunc: bindWindowStddevPop},
	{Name: "stddev_samp", BindFunc: bindWindowStddevSamp},
	{Name: "stddev", BindFunc: bindWindowStddev},
	{Name: "var_pop", BindFunc: bindWindowVarPop},
	{Name: "var_samp", BindFunc: bindWindowVarSamp},
	{Name: "variance", BindFunc: bindWindowVariance},

	// navigation functions
	{Name: "first_value", BindFunc: bindWindowFirstValue},
	{Name: "last_value", BindFunc: bindWindowLastValue},
	{Name: "nth_value", BindFunc: bindWindowNthValue},
	{Name: "lead", BindFunc: bindWindowLead},
	{Name: "lag", BindFunc: bindWindowLag},
	{Name: "percentile_cont", BindFunc: bindWindowPercentileCont},
	{Name: "percentile_disc", BindFunc: bindWindowPercentileDisc},

	// numbering functions
	{Name: "rank", BindFunc: bindWindowRank},
	{Name: "dense_rank", BindFunc: bindWindowDenseRank},
	{Name: "percent_rank", BindFunc: bindWindowPercentRank},
	{Name: "cume_dist", BindFunc: bindWindowCumeDist},
	{Name: "ntile", BindFunc: bindWindowNtile},
	{Name: "row_number", BindFunc: bindWindowRowNumber},
}

type NameAndFunc struct {
	Name string
	Func func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error)
}

type AggregateNameAndFunc struct {
	Name          string
	MakeAggregate func(ctx sqlite.FunctionContext) (sqlite.AggregateFunction, error)
}

var (
	funcMapMu          sync.RWMutex
	registerFuncOnce   sync.Once
	normalFuncMap      = map[string][]*NameAndFunc{}
	aggregateFuncMap   = map[string][]*AggregateNameAndFunc{}
	windowFuncMap      = map[string][]*AggregateNameAndFunc{}
	currentTimeFuncMap = map[string]struct{}{
		"current_date":      struct{}{},
		"current_datetime":  struct{}{},
		"current_time":      struct{}{},
		"current_timestamp": struct{}{},
	}
)

func RegisterFunctions() error {
	funcMapMu.RLock()
	defer funcMapMu.RUnlock()

	var onceErr error
	registerFuncOnce.Do(func() {
		for _, info := range normalFuncs {
			if err := setupNormalFuncMap(info); err != nil {
				onceErr = err
				return
			}
		}
		for _, info := range aggregateFuncs {
			if err := setupAggregateFuncMap(info); err != nil {
				onceErr = err
				return
			}
		}
		for _, info := range windowFuncs {
			if err := setupWindowFuncMap(info); err != nil {
				onceErr = err
				return
			}
		}
	})
	if onceErr != nil {
		return onceErr
	}

	if err := sqlite.RegisterFunction("zetasqlite_decode_array",
		&sqlite.FunctionImpl{
			Deterministic: true,
			NArgs:         -1,
			Scalar: func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				decoded, err := DecodeValue(args[0])
				if err != nil {
					return "", err
				}
				if decoded == nil {
					return "[]", nil
				}
				array, err := decoded.ToArray()
				if err != nil {
					return "", err
				}
				encodedValues := make([]interface{}, 0, len(array.values))
				for _, value := range array.values {
					v, err := EncodeValue(value)
					if err != nil {
						return "", err
					}
					encodedValues = append(encodedValues, v)
				}
				b, err := json.Marshal(encodedValues)
				if err != nil {
					return "", err
				}
				return string(b), err
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register decode_array function: %w", err)
	}

	if err := sqlite.RegisterFunction("zetasqlite_group_by", &sqlite.FunctionImpl{
		Deterministic: true,
		NArgs:         -1,
		Scalar: func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			decoded, err := DecodeValue(args[0])
			if err != nil {
				return "", err
			}
			if decoded == nil {
				return nil, nil
			}
			return decoded.Interface(), nil
		},
	}); err != nil {
		return fmt.Errorf("failed to register function zetasqlite_group_by: %w", err)
	}

	sqlite.MustRegisterCollationUtf8("zetasqlite_collate", func(a, b string) int {
		va, _ := DecodeValue(a)
		vb, _ := DecodeValue(b)
		eq, _ := va.EQ(vb)
		if eq {
			return 0
		}
		cond, _ := va.GT(vb)
		if cond {
			return 1
		}
		return -1
	})

	for _, values := range normalFuncMap {
		for _, v := range values {
			if err := sqlite.RegisterFunction(v.Name, &sqlite.FunctionImpl{
				Deterministic: true,
				NArgs:         -1,
				Scalar:        v.Func,
			}); err != nil {
				return fmt.Errorf("failed to register function %s: %w", v.Name, err)
			}
		}
	}
	for _, values := range aggregateFuncMap {
		for _, v := range values {
			if err := sqlite.RegisterFunction(v.Name, &sqlite.FunctionImpl{
				Deterministic: true,
				NArgs:         -1,
				MakeAggregate: v.MakeAggregate,
			}); err != nil {
				return fmt.Errorf("failed to register aggregate function %s: %w", v.Name, err)
			}
		}
	}
	for _, values := range windowFuncMap {
		for _, v := range values {
			if err := sqlite.RegisterFunction(v.Name, &sqlite.FunctionImpl{
				Deterministic: true,
				NArgs:         -1,
				MakeAggregate: v.MakeAggregate,
			}); err != nil {
				return fmt.Errorf("failed to register window function %s: %w", v.Name, err)
			}
		}
	}
	return nil
}

func setupNormalFuncMap(info *FuncInfo) error {
	normalFuncMap[info.Name] = append(normalFuncMap[info.Name], &NameAndFunc{
		Name: fmt.Sprintf("zetasqlite_%s", info.Name),
		Func: func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			values, err := convertArgs(args)
			if err != nil {
				return nil, err
			}
			ret, err := info.BindFunc(values...)
			if err != nil {
				return nil, err
			}
			return EncodeValue(ret)
		},
	})

	registerSafe := true
	if _, hasExplicitSafeFunction := normalFuncs[fmt.Sprintf("safe_%s", info.Name)]; hasExplicitSafeFunction {
		registerSafe = false
	}

	if registerSafe {
		normalFuncMap[info.Name] = append(normalFuncMap[info.Name], &NameAndFunc{
			Name: fmt.Sprintf("zetasqlite_safe_%s", info.Name),
			Func: func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				values, err := convertArgs(args)
				if err != nil {
					return nil, err
				}
				ret, err := info.BindFunc(values...)
				if err != nil {
					// Note, this should only suppress semantic errors based on the
					// input data. See
					// https://github.com/google/zetasql/blob/master/docs/resolved_ast.md#resolvedfunctioncallbase
					return nil, nil
				}
				return EncodeValue(ret)
			},
		})
	}
	return nil
}

func setupAggregateFuncMap(info *AggregateFuncInfo) error {
	aggregateFuncMap[info.Name] = append(aggregateFuncMap[info.Name], &AggregateNameAndFunc{
		Name:          fmt.Sprintf("zetasqlite_%s", info.Name),
		MakeAggregate: info.BindFunc(),
	})
	return nil
}

func setupWindowFuncMap(info *WindowFuncInfo) error {
	windowFuncMap[info.Name] = append(windowFuncMap[info.Name], &AggregateNameAndFunc{
		Name:          fmt.Sprintf("zetasqlite_window_%s", info.Name),
		MakeAggregate: info.BindFunc(),
	})
	return nil
}
