# go-rdbms-playground
リレーショナルデータベース作ってみた

## 内容
- Embedded Database（組み込み型データベース）
- RDB
- インデックス
  - B-tree
  - 単一カラムのみ。主キーには自動作成。
  - 通常のインデックスは同じ値を持つ複数行を許可する。
  - 行の参照先には、JSON Linesファイル内のバイトオフセットを保持する。検索後は該当位置から行を読み込む。
- 主キー: 必須、単一カラム。値は利用側が指定し、重複はエラー
- 型は整数（Goの`int64`）、文字列、真偽値のみ。NULLは非対応。JSONの読み書きでも整数の精度を維持する。
- SQLは利用しない
  - migration
    - `CreateTable`と`CreateIndex`をサポートする。
    - バージョン順に適用し、適用履歴をファイルに保存。
  - query: .NETのLINQを参考に、GoのAPIで問い合わせを組み立てる。`All(ctx)`で実行し、利用可能なインデックスがあれば使用する。
  - command: `Insert`のみサポートする。`Insert(ctx, row)`の呼び出し時に挿入を実行する。
- データの実体
  - ファイルに保存。バイナリー化しない。
  - 行データはJSON Linesで保存。
  - B-tree もデータとしてファイルに書き出す。
- 同時アクセス: 1プロセスのみ。同時操作はロックで直列化

## 書き込みと復旧

- `Insert`は行データとインデックスの両方について、`Write`だけでなく`Sync`まで完了してから成功を返す。
- インデックスは、挿入ごとにB-tree全体をテキスト形式で一時ファイルへ保存・同期し、既存ファイルを置き換える方式とする。挿入ごとの書き込み量が大きくなることは許容する。
- 行データを正とし、起動時にインデックスの不整合を検知したら再構築する。途中まで書かれた末尾の行は復旧時に除去する。
- 行データの保存後、インデックスの保存が完了する前に失敗した場合、`Insert`がエラーを返しても再起動後にその行が存在することがある。エラー時に必ず未挿入となる保証や、行データとインデックスの変更をまとめてロールバックする保証は設けない。再試行する場合は、利用側で指定した主キーによって行の存在を確認する。
- マイグレーションは変更の保存後に適用履歴を記録する。途中終了した場合は、次回起動時に実際の定義を確認して未完了の処理を再開する。

## 実装対象

Query系関数の実装対象
- Where
- Equal
- All

Command系関数の実装対象
- Insert

## Query 発行イメージ

`Where`関数のみサポートする。比較演算子は`Equal`のみサポートする。
`Where`の重複呼び出しはエラーにする。検索結果がない場合は空スライスを返し、結果の並び順は保証しない。

```go
// UserAgeはusers.Column[int64]("age")で取得した型付きカラム
query := users.Where(rdb.Equal(UserAge, 20))
result, err := query.All(ctx)
```

`And`、`Or`、`Join`、`GroupBy`なども対応しません。


## ディレクトリ構成

- `rdb/`: データベース本体、単体・復旧テスト、ベンチマーク
- `examples/basic/`: 実行可能な使用例
- ルート: `go.mod`、README、ライセンスなどのプロジェクト設定・文書

## 実行方法

Go 1.27以上（`go.mod`: `go 1.27.0`）。外部ライブラリは不要。

```sh
go run ./examples/basic
go test ./...
go test -race ./...
go vet ./...
```

使用例は一時ディレクトリにDBを作成し、終了時に削除する。

```text
age=20: 2 rows
  Alice (20)
  Bob (20)
duplicate primary key: rejected
after reopen: 3 rows
```

## APIの使用例

```go
import (
    "context"
    rdb "github.com/taako-502/go-rdbms-playground/rdb"
)

type User struct {
    ID   int64  `rdb:"id"`
    Name string `rdb:"name"`
    Age  int64  `rdb:"age"`
}

ctx := context.Background()
db, err := rdb.Open("./data")
if err != nil { return err }
defer db.Close()

err = db.Migrate(ctx,
    rdb.Migration{
        Version: 1,
        Operations: []rdb.Operation{
            rdb.CreateTableOp(rdb.Schema{
                Name: "users",
                Columns: []rdb.Column{
                    {Name: "id", Type: rdb.Integer},
                    {Name: "name", Type: rdb.String},
                    {Name: "age", Type: rdb.Integer},
                },
                PrimaryKey: "id",
            }),
        },
    },
    rdb.Migration{
        Version: 2,
        Operations: []rdb.Operation{rdb.CreateIndexOp("users", "age")},
    },
)
if err != nil { return err }

users, err := db.Table[User]("users")
if err != nil { return err }
err = users.Insert(ctx, User{ID: 1, Name: "Alice", Age: 20})
if err != nil { return err }

age, err := users.Column[int64]("age")
if err != nil { return err }

rows, err := users.Where(rdb.Equal(age, 20)).All(ctx) // []User
if err != nil { return err }
for _, user := range rows {
    _ = user.Name // string。型アサーション不要
}
```

