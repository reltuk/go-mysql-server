package plan

import (
	"fmt"
	"github.com/dolthub/go-mysql-server/sql"
)

// TableCopier is used into case 1. CREATE TABLE SELECT 2) INSERT INTO <Empty Table> SELECT *
type TableCopier struct {
	sql.Node

	source sql.Node
	destination sql.Node
	db sql.Database
	options CopierProps
}

var _ sql.Databaser = (*TableCopier)(nil)
var _ sql.Node = (*TableCopier)(nil)

type CopierProps struct {
	replace bool
	ignore bool
}

func NewTableCopierWithCreate(db sql.Database, createTableNode sql.Node, source sql.Node, prop CopierProps) *TableCopier {
	return &TableCopier{
		source: source,
		destination: createTableNode,
		db: db,
		options: prop,
	}
}

func NewCopierWithInsert(db sql.Database, destination *ResolvedTable, source sql.Node) *TableCopier {
	return &TableCopier{
		db: db,
		source: source,
		destination: destination,
	}
}

func (tc *TableCopier) WithDatabase(db sql.Database) (sql.Node, error) {
	ntc := *tc
	ntc.db = db
	return &ntc, nil
}

func (tc *TableCopier) Database() sql.Database {
	return tc.db
}

func (tc *TableCopier) RowIter(ctx *sql.Context, row sql.Row) (sql.RowIter, error) {
	// If the source is a Create Table go ahead and create the table.
	if _, ok := tc.destination.(*CreateTable); ok {
		return tc.processCreateTable(ctx, row)
	}

	drt, ok := tc.destination.(*ResolvedTable)
	if !ok {
		return nil, fmt.Errorf("TableCopier only accepts CreateTable or ResolvedTable as the destination")
	}

	return tc.copyTableOver(ctx, tc.source.Schema()[0].Source, drt.Name())
}

func (tc *TableCopier) processCreateTable(ctx *sql.Context, row sql.Row) (sql.RowIter, error) {
	ct := tc.destination.(*CreateTable)

	_, err := ct.RowIter(ctx, row)
	if err != nil {
		return sql.RowsToRowIter(), err
	}

	table, tableExists, err := tc.db.GetTableInsensitive(ctx, ct.Name())
	if err != nil {
		return sql.RowsToRowIter(), err
	}

	if !tableExists {
		return sql.RowsToRowIter(), fmt.Errorf("error: Newly created table does not exist")
	}

	if tc.createTableSelectCanBeCopied(table) {
		return tc.copyTableOver(ctx, tc.source.Schema()[0].Source, table.Name())
	}

	ii := NewInsertInto(tc.db, NewResolvedTable(table, tc.db, nil), tc.source, tc.options.replace, nil, nil, tc.options.ignore)

	// Wrap the insert into a row update accumulator
	roa := NewRowUpdateAccumulator(ii, UpdateTypeInsert)

	return roa.RowIter(ctx, row)
}

// createTableSelectCanBeCopied determines whether the newly created table's data can just be copied from the source table
func (tc *TableCopier) createTableSelectCanBeCopied(tableNode sql.Table) bool {
	// The differences in LIMIT between integrators prevent us from using a copy
	if _, ok := tc.source.(*Limit); ok {
		return false
	}

	// If the DB does not implement the TableCopierDatabase interface we cannot copy over the table.
	if _, ok := tc.db.(sql.TableCopierDatabase); !ok {
		return false
	}

	// If there isn't a match in schema we cannot do a direct copy.
	sourceSchema := tc.source.Schema()
	tableNodeSchema := tableNode.Schema()

	if len(sourceSchema) != len(tableNodeSchema) {
		return false
	}

	for i, sn := range sourceSchema {
		if sn.Name != tableNodeSchema[i].Name {
			return false
		}
	}

	return true
}

// copyTableOver is used with queries of the form CREATE TABLE [] AS SELECT * ... as we can guarantee the new
// table will have the exact same row data as the source data.
func (c *TableCopier) copyTableOver(ctx *sql.Context, sourceTable string, destinationTable string) (sql.RowIter, error) {
	db, ok := c.db.(sql.TableCopierDatabase)
	if !ok {
		return sql.RowsToRowIter(), sql.ErrTableCopyingNotSupported.New()
	}

	rowsUpdated, err := db.CopyTableData(ctx, sourceTable, destinationTable)
	if err != nil {
		return sql.RowsToRowIter(), err
	}

	return sql.RowsToRowIter([]sql.Row{sql.Row{sql.OkResult{RowsAffected: rowsUpdated, InsertID: 0, Info: nil}}}...), nil
}


func (tc *TableCopier) Schema() sql.Schema {
	return tc.destination.Schema()
}

func (tc *TableCopier) Children() []sql.Node {
	return nil
}

func (tc *TableCopier) WithChildren(children ...sql.Node) (sql.Node, error) {
	return tc, nil
}

func (tc *TableCopier) Resolved() bool {
	return tc.source.Resolved()
}
