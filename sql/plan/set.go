package plan

import (
	"fmt"
	"strings"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
)

// Set represents a set statement. This can be variables, but in some instances can also refer to row values.
type Set struct {
	Exprs []sql.Expression
}

// NewSet creates a new Set node.
func NewSet(vars []sql.Expression) *Set {
	return &Set{Exprs: vars}
}

// Resolved implements the sql.Node interface.
func (s *Set) Resolved() bool {
	for _, v := range s.Exprs {
		if !v.Resolved() {
			return false
		}
	}
	return true
}

// Children implements the sql.Node interface.
func (s *Set) Children() []sql.Node { return nil }

// WithChildren implements the sql.Node interface.
func (s *Set) WithChildren(children ...sql.Node) (sql.Node, error) {
	if len(children) != 0 {
		return nil, sql.ErrInvalidChildrenNumber.New(s, len(children), 0)
	}

	return s, nil
}

// WithExpressions implements the sql.Expressioner interface.
func (s *Set) WithExpressions(exprs ...sql.Expression) (sql.Node, error) {
	if len(exprs) != len(s.Exprs) {
		return nil, sql.ErrInvalidChildrenNumber.New(s, len(exprs), len(s.Exprs))
	}

	return NewSet(exprs), nil
}

// Expressions implements the sql.Expressioner interface.
func (s *Set) Expressions() []sql.Expression {
	return s.Exprs
}

// RowIter implements the sql.Node interface.
func (s *Set) RowIter(ctx *sql.Context, row sql.Row) (sql.RowIter, error) {
	span, ctx := ctx.Span("plan.Set")
	defer span.Finish()

	var updateExprs []sql.Expression
	for _, v := range s.Exprs {
		setField, ok := v.(*expression.SetField)
		if !ok {
			return nil, fmt.Errorf("unsupported type for set: %T", v)
		}

		switch left := setField.Left.(type) {
		case *expression.SystemVar:
			_, err := setSystemVar(ctx, left, setField.Right, row)
			if err != nil {
				return nil, err
			}
		case *expression.UserVar:
			_, err := setUserVar(ctx, left, setField.Right, row)
			if err != nil {
				return nil, err
			}
		case *expression.GetField:
			updateExprs = append(updateExprs, setField)
		default:
			return nil, fmt.Errorf("unsupported type for set: %T", left)
		}
	}

	var resultRow sql.Row
	if len(updateExprs) > 0 {
		newRow, err := applyUpdateExpressions(ctx, updateExprs, row)
		if err != nil {
			return nil, err
		}
		copy(resultRow, row)
		resultRow = row.Append(newRow)
	}

	return sql.RowsToRowIter(resultRow), nil
}

func setUserVar(ctx *sql.Context, userVar *expression.UserVar, right sql.Expression, row sql.Row) (interface{}, error) {
	var (
		value interface{}
		err   error
	)

	var varName = userVar.Name

	value, err = right.Eval(ctx, row)
	if err != nil {
		return nil, err
	}

	// TODO: differentiate between system and user vars here
	err = ctx.Set(ctx, varName, right.Type(), value)
	if err != nil {
		return nil, err
	}

	return value, nil
}

func setSystemVar(ctx *sql.Context, sysVar *expression.SystemVar, right sql.Expression, row sql.Row) (interface{}, error) {
	var (
		value interface{}
		typ   sql.Type
		err   error
	)

	var varName = sysVar.Name

	// TODO: value checking for system variables. Each one has specific lists of acceptable values.
	value, err = right.Eval(ctx, row)
	if err != nil {
		return nil, err
	}
	typ = sysVar.Type()

	// TODO: differentiate between system and user vars here
	err = ctx.Set(ctx, varName, typ, value)
	if err != nil {
		return nil, err
	}

	return value, nil
}

// Schema implements the sql.Node interface.
func (s *Set) Schema() sql.Schema {
	return nil
}

func (s *Set) String() string {
	var children = make([]string, len(s.Exprs))
	for i, v := range s.Exprs {
		children[i] = fmt.Sprintf(v.String())
	}
	return strings.Join(children, ", ")
}

func (s *Set) DebugString() string {
	var children = make([]string, len(s.Exprs))
	for i, v := range s.Exprs {
		children[i] = fmt.Sprintf(sql.DebugString(v))
	}
	return strings.Join(children, ", ")
}
