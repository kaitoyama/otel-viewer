# otel-viewer

LLM エージェント評価向け OpenTelemetry ビューワー。

モデル A→B→C のような切替時に、全体だけでなく**各エージェントステップ単位**で挙動の劣化を発見したい、ツール出力やフィードバックで多次元に絞り込んで観察したい、シャドウ実行(replay)を本番と並べて比較したい、というユースケースに焦点を当てる。

## 主要機能

- **ステップ整列比較**: 2–4 トレースをツール名アンカーの Needleman–Wunsch で揃え、各ステップの prompt / output / tool args を side-by-side で diff
- **タスク横断グルーピング**: `gen_ai.task.id` / `gen_ai.task.name` / `gen_ai.conversation.id` / プロンプトハッシュ / UI 手動指定の 5 経路でタスクを束ね、同タスクのモデル別 run を集約
- **集約フローグラフ**: 多数の trace を横断して `(agent, operation, tool)` の遷移を集計し、Sankey とノードグラフで描画
- **多次元フィルタ**: model / task / deploy_id / フィードバックスコア / ツール出力テキスト contains などを組み合わせて run をドリルダウン
- **フィードバック取り込み**: `POST /api/feedback` と OTel イベント `gen_ai.feedback` の双方
- **Replay の可視化**: `replay_of` / `replay_group_id` 属性を持つ trace を本番 run と並べて表示

## アーキテクチャ

- バックエンド: Go(`go-chi`, DuckDB 埋め込み)
- フロント: React + TypeScript + Vite、`go:embed` で単一バイナリ
- ストレージ: DuckDB 単独
- 取り込み: OTLP/HTTP receiver + JSONL アップロード + Tempo pull(GA エンドポイントのみ)

設計判断の詳細は `docs/design.md` を参照。

## クイックスタート

```bash
# ビルド(SPA → Go バイナリへ埋め込み、bin/otel-viewer)
make build

# 起動
./bin/otel-viewer -addr :8080 -db local.duckdb

# 別ターミナルで合成トレースを投入(opus/haiku を同一タスクで実行、replay
# 連携、フィードバック、ツールエラーを含む)
./bin/demo-agent -endpoint http://localhost:8080/v1/traces

# ブラウザで開く
open http://localhost:8080
```

`bin/demo-agent` は実際の LLM API は叩かず、OTel GenAI semconv に沿った
合成 trace を作って OTLP/HTTP receiver に投入する。UI で:

- `/traces` でモデル別の run 一覧、複数選択 → Compare
- `/tasks` で同一タスクのモデル別集約 → 任意の 2..4 を選んで Compare
- `/analytics/frequency` でツール呼び出しの成功率や遅延分布
- `/feedback` でスコア閾値以下の run の triage

を確認できる。

## 開発

```bash
make dev           # Go (8080) + Vite (5173) を起動
make test          # Go + Vitest
make build         # 単一バイナリを bin/otel-viewer に生成
```
