# ディレクトリ構成

```text
go-rdbms-playground/
├── cmd/                    # 実行プログラムのエントリーポイント
│   ├── migrate/            # 日時付きマイグレーションを適用
│   └── explorer/           # DBを開き、ローカルHTTPサーバーを起動
├── internal/               # 外部からimportできない内部パッケージ
│   └── explorer/           # テーブル閲覧・InsertのHTTP APIと静的ファイル配信
│       └── static/         # ブラウザ画面のHTML・CSS・JavaScript
├── migrations/             # 日時付きのGo定義と登録一覧
├── rdb/                    # 公開パッケージ：DB本体、テスト、ベンチマーク
│   └── testdata/           # テスト用の固定データ
│       └── legacy/         # 保存形式の互換性を確認するDBデータ
├── examples/               # ライブラリの使用例
│   └── basic/              # Go APIによる作成・挿入・検索・再起動の使用例
└── data/                   # 実行時のDB保存先（デフォルト）。Git管理対象外
```

## ディレクトリ間の関係

- `cmd/explorer/`が`rdb/`でDBを開き、`internal/explorer/`へ渡す。
- `internal/explorer/static/`の画面はHTTP APIを通じて`rdb/`を操作する。画面のファイルはGoのバイナリに埋め込む。
- `examples/basic/`はHTTP APIを使わず、直接`rdb/`を操作する。

## データの配置

`data/`は1つのDBに対応し、テーブルごとのサブディレクトリは作らない。
定義・行データ・インデックス・マイグレーション履歴を、その直下にフラットに保存する。
行を追加すると既存の行ファイルが大きくなり、テーブルやインデックスを追加するとファイル数が増える。

保存先はExplorerの`-data`または`rdb.Open(path)`で変更できる。相対パスは起動時のカレントディレクトリが基準。
`rdb/testdata/`はテスト専用で、実行中のDBとは別。`examples/basic/`は一時ディレクトリを使い、終了時に削除する。

起動方法と保存・復旧の仕様は[README.md](README.md)を参照。
