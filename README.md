# go-rdbms-playground

Goで作る、学習用の組み込み型リレーショナルデータベース。
SQLを使わず、型付きのGo APIで挿入・検索する。

## 仕様

- Go 1.27以上。外部ライブラリは不要。
- 型は`int64`・`string`・`bool`。NULLは非対応。
- 主キーは必須・単一カラム。値は利用側が指定し、重複はエラー。
- 挿入は`Insert`、検索は`Where`・`Equal`・`All`に対応。
- `Where`の重複呼び出しはエラー。検索結果がない場合は空スライスを返し、並び順は保証しない。
- インデックスは単一カラムのB-tree。主キーには自動作成し、通常のインデックスは同値の複数行を許可する。検索時は利用可能なインデックスがあれば使用する。
- 行データはJSON Lines、定義・インデックス・マイグレーション履歴はJSONで保存。整数の精度を維持する。
- 1プロセスで使用し、同時操作はロックで直列化する。同じDBディレクトリの重複Openは不可。
- 更新・削除、`And`・`Or`・`Join`・`GroupBy`、複数操作のトランザクションは非対応。

## 実行方法

ブラウザで行の閲覧・追加や、テーブル定義・インデックス定義・B-treeの確認を行う場合：

```sh
go run ./cmd/explorer
```

[http://127.0.0.1:8080](http://127.0.0.1:8080)を開く。データは`./data`に保存され、再起動後も残る。
「ランダム入力」で行追加フォームにサンプル値を入力し、「行を保存」で追加できる。
空のDBにはサンプルの`users`テーブルを作成する。既存DBを開く場合は`-data /path/to/db -demo=false`、ポート変更は`-port 8081`を指定する。

Go APIの使用例のみ実行する場合：

```sh
go run ./examples/basic
```

[使用例](examples/basic/main.go)は、テーブル作成・挿入・検索・再起動後の取得を実行する。
DBは一時ディレクトリに作成し、終了時に削除する。

## 型付きAPI

作成済みの`users`テーブルを操作する例。テーブル作成を含む全体のコードは[使用例](examples/basic/main.go)を参照。

```go
type User struct {
    ID   int64  `rdb:"id"`
    Name string `rdb:"name"`
    Age  int64  `rdb:"age"`
}

users, err := db.Table[User]("users")
if err != nil { return err }

err = users.Insert(ctx, User{ID: 1, Name: "Alice", Age: 20})
if err != nil { return err }

age, err := users.Column[int64]("age")
if err != nil { return err }

rows, err := users.Where(rdb.Equal(age, 20)).All(ctx) // []User
if err != nil { return err }
```

- importパスは`github.com/taako-502/go-rdbms-playground/rdb`。
- モデルはフラットな構造体。`rdb:"column"`でカラムを指定し、省略時はGoのフィールド名を使用する。`rdb:"-"`で除外できる。
- 対応するフィールド型は`int64`・`string`・`bool`と、それらを基底型とする名前付き型。埋め込み・ポインタ・非公開フィールドは除外指定がない限り使用できない。
- `Table[T]`取得時に保存済みスキーマとの一致を検証する。`Column[V]`にはモデルのフィールドと同じGo型を指定する。
- 比較値・挿入モデル・条件モデルの型違いはコンパイルエラー。別DB・別テーブルのフィールド流用は実行時エラー。
- `users.All(ctx)`で全件取得。動的なマップ操作には`db.RawTable(name)`と`rdb.RawEqual(name, value)`を使用する。

## マイグレーション

`db.Migrate(ctx, migrations...)`に、`CreateTableOp`・`CreateIndexOp`を含むバージョン付きの定義を渡す。
適用済みを含む全バージョンを毎回指定し、適用済みの定義は変更しない。

バージョン順に実行し、変更の保存後に履歴を記録する。途中終了した場合は実際の定義と照合して再開し、不一致はエラーにする。
`db.CreateTable`・`db.CreateIndex`を直接呼び出すこともできる。

## 保存と復旧

- `Insert`は行データと全インデックスの`Sync`完了後に成功を返す。
- インデックスは行のバイトオフセットを保持する。挿入ごとにB-tree全体を一時ファイルへ保存・同期して置き換えるため、書き込み量はデータ量とともに増える。
- 行データを正とし、起動時に全走査してインデックスと照合する。不整合・欠損・破損したインデックスは再構築する。
- 途中まで書かれた末尾行は除去する。改行済みの不正な行や重複主キーなどはエラーにする。
- 書き込み失敗後は`Close`して`Open`し直す。`Insert`がエラーでも行が残る場合があるため、再試行前に主キーで存在確認する。ロールバックは保証しない。
- インデックス全体と検索結果をメモリに保持する。起動時の全走査と挿入ごとの全インデックス保存を前提とした学習用実装。

## 開発・検証

ソースと保存データの配置は[ARCHITECTURE.md](ARCHITECTURE.md)を参照。

```sh
go test -race ./...
go vet ./...
go test ./rdb -run TestProcessCrashRecovery -v
go test ./rdb -run '^$' -bench 'Benchmark(Lookup|Insert)' -benchmem -benchtime=20x
```