- マイグレーションを使わず、`db.CreateTable(ctx, schema)`や`db.CreateIndex(ctx, table, column)`を直接呼び出すこともできる。
- `db.Table[T](name)`はGo 1.27のジェネリックメソッド。`Insert(ctx, value)`は`T`を受け付け、`All(ctx)`は空の場合も非nilの`[]T`を返す。
- `T`はフラットな構造体。公開フィールドに`rdb:"column"`タグを付ける。タグ省略時はGoのフィールド名をそのまま使用し、`rdb:"-"`で除外できる。埋め込み・ポインタ・非公開フィールドは除外指定がない限り拒否する。
- 対応するフィールド型は`int64`・`string`・`bool`およびそれらを基底型とする名前付き型。型付きAPIの整数には`int`を使わない。文字列は有効なUTF-8のみ受け付ける。
- `Table[T]`の取得時に、構造体と保存済みスキーマのカラム名・型・過不足を検証する。構造体と行の変換にはreflectionを使い、JSONへの再変換は挟まない。
- `users.Column[V](name)`もジェネリックメソッド。モデルのフィールドと完全に同じGo型（名前付き型も含む）を指定し、`Field[T, V]`を取得する。名前や型の不一致はここでエラーになる。
- `Equal(field, value)`の比較値は`V`、`Where`の条件は同じモデル`T`に限定される。別モデルの行・条件や、整数カラムと文字列の比較はコンパイルエラー。異なるDB・テーブルのフィールド流用は実行時に拒否する。
- スキーマ定義に使う`rdb.Column`と、クエリ用の`rdb.Field[T,V]`は別の型。
- 動的なマップ操作が必要な場合は`db.RawTable(name)`、`RawEqual(name, value)`を使用する。`Row`は`map[string]any`で、整数入力は`int64`と`int`、結果は`int64`。旧APIの`db.Table(name)`は`db.RawTable(name)`へ、文字列カラム指定の`Equal`は`RawEqual`へ変更する。
- 行は定義されたカラムを過不足なく持つ必要がある。NULL・型変換・デフォルト値は非対応。
- テーブル名とカラム名は`[A-Za-z_][A-Za-z0-9_]*`。インデックスはテーブル・カラムの組み合わせで識別する。
- `users.All(ctx)`で全件取得。`Where`は不変のQueryを返し、条件のエラーは`All(ctx)`で返す。
- `errors.Is`で`ErrDuplicate`、`ErrQuery`、`ErrNotFound`、`ErrNeedsRecovery`などを判定できる。
- コンテキストは操作開始時と走査中に確認する。ロック待ちやOSのファイル書き込み・同期自体は中断しない。行の書き込み開始後は整合性維持のためインデックス保存まで進める。

### コンパイル時に拒否される例

```go
users.Where(rdb.Equal(age, "20")) // ageはField[User, int64]なので文字列は渡せない
users.Insert(ctx, OtherModel{})  // Table[User]には別のモデルを渡せない
```

## Go 1.27への更新

- 型付きテーブルとカラムの取得にジェネリックメソッドを使用。
- JSON読み書きは`encoding/json/v2`と`encoding/json/jsontext`を使用。重複キーと不正なUTF-8は拒否し、整数は`int64`へ直接読み込む。
- 適用済みマイグレーションのチェックサム生成だけは、互換性維持のため従来の`encoding/json`によるバイト列を維持する。更新前のファイルを保存したfixtureで再Open・履歴照合・追記を検証する。
- 並行テストに`sync.WaitGroup.Go`、型付きエラー取り出しに`errors.AsType`、ベンチマークに`testing.B.Loop`を採用。これらはGo 1.27以前に導入された機能も含む。

## 保存形式と復旧の詳細

| ファイル | 内容 |
| --- | --- |
| `catalog.json` | テーブル・カラム・主キー・インデックスの定義 |
| `<table>.jsonl` | 行データ。1行1JSONで末尾に改行を付ける |
| `<table>.<column>.index.json` | B-treeのノード構造、キー、行のバイトオフセット |
| `migrations.json` | 適用済みバージョンと定義のチェックサム |

