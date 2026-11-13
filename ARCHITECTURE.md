# ディレクトリ構成

ソースコードは役割ごとにディレクトリを分ける。実行時のDBファイルは、指定した保存先の直下にフラットに配置する。

## リポジトリ

| パス | 役割 |
| --- | --- |
| `go.mod` | モジュール名とGoバージョン |
| `README.md` | 概要・仕様・使い方 |
| `AGENTS.md` | リポジトリで作業するエージェント向けの指示 |
| `ARCHITECTURE.md` | ソースと保存データの配置 |
| `rdb/` | 公開パッケージ。DB本体とテスト |
| `rdb/testdata/legacy/` | 保存形式の互換性を確認するテスト用データ。実行中のDBではない |
| `cmd/explorer/` | ブラウザ用のローカルサーバーの起動処理 |
| `internal/explorer/` | 閲覧・Insert用のHTTP API。リポジトリ外からはimportできない |
| `internal/explorer/static/` | 画面のHTML・CSS・JavaScript。Goのバイナリへ埋め込む |
| `examples/basic/` | Go APIだけでDBを操作する使用例 |
| `data/` | Explorerのデフォルト保存先。実行時に作成し、Git管理から除外 |

`cmd/explorer/`がDBを開き、`internal/explorer/`へ渡す。画面のリクエストはHTTP APIを経由して`rdb/`の操作を呼び出す。
`examples/basic/`はHTTP APIを使わず、直接`rdb/`を呼び出す。

## DB本体：`rdb/`

以下のGoファイルは、すべて同じ`rdb`パッケージに属する。ファイルごとの役割分担であり、別パッケージではない。

| ファイル | 役割 |
| --- | --- |
| `db.go` | Open・Close、テーブル作成、RawTable、Insert、操作の排他制御 |
| `schema.go` | カラム型・スキーマ・行の検証、共通エラー |
| `typed.go` | `Table[T]`・型付きカラム・型付きクエリ、構造体と行の変換 |
| `query.go` | Raw APIの検索実行。全件走査とインデックス検索の選択 |
| `catalog.go` | テーブル定義とインデックス一覧の取得 |
| `storage.go` | JSON Linesの読み書き、バイトオフセットでの行取得、ファイル同期・置換 |
| `btree.go` | メモリ上のB-treeの検索・挿入・ノード分割 |
| `index.go` | インデックス作成、既存行からの構築 |
| `recovery.go` | インデックスの保存、起動時の照合・再構築 |
| `migration.go` | マイグレーションの適用、履歴保存、未完了処理の再開 |
| `*_test.go` | 単体・結合・復旧・コンパイル検証とベンチマーク |

## ブラウザ画面

| ファイル | 役割 |
| --- | --- |
| `cmd/explorer/main.go` | 保存先・ポートの設定、DBのOpen、空のDBへのサンプル作成、HTTPサーバー起動 |
| `internal/explorer/server.go` | テーブル一覧・行一覧・InsertのAPI、静的ファイル配信 |
| `internal/explorer/static/index.html` | テーブル一覧、行一覧、Insertフォームの画面構造 |
| `internal/explorer/static/style.css` | 画面のスタイルとレスポンシブ表示 |
| `internal/explorer/static/app.js` | API通信、スキーマに応じた入力欄の生成、保存後の一覧更新 |

## 保存データ：`data/`

**1つの保存先ディレクトリが1つのDBに対応する。テーブルごとのサブディレクトリは作らない。**

次は、`users`と`products`という2つのテーブルを持つ場合の配置例。
`products`は説明用であり、Explorerが自動作成するテーブルではない。

| パス例 | 内容 | 更新方法 |
| --- | --- | --- |
| `data/catalog.json` | DB全体のテーブル・カラム・主キー・インデックス定義 | 定義変更時に置換 |
| `data/migrations.json` | 適用済みマイグレーションのバージョン・チェックサム | バージョン適用後に置換 |
| `data/users.jsonl` | usersの全行。1行につき1つのJSON | Insertごとに末尾へ追記 |
| `data/users.id.index.json` | users.idの主キーインデックス | usersへのInsertごとに全体を置換 |
| `data/users.age.index.json` | users.ageの通常インデックス | usersへのInsertごとに全体を置換 |
| `data/products.jsonl` | productsの全行 | Insertごとに末尾へ追記 |
| `data/products.id.index.json` | products.idの主キーインデックス | productsへのInsertごとに全体を置換 |

ファイル名の規則は、行データが`<table>.jsonl`、インデックスが`<table>.<column>.index.json`。
インデックスにはキーと行データのバイトオフセットを保存し、該当位置から行を読む。

- **行が増えても、行ごとのファイルは増えない。** 既存のJSON Linesファイルが大きくなる。
- テーブルを追加すると、行ファイルと主キーインデックスが増える。通常インデックスを追加すると、そのカラムのインデックスファイルが増える。
- `migrations.json`はマイグレーションを適用した場合に作成する。Explorerのサンプル作成は直接テーブルを作るため、このファイルがなくても正常。
- 保存・置換中は同じディレクトリに`.pending-*`という一時ファイルを作る。通常は置換または後片付けで消えるが、プロセスの強制終了後に残る場合がある。行データとしては使用しない。
- 行データを正とし、起動時にインデックスを照合する。不整合なら再構築し、書きかけの末尾行は除去する。

## 保存先の指定

`data/`という名前は固定ではない。Explorerの`-data`、またはGo APIの`rdb.Open(path)`で指定する。
相対パスは起動時のカレントディレクトリを基準とする。

```sh
# デフォルト：./data
go run ./cmd/explorer

# 別の保存先にあるDBを開く。サンプルは作成しない
go run ./cmd/explorer -data /path/to/my-db -demo=false
```

`examples/basic/`は一時ディレクトリを使用して終了時に削除するため、`data/`には保存しない。
