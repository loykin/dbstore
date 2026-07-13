package mcpserver

import (
	"context"
	"fmt"
)

type Operation string

const (
	OperationListSources   Operation = "list_sources"
	OperationPing          Operation = "ping"
	OperationListTables    Operation = "list_tables"
	OperationDescribeTable Operation = "describe_table"
	OperationQuery         Operation = "query"
	OperationRegister      Operation = "register_source"
	OperationRemove        Operation = "remove_source"
)

type Policy interface {
	Authorize(ctx context.Context, operation Operation, source string) error
}

type PolicyFunc func(context.Context, Operation, string) error

func (f PolicyFunc) Authorize(ctx context.Context, operation Operation, source string) error {
	return f(ctx, operation, source)
}

// InspectionPolicy permits source and schema inspection, but denies arbitrary
// SQL queries and source lifecycle mutations.
type InspectionPolicy struct{}

func (InspectionPolicy) Authorize(_ context.Context, operation Operation, _ string) error {
	switch operation {
	case OperationListSources, OperationPing, OperationListTables, OperationDescribeTable:
		return nil
	default:
		return fmt.Errorf("mcpserver: operation %q is disabled by the inspection policy", operation)
	}
}

// CapabilityPolicy is a static policy for trusted local deployments. Query
// means SQL SELECT syntax is accepted; it does not prove the statement is free
// of side effects, so use a database account with appropriately narrow grants.
type CapabilityPolicy struct {
	AllowQuery  bool
	AllowManage bool
}

func (p CapabilityPolicy) Authorize(_ context.Context, operation Operation, _ string) error {
	switch operation {
	case OperationListSources, OperationPing, OperationListTables, OperationDescribeTable:
		return nil
	case OperationQuery:
		if p.AllowQuery {
			return nil
		}
	case OperationRegister, OperationRemove:
		if p.AllowManage {
			return nil
		}
	}
	return fmt.Errorf("mcpserver: operation %q is disabled by capability policy", operation)
}

// QueryPolicy permits inspection and SQL SELECT tools, but denies lifecycle
// management. A read-only database credential remains the security boundary.
type QueryPolicy struct{}

func (QueryPolicy) Authorize(ctx context.Context, operation Operation, source string) error {
	return (CapabilityPolicy{AllowQuery: true}).Authorize(ctx, operation, source)
}

// AllowAllPolicy permits every operation. It is intended for trusted local
// deployments; remote servers should use an identity-aware Policy instead.
type AllowAllPolicy struct{}

func (AllowAllPolicy) Authorize(context.Context, Operation, string) error { return nil }
