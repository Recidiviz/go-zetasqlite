package internal

import (
	"context"

	"cloud.google.com/go/bigquery"
)

// Placeholder request/response types for the future go-googlesql client boundary.
// When github.com/vantaboard/go-googlesql/client lands, alias or replace these with
// the real types and wire AnalyzerBackend implementations.
type StatementAnalysisPayload struct{}

type ParseScriptRequest struct{}
type ParseScriptResponse struct{}
type AnalyzeStatementRequest struct{}

// StatementAnalysis is the minimum analysis payload go-googlesqlite needs from
// the new go-googlesql client boundary.
type StatementAnalysis struct {
	Statement *StatementAnalysisPayload
}

// AnalyzerBackend describes the new analysis seam. The existing in-process
// googlesql calls should move behind this interface so the package can switch to
// the embedded gRPC path without re-threading planner state everywhere.
type AnalyzerBackend interface {
	ParseScript(ctx context.Context, query string, req *ParseScriptRequest) (*ParseScriptResponse, error)
	AnalyzeStatement(ctx context.Context, query string, req *AnalyzeStatementRequest) (*StatementAnalysis, error)
}

type QueryParameterShape struct {
	Name string
	// StandardSQLDataType or REST parameter type once the client boundary is wired.
	Type *bigquery.StandardSQLDataType
}
