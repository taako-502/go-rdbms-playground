package migrations

import "github.com/taako-502/go-rdbms-playground/rdb"

func createUsersAgeIndex() rdb.Migration {
	return rdb.Migration{Version: 202611140002, Operations: []rdb.Operation{
		rdb.CreateIndexOp("users", "age"),
	}}
}