- B-treeの最小次数は8。メモリ上にツリー全体を持ち、通常インデックスの同値キーは複数のオフセットを保持する。整数は数値順、文字列はGoの文字列比較順、真偽値は`false < true`。
- 保存順序は「行の追記・Sync → 各インデックスの一時ファイル作成・Sync・rename・親ディレクトリSync」。定義と履歴も一時ファイルを同期して置き換える。
- 起動時には行を全走査して期待するインデックスを作り、保存済みツリーの構造・全キー・全オフセットを照合する。不一致・欠損・破損時に再構築する。起動はデータ量に比例した走査とツリー構築を伴う。
- 改行まで到達していない末尾の行を除去する。改行済みの不正な行、重複主キー、既存テーブルの行ファイル欠損などはエラーにし、黙ってデータを削除しない。
- 書き込み途中でI/Oエラーが起きたDBインスタンスは`ErrNeedsRecovery`となる。`Close`して`Open`し直し、主キーで存在確認してから再試行する。エラーを返したInsertの行が復旧後に存在する場合がある。
- マイグレーションには毎回、適用済みを含む全バージョンを渡す。入力順によらずバージョン順に実行し、適用済みの変更・削除や過去へのバージョン追加を拒否する。途中終了した操作は実際の定義が一致する場合に再利用し、不一致はエラーにする。
- 同一プロセスから同じパスを重複Openすることも拒否する。複数プロセス間の排他制御は実装していないため、同じDBディレクトリを複数プロセスで開かない。
- 全件取得・検索結果とインデックスはメモリ上に保持する。ページキャッシュ、WAL、複数操作のトランザクション、更新・削除は実装対象外。
- OSのファイル・ディレクトリ同期を利用するため、対応するローカルファイルシステムを前提とする。プロセス中断の復旧はテストするが、実機の電源断やハードウェア障害の耐久性は検証していない。

## 検証とベンチマーク

```sh
go test -cover ./...
go test ./rdb -run TestProcessCrashRecovery -v
# 初期行数100/1000、検索は1件ヒット、挿入は主キーのみ／通常インデックス追加で比較。
go test ./rdb -run '^$' -bench 'Benchmark(Lookup|Insert)' -benchmem -benchtime=20x
```

型付きAPIのテストでは、正しいコードのビルド成功と、比較値・挿入モデル・条件モデル・取得結果の型を間違えたコードのビルド失敗も確認する。

復旧テストは子プロセスを終了させ、行追記途中・行Sync後・インデックス一時ファイルSync後・rename後・マイグレーション変更後・履歴保存中の再起動を検証する。I/Oエラーを注入するテストでは、その後の操作拒否と再Openによる復旧も確認する。

ベンチマークは型付きAPIで実行し、`b.Loop`によって初期データ投入・起動時構築・モデルの結び付け・カラム取得を計測外にする。検索は同じカラム・条件について全件走査とインデックスを比較する。InsertはJSON Lines追記、Sync、B-tree全体保存、rename、ディレクトリSyncを含む。測定中は行数が増加するため、比較時は同じ`-benchtime=20x`を指定する。Goバージョン、OS、CPU、ファイルシステムに依存する結果として扱う。

### 更新前の測定例（参考）

以下は型付きAPI・JSON v2への移行前の測定値。Go 1.27.0 / darwin arm64 / Apple M4、`-benchtime=20x`。一時ディレクトリ上で実行し、ファイルシステム種別は未確認。短い単発測定であり、性能保証ではない。

| 操作 | 行数 | インデックス | 時間/操作 |
| --- | ---: | --- | ---: |
| 名前による検索 | 100 | なし（全件走査） | 0.318 ms |
| 名前による検索 | 100 | あり | 0.016 ms |
| 名前による検索 | 1,000 | なし（全件走査） | 1.292 ms |
| 名前による検索 | 1,000 | あり | 0.012 ms |
| Insert | 初期100 | 主キーのみ | 14.001 ms |
| Insert | 初期100 | 主キー＋名前 | 25.442 ms |
| Insert | 初期1,000 | 主キーのみ | 18.199 ms |
| Insert | 初期1,000 | 主キー＋名前 | 49.128 ms |

### 型付きAPI・JSON v2移行後の測定例

Go 1.27.0 / darwin arm64 / Apple M4、`-benchtime=20x`、`b.Loop`使用。初期データと検索条件は上記と同じだが、実行時の負荷や計測方式も影響するため、単発値から改善率は断定しない。

| 操作 | 行数 | インデックス | 時間/操作 |
| --- | ---: | --- | ---: |
| 名前による検索 | 100 | なし（全件走査） | 0.088 ms |
| 名前による検索 | 100 | あり | 0.0038 ms |
| 名前による検索 | 1,000 | なし（全件走査） | 1.708 ms |
| 名前による検索 | 1,000 | あり | 0.0048 ms |
| Insert | 初期100 | 主キーのみ | 14.996 ms |
| Insert | 初期100 | 主キー＋名前 | 24.132 ms |
| Insert | 初期1,000 | 主キーのみ | 15.996 ms |
| Insert | 初期1,000 | 主キー＋名前 | 28.960 ms |
