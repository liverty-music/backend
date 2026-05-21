# Gemini Concert Searcher — チューニング参考資料

公式ドキュメント・SDK ソース・Google 公式ブログから検証した「concert searcher のチューニングで役立つ情報」を集約したリファレンス。`internal/infrastructure/gcp/gemini/searcher.go` を触る際に読む前提。

**最終更新**: 2026-05-21 (Gemini 3 series GA、`google.golang.org/genai@v1.57.0` 基準)

---

## 目次

1. [Backend 選択 (Vertex AI vs Gemini API direct)](#1-backend-選択-vertex-ai-vs-gemini-api-direct)
2. [モデル比較 (Gemini 3 family)](#2-モデル比較-gemini-3-family)
3. [プロンプティング (Gemini 3 公式ベストプラクティス)](#3-プロンプティング-gemini-3-公式ベストプラクティス)
4. [Grounding ツール](#4-grounding-ツール)
5. [Generation config パラメータ](#5-generation-config-パラメータ)
6. [Response metadata (チューニングに使える観測データ)](#6-response-metadata-チューニングに使える観測データ)
7. [Go SDK 現状](#7-go-sdk-現状)
8. [運用上の制約](#8-運用上の制約)
9. [既知の落とし穴・既知問題](#9-既知の落とし穴-既知問題)
10. [References](#10-references)

---

## 1. Backend 選択 (Vertex AI vs Gemini API direct)

### 機能パリティ (concert search 関連、SDK コメント実機確認済み)

| 機能 | Vertex AI<br>(`BackendVertexAI`) | Gemini API direct<br>(`BackendGeminiAPI`) |
|---|:-:|:-:|
| Gemini 3 series モデル | ✅ | ✅ |
| GoogleSearch grounding | ✅ | ✅ |
| URLContext tool | ⚠ SDK 配線あれど smoke で発動せず | ✅ ネイティブ対応 |
| `GoogleSearch.TimeRangeFilter` | ❌ "not supported in Vertex AI" | ✅ |
| `GoogleSearch.ExcludeDomains` | ✅ | ❌ "not supported in Gemini API" |
| `GoogleSearch.BlockingConfidence` | ✅ | ❌ |
| `EnterpriseWebSearch` tool | ✅ | ❌ |
| `Retrieval.VertexAISearch` (data store) | ✅ | ❌ |
| ResponseJsonSchema / 構造化出力 | ✅ | ✅ |
| ThinkingConfig | ✅ | ✅ |
| Context Caching | ✅ | ✅ |
| **Auth** | ADC + IAM | API key |
| **2026-06-19 以降の追加要件** | 不要 | API key restriction 必須 |

→ **URL Context や TimeRangeFilter を使いたいなら Gemini API direct 一択**。ExcludeDomains が必要なら Vertex AI。両方は不可。

### SDK での切替

```go
cc := &genai.ClientConfig{}
if cfg.APIKey != "" {
    cc.Backend = genai.BackendGeminiAPI
    cc.APIKey  = cfg.APIKey  // env: GOOGLE_API_KEY / GEMINI_API_KEY も認識
} else {
    cc.Backend = genai.BackendVertexAI
    cc.Project = cfg.ProjectID
    cc.Location = cfg.Location  // ADC 自動取得
}
```

`GOOGLE_GENAI_USE_VERTEXAI=true` 環境変数でも Vertex 強制可。

---

## 2. モデル比較 (Gemini 3 family)

### 公式単価 (2026-05 時点、両 backend 同一)

| Model | Input $/1M tokens | Output $/1M tokens | Cached $/1M | 備考 |
|---|---|---|---|---|
| `gemini-3-flash-preview` | 0.50 | 3.00 | 0.05 | Preview、504 多発の不安定報告 |
| `gemini-3.1-flash-lite` | 0.25 | 1.50 | 0.025 | 最安。grounding 浅め |
| `gemini-3.5-flash` | 1.50 | 9.00 | 0.15 | 最高品質、verbose 傾向 |
| `gemini-3.1-pro-preview` | (Preview) | (Preview) | — | 最強推論 |

### Grounding 単価 (両 backend 同一)

- **Google Search grounding**: 5,000 requests/month 無料 → $14 per 1,000 queries
- **URL Context**: 取得 content は input tokens 課金、サーチャージなし

### ThinkingLevel オプション

SDK の `genai.ThinkingLevel`:

```go
ThinkingLevelUnspecified // ThinkingConfig を渡さない (model 既定)
ThinkingLevelMinimal     // "MINIMAL"
ThinkingLevelLow         // "LOW"
ThinkingLevelMedium      // "MEDIUM"
ThinkingLevelHigh        // "HIGH"
```

旧 `ThinkingBudget` (token 数指定) は Gemini 2.x まで。Gemini 3 系は `ThinkingLevel` のみ。

**実測の挙動 (3.1-lite で 54 cells)**:

| Level | Precision | Recall | Latency | Cost |
|---|---|---|---|---|
| medium | 0.492 | 0.271 | 11.8s | $0.080 (54 cells) |
| high | 0.482 | 0.212 | 16.7s | $0.140 (54 cells) |

→ **3.1-lite では `medium` が全指標で `high` を上回る**。他モデルでは未検証。

---

## 3. プロンプティング (Gemini 3 公式ベストプラクティス)

### Gemini 3 が嫌うもの (旧モデルで効いていた手法)

- **冗長な persona 設定** ("You are a world-class expert...")
- **長い禁則例リスト** (FORBIDDEN modifications を 5+ 並べる等)
- **キャップス連発** ("ABSOLUTE RULE", "NEVER", "FORBIDDEN")
- **複雑な CoT 誘導** ("Let's think step by step...")
- **XML と Markdown 混在**

→ Gemini 3 は over-analyze する。簡潔・直接的に。

### Gemini 3 が好むもの

- **簡潔な命令**: 1 文 1 意図
- **構造一貫**: XML タグ (`<task>`, `<rules>`, `<output>`) or Markdown のいずれか単一
- **重要命令を冒頭に**: model は先頭の文脈を重視
- **`Think very hard before answering`** のような単純な強化命令
- **Persistence Directive**: 「タスク完了まで止まるな」明示
- **Pre-Computation Reflection**: ツール呼び出し前に why/what/how を述べさせる

### Temperature

**デフォルト `1.0` 維持を強く推奨**。低温度で挙動が不安定になる報告あり。実測でも T=1.0 が T=0.2/0.5 を precision/recall 両方で上回った。

### 構造化出力

`responseJsonSchema` (JSON Schema 標準) が推奨。`responseSchema` (OpenAPI subset) は legacy。

JSON Schema 利用時の Tips:
- `additionalProperties: false` で extra field 抑止
- `required` で必須化
- `description` で各 field の規約を明示
- Empty string vs null の扱いを明示 (model は null を返しがち)

### XML タグ構造の例

```xml
<task>
  ...タスク定義...
</task>

<sources>
  ...どこから情報を引くか...
</sources>

<rules>
  1. ...
  2. ...
</rules>

<output>
  JSON only:
  { "events": [ ... ] }
</output>
```

---

## 4. Grounding ツール

### 4.1 Google Search grounding

#### 構造

```go
type GoogleSearch struct {
    SearchTypes        *SearchTypes        // SearchTypes 指定 (default = web)
    BlockingConfidence PhishBlockThreshold // ⚠ Vertex AI のみ
    ExcludeDomains     []string            // ⚠ Vertex AI のみ (max 2000 ドメイン)
    TimeRangeFilter    *Interval           // ⚠ Gemini API direct のみ
}
```

#### TimeRangeFilter (Gemini API direct 限定)

- **両端必須** (start のみ・end のみは不可)
- **granularity は秒以上** ("Granularity of nano is not supported" エラーが出る)
- Web ページの最終更新日が範囲内のものに限定

```go
now := time.Now().UTC().Truncate(time.Second)
&genai.Interval{
    StartTime: now.AddDate(0, -6, 0),
    EndTime:   now,
}
```

⚠ python-genai #1207 で「効果がない」報告あり。本番投入前に metadata で範囲外 URL が含まれていないか確認推奨。

#### ExcludeDomains (Vertex AI 限定)

```go
&genai.GoogleSearch{
    ExcludeDomains: []string{
        "facebook.com",
        "twitter.com",
        "x.com",
        "ja.wikipedia.org",
    },
}
```

→ stale 情報源や fan blog を grounding 候補から外せる。

#### **`IncludeDomains` は存在しない**

「公式サイトのみ」に限定する代替手段:
- prompt で `site:<official-domain>` 演算子を model に提案
- URLContext を中心に据えて Google Search を抑止
- 後処理で source_url のドメイン判定

### 4.2 URL Context (Gemini API direct 限定)

#### 動作原理

```
model が url_context を呼ぶ判断
    ↓
Step 1: Google internal index cache を確認
    ↓
  HIT → 即返却 (cost ↓ latency ↓)
  MISS → Step 2: live HTTP fetch
    ↓
取得 content が input tokens として model に渡る
```

#### 制約

| 項目 | 値 |
|---|---|
| Max URLs per request | **20** |
| Per-URL content cap | **34 MB** |
| 課金 | 取得 content は input tokens として model 単価で課金 |
| Surcharge | なし |
| 対応形式 | HTML, JSON, plain text, XML, CSV, RTF, PNG/JPEG/BMP/WebP, PDF |
| 非対応 | paywall, YouTube, Google Workspace, video/audio |

#### URLContext の発動条件

公式 doc: **"users provide URLs upfront in the prompt text"** — model は自律的に発動せず、**プロンプト中に URL を明記する**ことで発火する。

GoogleSearch と併用すれば「Search で URL 発見 → url_context で deep-read」のパターンも可能だが、これは **prompt で明示的に指示しないと model が呼ばない** (特に lite モデル)。

#### 効く prompt パターン (smoke で確認済み)

```
STEP 1 — MANDATORY: call url_context with the EXACT URL https://...
STEP 2 — MANDATORY: for each linked URL discovered above, call url_context AGAIN.
```

「MANDATORY」「EXACT URL」「STEP N」のような明示的・命令的語り口で発動率が上がる。

### 4.3 EnterpriseWebSearch (Vertex AI 限定)

Sec4 (Google enterprise security tier) コンプライアンス対応の Web 検索。standard `GoogleSearch` の上位互換的位置付け。利用には Vertex AI Enterprise 契約必要。

---

## 5. Generation config パラメータ

`GenerateContentConfig` の主要 field と推奨設定:

| Field | 推奨値 (concert search) | 備考 |
|---|---|---|
| `SystemInstruction` | 短く明示的に | concert 抽出方針を 3-5 行で |
| `Temperature` | `1.0` (default) | Gemini 3 では下げない |
| `MaxOutputTokens` | `16384` | tour 30 件分の JSON 想定 |
| `ResponseMIMEType` | `"application/json"` | 必須 (structured output 時) |
| `ResponseJsonSchema` | 厳密 schema | `additionalProperties: false` 推奨 |
| `ResponseSchema` | (使わない) | レガシー、JSON Schema 推奨 |
| `Tools` | `[google_search, url_context]` | Gemini API direct なら両方 |
| `ThinkingConfig` | `medium` (lite で実測 best) | 他モデルは要 N>=3 検証 |
| `ToolConfig.FunctionCallingConfig` | (built-in tool には適用不可) | function calling 用 |

### MaxOutputTokens の調整指針

- 1 event ≈ 200-300 output tokens
- ツアー 30 dates 想定 → 6,000-9,000 → 16,384 で余裕
- verbose 傾向のある 3.5-flash では truncation 注意。`FinishReason=MAX_TOKENS` 監視

### `ToolConfig` の制限

`FunctionCallingConfig.Mode = AUTO / ANY / NONE` で function calling を制御できるが、**built-in tool (GoogleSearch / URLContext) には適用されない**。built-in tool の発動回数を直接制御する API は現状無い。間接的な手段:

- Prompt で誘導 (例: "Issue at most 3 grounding queries")
- Context Caching で system_instruction を固定
- アプリ層で「取得済み公演」を prompt に注入 → 重複検索を抑止

---

## 6. Response metadata (チューニングに使える観測データ)

`response.Candidates[0]` から取得可能:

### 6.1 UsageMetadata

```go
type GenerateContentResponseUsageMetadata struct {
    PromptTokenCount        int32 // 入力トークン (system + user)
    CandidatesTokenCount    int32 // 出力トークン
    ThoughtsTokenCount      int32 // thinking 中の internal reasoning tokens (output 単価)
    TotalTokenCount         int32 // 合計
    ToolUsePromptTokenCount int32 // url_context 等の tool 呼び出しで input 化された tokens
    CachedContentTokenCount int32 // context cache hit 分
}
```

**重要**: `ToolUsePromptTokenCount` を **コスト計算に input として加算する必要あり**。url_context で 34MB を fetch すると 10,000+ tokens 追加されることもある。

### 6.2 GroundingMetadata

```go
type GroundingMetadata struct {
    WebSearchQueries     []string             // model が発行した検索クエリ (debug 用)
    GroundingChunks      []*GroundingChunk    // 引用源 (uri, title, domain)
    GroundingSupports    []*GroundingSupport  // 出力 text segment → chunk 対応
    SearchEntryPoint     *SearchEntryPoint    // Search Suggestions の HTML/CSS
    RetrievalMetadata    *RetrievalMetadata   // googleSearchDynamicRetrievalScore (旧 1.5 用)
    RetrievalQueries     []string             // Vertex AI Search retrieval 用
    GoogleMapsWidgetContextToken string       // Maps grounding 用
}

type GroundingChunk struct {
    Web              *GroundingChunkWeb              // uri, title, domain
    Maps             *GroundingChunkMaps             // 場所情報
    RetrievedContext *GroundingChunkRetrievedContext // Vertex AI Search 由来
    Image            *GroundingChunkImage            // 画像引用
}

type GroundingSupport struct {
    GroundingChunkIndices []int32   // どの chunk に紐付くか
    ConfidenceScores      []float32 // 0-1 confidence
    Segment               *Segment  // 出力テキストのどの部分か (StartIndex/EndIndex)
    RenderedParts         []int32   // (rendered_parts への index、用途不明)
}
```

### 6.3 URLContextMetadata

```go
type URLContextMetadata struct {
    URLMetadata []*URLMetadata
}

type URLMetadata struct {
    RetrievedURL       string             // 実際に fetch した URL
    URLRetrievalStatus URLRetrievalStatus // SUCCESS / ERROR / PAYWALL / UNSAFE
}
```

→ **url_context が実際に fetch した URL の完全リスト**。citation / 検証に最適。

### 6.4 既知の metadata 欠落問題

- **`GroundingChunks` が空 (`len == 0`) で返ることがある**
  - GoogleSearch grounding を使っているのに `webSearchQueries` だけ populate、`groundingChunks` が空
  - Gemini API direct でも Vertex AI でも発生
  - 関連 Issue: [googleapis/python-genai#1322](https://github.com/googleapis/python-genai/issues/1322)、[Google AI Forum feature request](https://discuss.ai.google.dev/t/feature-request-provide-actual-source-urls-in-grounding-metadata/107352)
  - 回避策: 引用元 URL は **url_context の `URLContextMetadata`** から取得する

- **`GroundingChunks.Web.URI` が `vertexaisearch.cloud.google.com/grounding-api-redirect/...` redirect URL**
  - 実際の source URL ではなく Google の redirect URL が返る
  - HTTP HEAD で resolve するロジックが別途必要

---

## 7. Go SDK 現状

### `google.golang.org/genai`

- **2026-05 時点最新**: `v1.57.0`
- 旧 `cloud.google.com/go/vertexai` および `github.com/google/generative-ai-go` は **deprecated**
- 新 SDK は Vertex AI と Gemini API direct を統一 backend で扱う

### Gemini 3 関連の主要型 (`google.golang.org/genai@v1.57.0`)

| 型 | 用途 | Backend 制約 |
|---|---|---|
| `Tool.GoogleSearch` | Web 検索 grounding | 両 backend |
| `Tool.URLContext` | URL fetch tool | Gemini API direct 推奨 |
| `Tool.GoogleSearchRetrieval` | 旧 Gemini 1.5 用 (DynamicRetrievalConfig 付き) | レガシー、3.x で非推奨 |
| `Tool.Retrieval.VertexAISearch` | Vertex AI Search data store | Vertex AI 限定 |
| `Tool.EnterpriseWebSearch` | 企業向け Web 検索 | Vertex AI 限定 |
| `Tool.FunctionDeclarations` | カスタム function calling | 両 backend |
| `Tool.CodeExecution` | コード実行 | Vertex AI |
| `ThinkingConfig.ThinkingLevel` | Gemini 3 thinking | 両 backend |
| `GenerateContentConfig.ResponseJsonSchema` | JSON Schema 構造化出力 | 両 backend |
| `Interval.StartTime/EndTime` (TimeRangeFilter) | Web 検索の時間範囲 | Gemini API direct 限定 |

### Client 初期化

```go
import "google.golang.org/genai"

// Gemini API direct
cli, _ := genai.NewClient(ctx, &genai.ClientConfig{
    Backend: genai.BackendGeminiAPI,
    APIKey:  os.Getenv("GEMINI_API_KEY"),
})

// Vertex AI
cli, _ := genai.NewClient(ctx, &genai.ClientConfig{
    Backend:  genai.BackendVertexAI,
    Project:  os.Getenv("GCP_PROJECT_ID"),
    Location: os.Getenv("GCP_LOCATION"),
})
// ↑ ADC を自動使用。明示認証が必要なら cc.UseDefaultCredentials() 呼び出し
```

---

## 8. 運用上の制約

### 8.1 API key restrictions (2026-06-19 deadline)

Gemini API direct を使う場合、**2026-06-19 以降は unrestricted key が即失効**。

必須対応:
- **Option A (最小限)**: AI Studio の "Restrict to Gemini API" ボタンで API restriction 設定
- **Option B (推奨)**: Google Cloud Console で IP allow-list (Application restriction) 設定
- **Option C**: 両方併用 (Best Practice)

K8s 環境では:
- GKE NAT IP を固定化 → IP allow-list に登録
- API key は Secret Manager 経由で Workload Identity 連携
- Rotation: 90 日推奨

### 8.2 Rate limits

| Backend | Free tier | Paid tier |
|---|---|---|
| Gemini API direct | 1,500 RPD / 15 RPM (Flash) | 引き上げ可、quota 申請 |
| Vertex AI | $300 free credits × 90 日 | 引き上げ可、enterprise quota |

### 8.3 Data residency

- Vertex AI: `Location` で region 指定可 (`global`, `asia-northeast2` 等)
- Gemini API direct: data location 制御不可。VPC Service Controls 非対応

### 8.4 GroundingChunks の API 安定性

`GroundingChunks` の populate は **公式に保証されていない**。空が返るパターン:
- Search が結果ヒットしたが confidence 不足
- Model が grounding を判断したが内部で破棄
- API 側の既知バグ

→ source URL 取得は `URLContextMetadata` 経由が確実。

---

## 9. 既知の落とし穴・既知問題

### 9.1 TimeRangeFilter

- ❌ nanos granularity 受付不可 → `time.Now().Truncate(time.Second)` 必須
- ⚠ python-genai #1207: 「効かない」報告 — start/end が無視される現象
- ✅ Go SDK では SDK-level エラー検出はある (granularity チェック等)

### 9.2 URL Context

- ❌ **自律発動しない** — prompt 中に URL を明記しないと呼ばれない (公式仕様)
- ⚠ Lite モデルは `STEP 1 — MANDATORY` のような命令的語り口でないと無視しがち
- ⚠ Step 2 (再帰 fetch) は明示しても発火率低い (model 依存)
- ⚠ Vertex AI 経由では `URLContextMetadata` が空 (実質非対応)

### 9.3 Tool 制御

- ❌ `ToolConfig.FunctionCallingConfig.Mode = ANY` は **built-in tool には適用されない**
- ⚠ `dynamic_retrieval_config` (Gemini 1.5 era) は Gemini 3 では非対応
- → built-in tool の発動回数は prompt で間接誘導するしかない

### 9.4 構造化出力

- ⚠ `start_time` / `open_time` の **timezone 欠落**: schema description で明示しても省略される (例: `"2026-06-06T17:00:00"`)
- ⚠ `null` vs `""` の混同: 「empty string で返せ」と prompt で何度も繰り返す必要あり
- ⚠ `venue` field に **prefecture を併記**してくる (例: `"Zepp Osaka Bayside, 大阪府"`) → rule で禁止例示必要

### 9.5 ツアー名アグリゲーション

- ⚠ 個別公演を「UVERworld THE LIVE 2026」のような **集約名で返す傾向** (全モデル共通)
- 回避: prompt で `verbatim` と明示 + 禁止例示 (`three FC live shows merged into "UVERworld THE LIVE 2026" — forbidden`)

### 9.6 訓練データの古い情報

- ⚠ 3.5-flash で **古い venue 情報を返す** (例: ROCK IN JAPAN 2023 当時の蘇我スポーツ公園 → 2024 から海浜公園)
- 回避: TimeRangeFilter で stale ページ排除 + prompt で「search result を model 内部知識より優先せよ」

### 9.7 grounding queries 過多

- ⚠ 3.5-flash は per call で 17 queries 平均 → grounding overage コスト爆発
- 回避策:
  - Prompt 圧縮 (Gemini 3 は short prompt を好む)
  - 取得済み公演を prompt に「除外リスト」として渡す
  - ThinkingLevel を low に下げる
  - dynamic_retrieval は使えない (Gemini 3 非対応)

### 9.8 redirect URL 問題

- `GroundingChunks[].Web.URI` が `vertexaisearch.cloud.google.com/grounding-api-redirect/...` 形式
- 実 URL を取得するには HTTP HEAD/GET で `Location` を resolve する追加実装が必要

---

## 10. References

### Google 公式ドキュメント

- [Gemini API libraries (Go SDK)](https://ai.google.dev/gemini-api/docs/libraries)
- [Gemini Developer API vs Enterprise Agent Platform](https://ai.google.dev/gemini-api/docs/migrate-to-cloud)
- [Prompt design strategies](https://ai.google.dev/gemini-api/docs/prompting-strategies)
- [Grounding with Google Search](https://ai.google.dev/gemini-api/docs/google-search)
- [URL Context tool](https://ai.google.dev/gemini-api/docs/url-context)
- [Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Gemini Enterprise Agent Platform pricing](https://cloud.google.com/gemini-enterprise-agent-platform/generative-ai/pricing)
- [Function calling (tool_config / function_calling_config)](https://ai.google.dev/gemini-api/docs/function-calling)
- [API key restrictions](https://ai.google.dev/gemini-api/docs/api-key)
- [GroundingMetadata reference](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1beta1/GroundingMetadata)
- [Get a Google Cloud API key (for Gemini)](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/start/api-keys)

### Google 公式ブログ

- [Gemini 3 for developers: New reasoning, agentic capabilities](https://blog.google/innovation-and-ai/technology/developers-tools/gemini-3-developers/)
- [Gemini API tooling updates (context circulation, tool combos, Maps grounding for Gemini 3)](https://blog.google/innovation-and-ai/technology/developers-tools/gemini-api-tooling-updates/)
- [Gemini API and AI Studio now offer Grounding with Google Search](https://developers.googleblog.com/en/gemini-api-and-ai-studio-now-offer-grounding-with-google-search/)
- [Mastering Controlled Generation with Gemini (schema adherence)](https://developers.googleblog.com/en/mastering-controlled-generation-with-gemini-15-schema-adherence/)
- [Gemini API JSON Schema support](https://blog.google/innovation-and-ai/technology/developers-tools/gemini-api-structured-outputs/)
- [Gemini 3 Flash announcement](https://blog.google/products-and-platforms/products/gemini/gemini-3-flash/)
- [Gemini 3.1 Flash Lite announcement](https://blog.google/innovation-and-ai/models-and-research/gemini-models/gemini-3-1-flash-lite/)

### コミュニティ・記事

- [Gemini 3 Prompting Best Practices (Philipp Schmid)](https://www.philschmid.de/gemini-3-prompt-practices)
- [Artificial Analysis - Gemini 3.5 Flash](https://artificialanalysis.ai/models/gemini-3-5-flash)
- [Artificial Analysis - Gemini 3 Flash](https://artificialanalysis.ai/models/gemini-3-flash)

### GitHub Issues (注意すべき不具合報告)

- [python-genai#1207 — time_range_filter has no effect](https://github.com/googleapis/python-genai/issues/1207)
- [python-genai#1322 — URL context combined with grounding fails to load](https://github.com/googleapis/python-genai/issues/1322)
- [AI Forum — Feature request: actual source URLs in grounding metadata](https://discuss.ai.google.dev/t/feature-request-provide-actual-source-urls-in-grounding-metadata/107352)

### SDK ソース (本リポジトリで参照中)

`google.golang.org/genai@v1.57.0`:

- `types.go` — `GoogleSearch`, `URLContext`, `Tool`, `ThinkingConfig`, `Interval` 等の型定義
- `client.go` — `Backend` enum, `ClientConfig`
- `models.go` — `GenerateContent` の API surface

### このリポジトリの A/B 評価ログ

実測データの保管場所:

- `internal/infrastructure/gcp/gemini/testdata/ab_results/` — A/B 検証結果 (JSON / CSV)
- `internal/infrastructure/gcp/gemini/testdata/ab_results/<ts>_raw/` — per-cell の生レスポンス (URLContextMetadata, GroundingMetadata 含む)

ツール:
- `cmd/replay-ab-log/` — 過去ログを fixture と再スコアリング
- `cmd/analyze-ab-errors/` — false positive / false negative の分類
- `cmd/analyze-missed-events/` — 欠落イベントのカテゴリ別集計
- `cmd/analyze-grounding/` — grounding 活動量の per-cell 解析
- `cmd/annotated-fixture/` — fixture を per-event-status で出力
