// Package migrations はExplorerサンプルのスキーマ変更を管理する。
package migrations

import (
	"context"
	"github.com/taako-502/go-rdbms-playground/rdb"
)

// All は適用済みを含む全定義を返す。新しいファイルを追加したらここに登録する。
func All() []rdb.Migration {
	return []rdb.Migration{createUsers(), createUsersAgeIndex()}
}

// Apply は未適用の変更を適用し、適用済みの定義が変更されていないか検証する。
func Apply(ctx context.Context, db *rdb.DB) error {
	return db.Migrate(ctx, All()...)
}
