# go-rdbms-playground
リレーショナルデータベース作ってみた

## 内容
- Embedded Database（組み込み型データベース）
- RDB
- B-tree インデックス 対応
- SQLは利用しない。データベースから取得したデータは.netにおけるLINQのような構文でデータを取得できるようにする。


### SQLは利用しないについて

`Where`関数のみサポートする。関係演算子は`Equal`のみサポートする。

```go
// こちらも設計イメージ
query := users.Where(Equal(UserAge, 20))
result, err := query.All(ctx)
```