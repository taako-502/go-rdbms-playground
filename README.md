# go-rdbms-playground
リレーショナルデータベース作ってみた

## 内容
- Embedded Database（組み込み型データベース）
- RDB
- インデックス
  - B-tree
  - 単一カラムのみ。主キーには自動作成。
- 主キー: 必須、単一カラム。値は利用側が指定し、重複はエラー
- 型は整数、文字列、真偽値のみ。NULLは非対応。
- SQLは利用しない
  - migration
    - `CreateTable`と`CreateIndex`をサポートする。
    - バージョン順に適用し、適用履歴をファイルに保存。
  - query: データベースから取得したデータは.netにおけるLINQのような構文でデータを取得できるようにする。
  - command: `Insert`のみサポートします。
- データの実体
  - ファイルに保存。バイナリー化しない。
  - 行データはJSON Linesで保存。
  - B-tree もデータとしてファイルに書き出す。
- 同時アクセス: 1プロセスのみ。同時操作はロックで直列化

## 実装対象

Query系関数の実装対象
- Where
- Equal
- All

Command系関数の実装対象
- Insert

## Query 発行イメージ

`Where`関数のみサポートする。比較演算子は`Equal`のみサポートする。

```go
// こちらも設計イメージ
query := users.Where(Equal(UserAge, 20))
result, err := query.All(ctx)
```

`And`、`Or`、`Join`、`GroupBy`なども対応しません。
