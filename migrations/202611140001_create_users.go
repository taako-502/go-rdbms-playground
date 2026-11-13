package migrations

import "github.com/taako-502/go-rdbms-playground/rdb"

func createUsers() rdb.Migration {
	return rdb.Migration{Version: 202611140001, Operations: []rdb.Operation{
		rdb.CreateTableOp(rdb.Schema{Name: "users", Columns: []rdb.Column{
			{Name: "id", Type: rdb.Integer}, {Name: "name", Type: rdb.String},
			{Name: "age", Type: rdb.Integer}, {Name: "active", Type: rdb.Boolean},
		}, PrimaryKey: "id"}),
	}}
}
