## Context

現在の `ConcertService` は、本来分離されるべき「アーティスト情報管理」と「コンサート情報管理」の両方を担っている。また、ユーザーがアーティストをフォロー（お気に入り登録）する機能が存在せず、パーソナライズされた体験の起点がない。
Last.fm API を活用した連鎖的な発見体験を提供するために、サービス分割と新規UXの実装を行う。

## Goals / Non-Goals

**Goals:**
- `ArtistService` の新設と `ConcertService` からの責務移譲。
- Last.fm API を利用した「人気アーティスト」および「関連アーティスト」の取得。
- ユーザーによるアーティストフォロー機能の実装。
- 検索・フォロー・連鎖的発見（Drill-down）を統合したオンボーディングUIの実装。

**Non-Goals:**
- フォローしたアーティストのライブ情報の自動検索（これは既存の `SearchNewConcerts` を非同期で回す別タスクとする）。
- YouTube 履歴解析ロジックの実装（本チェンジのスコープ外）。

## Decisions

### 1. サービス分割 (Refactoring)
**決定**: アーティストの CRUD およびメタデータ、フォローステータスに関わる全ての RPC を `ArtistService` へ移動・集約する。
**理由**: 責務の明確化。将来的に「アーティスト詳細ページ」や「おすすめアーティスト」などの機能拡張を容易にするため。

### 2. Follow（Watch）のデータモデル
**決定**: `followed_artists` テーブルを新設し、`user_id` と `artist_id` のペアで管理。
**理由**: 将来的にフェスや会場などのフォローも想定されるが、まずはシンプルにアーティスト単体に特化。

### 3. Last.fm API のプロキシ
**決定**: フロントエンドから直接 Last.fm を叩かず、Backend の `ArtistService` (ListSimilar, ListTop) を介してアクセスする。
**理由**:
- APIキーの秘匿。
- 結果に含まれるアーティストを DB と照合し、必要に応じて ID を付与するため。
- レートリミット等の制御。

**使用するエンドポイント**:
- **Search (インクリメンタルサーチ)**:
  - メソッド: `artist.search`
  - URL: `http://ws.audioscrobbler.com/2.0/?method=artist.search&artist={artist_name}&api_key={api_key}&format=json`
- **Similar Artists (連鎖フォロー機能)**:
  - メソッド: `artist.getSimilar`
  - URL: `http://ws.audioscrobbler.com/2.0/?method=artist.getsimilar&artist={artist_name}&api_key={api_key}&format=json`
- **Top Artists (初期表示 - 国内人気アーティスト)**:
  - メソッド: `geo.getTopArtists`
  - URL: `http://ws.audioscrobbler.com/2.0/?method=geo.gettopartists&country=japan&api_key={api_key}&format=json`

### 4. UI 遷移の管理
**決定**:
- `ListTop`: ユーザーが最初に目にする「国内人気アーティスト」。
- `Search`: インクリメンタルサーチ。
- `Follow`: 実行後にそのアーティストをシードとして `ListSimilar` を呼び出し、「連鎖提案モード」へ移行。
- `Reset`: 全てのフィルタを解除し `ListTop` の結果に戻す。

## Risks / Trade-offs

- **[Risk] Last.fm の不正確なデータ** → アーティスト名のみで名寄せを行うため、DB上の既存データと重複する可能性がある。
  - **Mitigation**: 取得時にアーティスト名による簡単な正規化を行い、DBにあればそのIDを、なければ新規作成候補として扱う。
- **[Trade-off] インクリメンタルサーチのパフォーマンス** → 外部APIを毎回叩くと遅延が発生する。
  - **Mitigation**: Backend でキャッシュを行う、または最初は DB 検索のみにする。
