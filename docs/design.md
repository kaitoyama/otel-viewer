# otel-viewer — 設計判断

このドキュメントは「なぜこういう構成なのか」を残すためのものです。実装の詳細は
コードを直接読んでください。

## なぜ専用ビューワーを作るのか

LLM エージェントの開発で、本ユーザーが直面している中核課題:

- モデル切替時(例: opus → haiku → 新世代モデル)に「全体スコア」だけでは
  劣化を見落とす。**各エージェントステップ単位**でツール呼び出し・思考・
  最終出力を並べたい。
- 観察したい次元が「実行時刻」だけではない。**ツール X が特定の出力をした
  trace 群、フィードバックがネガティブな trace 群、同じタスクを処理した
  異なるモデルの入出力一覧、エージェントが取った分岐パターンの分布**
  — こうした多次元観察ができないと改善の打ち手が見えない。
- 「裏でモデル B を試した結果」と本番 run を並べて見たい。ユーザー側で
  シャドウ実行は別途行うので、ビューワー側は `replay_of` / `replay_group_id`
  属性を**認識して可視化**するだけでよい。

既存ツールの空白:

- **Tempo + Grafana** はトレース表示には強いが、ステップ整列差分や集約フロー
  グラフはできない。
- **Langfuse / Phoenix / LangSmith / Braintrust** は LLM 観察に特化しているが、
  ステップ整列差分・タスク横断グルーピング・集約フローグラフは商用にも
  実装されていない。

## ストレージは DuckDB 単独

最初は「Tempo を主にして DuckDB を後付け」のハイブリッドを考えたが、Tempo の
**標準化された(GA)機能**だけで本プロジェクトの要件を満たせるか精査した結果、
DuckDB は不可避だと判断した。

| 必要な操作 | TraceQL(GA) | DuckDB |
|---|---|---|
| 単一 trace 取得・基本属性での絞り込み | ✓ | ✓ |
| trace 一覧の時間窓検索 | ✓ | ✓ |
| 1 trace 内のスパン関係(`>>` 等) | ✓ | △(JOIN で代替) |
| per-trace の `count() / avg() / sum()` | ✓ | ✓ |
| **クロストレース集計**(フローグラフ・頻度テーブル) | ✗ | ✓ |
| **外部由来のユーザ生成データの保存・JOIN**(フィードバック・手動グループ) | ✗ | ✓ |
| **プロンプトハッシュ等のキーでグルーピング** | ✗ | ✓ |
| 長文 attribute 値の効率検索 | △(regex 遅い) | △(LIKE) |
| 属性組み合わせでの頻度集計テーブル | △(TraceQL Metrics preview) | ✓ |

特に「**Tempo は write-once**」なので、後から付与するフィードバックや手動
タスク分類を保存する場所がない。仮にハイブリッドにしても結局 DuckDB が
必要になる以上、Tempo を query エンジンとして併用する複雑性は割に合わない。

**結論: storage は DuckDB 単独**。Tempo は「履歴データを DuckDB に取り込む
**ingest ソースの一つ**」として `/api/search` と `/api/traces/{id}` の GA
エンドポイントのみ使う。TraceQL Metrics、metrics-generator のカスタム span-
to-metric ルール、`compare()` 関数、その他 preview/experimental 機能には
依存しない。

Tempo 接続は optional。OTLP/HTTP push receiver と JSONL アップロードだけでも
ビューワーは完結する。

## 取り込みの 3 経路

1. **OTLP/HTTP receiver** (`POST /v1/traces`) — 本番アプリの SDK から直接、
   または OTel Collector の otlphttp exporter から。Replay のシャドウ実行
   もここに到達する。
2. **JSONL アップロード** (`POST /api/traces/upload`) — Collector の file
   exporter 出力 (NDJSON、各行が TracesData)。
3. **Tempo pull** (`POST /api/datasources/tempo/backfill`) — 既存 Tempo に
   蓄積された履歴の取り込み。時間窓と任意の TraceQL フィルタを指定する
   非同期ジョブ。

正規化レイヤは OTel GenAI semconv と OpenInference の両方を共通モデルに写像する。

## ステップ整列(Compare の核)

**Needleman–Wunsch グローバル整列**を採用。モデル名は差し替え対象なので
アンカーに使えないため、シグネチャは:

- `PlannerLLM` → `"P"`
- `ToolCall(name=X)` → `"T:X"`
- `AgentInvoke(name=X)` → `"A:X"`

スコア: 同一シグネチャ +3、`P↔P` +2、ツール名違い -2、gap -1。
O(n·m) で n,m は数百以下なので問題なし。

N=2..4 run は最左へのペアワイズ整列 → 列マージ。意味的(埋め込み)整列は
v1 非採用(必要なら v2 でオプション化)。

## Task ID 解決の 5 経路

優先度順:
1. `gen_ai.task.id` 属性(spec で議論中だが honor する)
2. `gen_ai.task.name` 属性 → `name:<value>`
3. `gen_ai.conversation.id` 属性 → `conv:<value>`
4. プロンプト SHA-256 ハッシュ → `ph:<head16>`
5. UI 手動グルーピング(他経路を上書きする)

これで OTel native の属性が付いていない既存のトレースでも、プロンプト形状
だけで自然なグルーピングができる。

## なぜ DuckDB の PRIMARY KEY を外しているか

DuckDB の ART インデックスは PRIMARY KEY 列に対する UPDATE/DELETE で「同じキー
が既に存在する」と誤検知することがあり(known limitation)、本プロジェクトの
upsert ワークフローと噛み合わない。

代わりに、全テーブルで PK を持たず非ユニーク INDEX のみ。`UpsertRun` の
DELETE+INSERT パターンと、その他のアップサート用ヘルパが uniqueness を担保
する。これによりトレースの再取り込み(同じ trace_id を OTLP / Tempo 双方
から受信)が安全になり、UPDATE 系操作も自由に書ける。

## v1 で**入れていない**もの(意図的に)

- 集約フローグラフの**専用ページ** — データレイヤ(`/api/analytics/flow`)は
  実装済みだが、Sankey/react-flow 描画は v1.1。データはすでに取れているので
  実装は UI 追加のみ。
- Replay groups の**専用ページ** — `replay_group_id` はデータモデル・テーブル
  に格納されており、`/traces?filter` 経由でフィルタできる。専用 UI は v1.1。
- 認証 UI — v1 は API token のみ(`Authorization: Bearer`)、ローカル運用前提。
  OIDC / Basic は v1.1。
- 意味的(埋め込み)ステップ整列 — シグネチャベースで十分なケースが多く、
  埋め込みベースは v2 でオプション化。
