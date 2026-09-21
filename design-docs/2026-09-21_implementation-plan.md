# Yomiagen 実装プラン

最終更新日時: 2026-09-21

読者: AI実装者を主、方針をレビューする人間を従とする。
前提文書: `2026-09-21_initial.md`（コンセプト）、`2026-09-21_tech-research.md`（技術調査・実測結果）
方針: `AGENTS.md` に従い、**現実的な時間内に動くものを出す**ことを最優先する。

---

## 1. 設計の中心アイデア

### 1.1 ステージ + ジョブディレクトリ

パイプラインを6ステージに分け、**各ステージの出力をジョブディレクトリ内のファイルとして永続化する**。

```
ingest → script → pronounce → query → synthesize → assemble
```

これ1つで initial.md の3要求が同時に満たされる:

- **一気通貫** → 全ステージを順に回すだけ
- **中間生成物をレビューしながら** → ステージ出力は**ただのファイル**。`cat` で見れるし、`$EDITOR` で書き換えて次のステージを走らせればよい。専用UIは不要
- **コストのトラッキング** → 各ステージがコストレコードを追記する

**中間生成物を「人が手で編集できるファイル」にすることが、レビュー機能の実装コストをほぼゼロにする。** これが設計の肝。

### 1.2 エンジン固有の最適化を一級市民にする

ユーザー方針:

> 最大公約数を取るより、特定エンジンにロックインされてでも必要な最適化を行える方が価値がある。

実測（調査文書 §2）でこれを支える事実が見つかった:

- VOICEVOX は **`GET /engine_manifest` の `supported_features` で自分の機能を機械可読に申告する**
- **アクセント型を変えて `/mora_data` に投げると、ピッチと音長がエンジン側で正しく引き直される**

したがって設計は「共通インターフェースに合わせて機能を削る」のではなく、

**「エンジンネイティブの表現をそのまま中間生成物として持ち、そこに対するエンジン固有の操作を、データとして記述・蓄積・レビューできるようにする」**

とする。ポータビリティは capability negotiation（エンジンへの問い合わせ）で**実行時に**解決し、設計時に最大公約数を取ることはしない。

これを3層に分ける:

| 層 | 役割 | ポータビリティ |
|---|---|---|
| **L1 Backend** | テキスト → 音声。全バックエンドが実装 | 完全 |
| **L2 Query** | エンジンネイティブの中間表現を露出（VOICEVOX: AudioQuery、GCP: SSML） | エンジンごとに別物でよい |
| **L3 Ops** | ネイティブ表現に対する名前付き操作（`accent`, `emphasize`, `devoice`, …） | エンジン固有。capability で可否が決まる |

**L3 が「エンジンにロックインされてでも必要な最適化」の置き場所。** L1 は非対応エンジンへのフォールバック経路として残るだけで、L1 に合わせて L3 を削ることはしない。

### 1.3 再実行とキャッシュ

各ステージは「入力ファイルの内容ハッシュ + ステージ設定のハッシュ」をキーに持つ。

- キーが前回と同じで出力が存在する → **スキップ**
- 中間ファイルを手編集した → 入力ハッシュが変わり、**下流だけが再実行される**
- `--force` で強制再実行

コストのかかる LLM 呼び出しと TTS 合成の重複実行がこれで自然に防げる。

### 1.4 ジョブディレクトリ

```
$YOMIAGEN_HOME/jobs/<job-id>/
  job.json                 # メタデータ、各ステージの状態とハッシュ、登録した辞書のUUID
  cost.jsonl               # コストレコード（追記のみ）
  00_source.html           # 取り込んだ生データ
  10_document.json         # ingest:     構造化された論理文書
  20_script.json           # script:     読み上げ台本 + 用語辞書
  30_speech.json           # pronounce:  チャンク分割済み発話 + 適用する ops
  35_query/c0001.json      # query:      ★エンジンネイティブ表現（AudioQuery / SSML）
  40_chunks/c0001.wav      # synthesize: チャンク音声
  50_output.wav            # assemble:   結合音声
  50_output.m4a            #             ffmpeg によるエンコード + チャプター
  50_chapters.txt
```

`$YOMIAGEN_HOME` は既定で `~/.local/share/yomiagen`（`XDG_DATA_HOME` を尊重）。
`<job-id>` は `20260921-143052-<入力由来のスラグ>`。時系列に並ぶので `ls` が使える。

**`35_query/` が抑揚チューニングのレビュー面。** VOICEVOX なら、ここに VOICEVOX 自身の AudioQuery がそのまま入っている。モーラのピッチを手で直したければ直接書き換えればよく、`synthesize` はそれを読んで合成する。**エンジンの表現力に、こちらの抽象が上限をかけない。**

---

## 2. TTS 層の設計（本プランの中核）

### 2.1 インターフェース

```go
package tts

// --- L1: 全バックエンド共通の最小契約 ---

type Backend interface {
    Name() string
    Capabilities(ctx context.Context) (CapSet, error)
    Voices(ctx context.Context) ([]Voice, error)
    Synthesize(ctx context.Context, req Request) (*Result, error)
}

type Request struct {
    Text    string
    VoiceID string
    Speed   float64
    Volume  float64
}

type Result struct {
    WAV          []byte
    Characters   int
    AudioSeconds float64
    CostUSD      float64
}

type Voice struct {
    ID       string   // VOICEVOX: style_id の文字列 ("3")
    Name     string   // "ずんだもん / ノーマル"
    Language string
}
```

```go
// --- L2: ネイティブ中間表現を露出するバックエンド（任意実装） ---

type QueryBackend interface {
    Backend

    // テキストからネイティブクエリを作る（VOICEVOX: POST /audio_query）
    BuildQuery(ctx context.Context, text string, voice string, opt QueryOptions) (*Query, error)

    // 編集済みクエリから合成する（VOICEVOX: POST /synthesis）
    SynthesizeQuery(ctx context.Context, q *Query, voice string) (*Result, error)
}

// Query はエンジンネイティブの JSON をそのまま保持する。
// Go の struct にマッピングしない ― エンジンのバージョン差分・独自拡張を
// こちらの型定義で削ってしまわないため。
type Query struct {
    Engine string         `json:"engine"`  // "voicevox" | "gcp"
    Voice  string         `json:"voice"`
    Doc    jsontext.Value `json:"doc"`     // AudioQuery そのもの / SSML 文字列
}

type QueryOptions struct {
    KatakanaEnglish bool
    Kana            string // AquesTalk風記法。空でなければ読みを上書き
}
```

**`jsontext.Value` で生 JSON を保持するのが設計上の要。** `AudioQuery` を Go の struct に写すと、エンジンが新しいフィールド（`tempoDynamicsScale` のような）を足したときに落ちる。生で持てば、知らないフィールドはそのまま素通しできる。

```go
// --- L3: ネイティブ表現に対するエンジン固有の操作 ---

type Refiner interface {
    Engine() string
    Apply(ctx context.Context, q *Query, op Op) error
}

type Op struct {
    Op     string         `json:"op"`              // "accent" | "emphasize" | "devoice" | ...
    Engine string         `json:"engine,omitzero"` // 空なら engine を問わない
    Needs  []Capability   `json:"needs,omitzero"`  // 必要な capability
    Target Target         `json:"target,omitzero"`
    Args   jsontext.Value `json:"args,omitzero"`
}

type Target struct {
    Phrase int    `json:"phrase,omitzero"` // アクセント句インデックス。-1 で全体
    Mora   int    `json:"mora,omitzero"`   // モーラインデックス。-1 で句内全体
    Match  string `json:"match,omitzero"`  // モーラ列にマッチするカタカナ（インデックスより壊れにくい）
}

type Capability string

const (
    CapMoraPitch       Capability = "adjust_mora_pitch"
    CapPhonemeLength   Capability = "adjust_phoneme_length"
    CapPauseLength     Capability = "adjust_pause_length"
    CapIntonationScale Capability = "adjust_intonation_scale"
    CapPitchScale      Capability = "adjust_pitch_scale"
    CapInterrogative   Capability = "interrogative_upspeak"
    CapKatakanaEnglish Capability = "apply_katakana_english"
    CapMorphing        Capability = "synthesis_morphing"
)
```

`CapSet` は **`GET /engine_manifest` の `supported_features` をそのまま読んだもの**（調査文書 §2.2）。キー名を VOICEVOX の申告名に合わせてあるのは意図的で、変換テーブルを持たなくて済む。

### 2.2 非対応 op の扱い

```go
type DegradePolicy string
const (
    DegradeSkip  DegradePolicy = "skip"  // 既定: 警告を出して飛ばす
    DegradeError DegradePolicy = "error" // 品質を厳密に再現したいとき
)
```

`op.Needs` が `Capabilities()` に含まれなければ、既定ではログに残して**飛ばす**。止めない。

**これにより「VOICEVOX 向けに書き込んだ抑揚チューニングを AivisSpeech でそのまま流すと、効くものだけが効く」という降格が自動で起きる。** エンジン差分のテーブルをこちらで持つ必要がない。

### 2.3 VOICEVOX の op 実装（実測に基づく）

`internal/tts/voicevox/refine.go`。すべて調査文書 §2 で実測した挙動に基づく。

| op | 実装 | 必要な capability |
|---|---|---|
| `accent` | `AccentPhrase.accent` を書き換え、**`POST /mora_data` でピッチと音長を再計算**（実測で正しく引き直されることを確認済み） | `adjust_mora_pitch` |
| `emphasize` | 対象モーラの `pitch` に加算（**pitch は対数スケールなので乗算ではなく加算**）。既定 `+0.15` | `adjust_mora_pitch` |
| `lengthen` | `vowel_length` / `consonant_length` に倍率。強調・タメに使う | `adjust_phoneme_length` |
| `devoice` | モーラの `vowel` を大文字化（`u` → `U`）。AquesTalk記法の `_` 相当 | `adjust_mora_pitch` |
| `pause` | `AccentPhrase.pause_mora` の `vowel_length`（秒）を設定、または `pause_mora` を新規挿入 | `adjust_pause_length` |
| `interrogative` | `AccentPhrase.is_interrogative` を立てる。`/synthesis?enable_interrogative_upspeak=true` と併用 | `interrogative_upspeak` |
| `kana` | `POST /accent_phrases?is_kana=true` で AquesTalk風記法をパースし、対象句を差し替える。事前に `POST /validate_kana` で検証 | — |
| `global` | `speedScale` / `pitchScale` / `intonationScale` / `volumeScale` / `prePhonemeLength` / `postPhonemeLength` / `pauseLength` / `pauseLengthScale` を設定 | 各 `adjust_*` |
| `patch` | ネイティブ JSON への直接マージパッチ。**脱出ハッチ**。op の語彙に無い調整を、コードを足さずに試せる | — |

`patch` を最初から用意しておくのが重要。**新しい調整を思いついたときに、まず `patch` で試し、定着したら名前付き op に昇格させる**という運用ができる。実装を増やさずに表現力を保てる。

### 2.4 op はどこから来るか

3つの経路があり、この順に重ねる:

1. **プロソディプロファイル**（設定ファイル）— 文書全体に効く既定値。話速、間、`intonationScale` など
2. **pronounce ステージのルール** — 見出しは少しゆっくり、箇条書きの先頭は少し強調、といった機械的なもの
3. **手編集** — `30_speech.json` の `ops` 配列に直接書く、または `35_query/*.json` のネイティブ表現を直接いじる

**LLM に op を生成させるのは当面やらない。** ルールと手編集で十分な範囲がどこまでかを見てから判断する。`Op` がデータ構造として定義されているので、後から LLM 出力を流し込むのは容易。

### 2.5 GCP バックエンドをこの枠に収める

GCP のネイティブ表現は AudioQuery ではなく **SSML 文字列**。それでも同じ枠に収まる:

- `Query.Engine = "gcp"`、`Query.Doc` に `{"ssml": "<speak>…</speak>", "audioConfig": {…}}`
- `Capabilities()` は静的に返す（`adjust_speed_scale`, `adjust_pitch_scale` は真、`adjust_mora_pitch` は偽）
- `gcp` の `Refiner` は `emphasize` を `<emphasis>`、`pause` を `<break time="…">` にマップする
- `accent` / `devoice` は capability 不足で自動的にスキップされる

**「同じ op 名が、エンジンごとに違う機構で実現される、あるいは静かに諦められる」**のが正しい振る舞い。GCP を実装しなくても、この構造は Phase 1 から成立している。

---

## 3. データモデル

すべて `encoding/json/v2` で `jsontext.WithIndent("  ")` 整形して書く（人が編集する前提）。

### 3.1 Document（`10_document.json`）

```go
type Document struct {
    Title    string  `json:"title"`
    Source   string  `json:"source"`
    Language string  `json:"language"`   // "ja" | "en" | "mixed"
    Blocks   []Block `json:"blocks"`
}

type Block struct {
    ID    string `json:"id"`              // "b0001"
    Kind  string `json:"kind"`            // heading|paragraph|list_item|quote|code|table|caption
    Level int    `json:"level,omitzero"`  // heading のみ 1..6
    Text  string `json:"text"`
    Lang  string `json:"lang,omitzero"`   // code block の言語
}
```

表は行をタブ区切りにした1テキストとして持つ。後段で散文化するだけなので構造を保つ価値が無い。

### 3.2 Script（`20_script.json`）

```go
type Script struct {
    Title      string      `json:"title"`
    Language   string      `json:"language"`
    Utterances []Utterance `json:"utterances"`
    Lexicon    []LexEntry  `json:"lexicon"`
}

type Utterance struct {
    ID          string `json:"id"`            // "u0001"
    SourceBlock string `json:"source_block"`  // 由来 Block ID（トレーサビリティ）
    Role        string `json:"role"`          // heading|body|note
    Level       int    `json:"level,omitzero"`
    Text        string `json:"text"`
}

type LexEntry struct {
    Surface       string `json:"surface"`        // "k8s"
    Pronunciation string `json:"pronunciation"`  // "クーベルネティス"（全角カタカナ）
    AccentType    int    `json:"accent_type"`
    WordType      string `json:"word_type"`      // PROPER_NOUN|COMMON_NOUN|VERB|ADJECTIVE|SUFFIX
    Note          string `json:"note,omitzero"`  // なぜこの読みか（レビュー用）
}
```

### 3.3 Speech（`30_speech.json`）

```go
type Speech struct {
    Backend string  `json:"backend"`
    Voice   string  `json:"voice"`
    Global  []tts.Op `json:"global"`   // 全チャンクに適用する op
    Chunks  []Chunk `json:"chunks"`
}

type Chunk struct {
    ID          string   `json:"id"`              // "c0001"
    UtteranceID string   `json:"utterance_id"`
    Text        string   `json:"text"`            // 合成に投げる実テキスト
    Kana        string   `json:"kana,omitzero"`   // AquesTalk風記法。空なら Text を使う
    Ops         []tts.Op `json:"ops,omitzero"`    // ★このチャンク固有の抑揚指示
}
```

Utterance → Chunk は 1:N。文境界で分割する。

### 3.4 Cost（`cost.jsonl`）

```go
type Record struct {
    Time         time.Time `json:"time"`
    JobID        string    `json:"job_id"`
    Stage        string    `json:"stage"`
    Provider     string    `json:"provider"`   // openai|voicevox|aivisspeech|gcp
    Model        string    `json:"model"`      // モデル名 or ボイス名
    InputTokens  int       `json:"input_tokens,omitzero"`
    CachedTokens int       `json:"cached_tokens,omitzero"`
    OutputTokens int       `json:"output_tokens,omitzero"`
    Characters   int       `json:"characters,omitzero"`
    AudioSeconds float64   `json:"audio_seconds,omitzero"`
    WallMS       int64     `json:"wall_ms"`
    CostUSD      float64   `json:"cost_usd"`
}
```

1行1レコードの JSONL。追記のみ。ローカルエンジンは `cost_usd: 0` だが**所要時間と文字数は記録する**（どこに時間がかかっているかが分かる）。

---

## 4. パッケージ構成

```
cmd/yomiagen/main.go

internal/
  cli/                      # サブコマンド（flag.NewFlagSet）
  config/                   # 設定、プリセット、プロソディプロファイル、料金表
  job/                      # ジョブディレクトリ、ステージ状態、ハッシュ、キャッシュ判定
  doc/                      # Document / Script / Speech の型
  ingest/
    ingest.go
    htmlx/                  # x/net/html ベースの本文抽出
    markdown/
  script/
    script.go
    schema.json             # Structured Outputs 用スキーマ（go:embed）
    prompt.md               # システムプロンプト（go:embed）
  pronounce/                # 辞書登録、チャンク分割、ルールベース op 付与
  tts/
    tts.go                  # Backend / QueryBackend / Refiner / Op / Capability
    voicevox/
      client.go             # HTTP クライアント
      backend.go            # Backend / QueryBackend 実装
      refine.go             # ★ VOICEVOX 固有の op 実装
      dict.go               # ユーザー辞書
    gcp/                    # Phase 4
  audio/                    # WAV パース・結合、ffmpeg ラッパ、チャプター
  llm/openai/               # Responses API クライアント
  cost/
```

**パッケージを細かく割りすぎない。** `internal/doc` に3モデルをまとめるのは意図的で、相互参照するものを分けると無意味な型変換が発生する。

---

## 5. 設定ファイル

`$YOMIAGEN_HOME/config.json`（初回起動時に既定値で生成）。

```json
{
  "default_preset": "voicevox-zundamon",
  "openai": {
    "model": "gpt-5.6-luna",
    "base_url": "https://api.openai.com/v1",
    "chunk_chars": 4000
  },
  "presets": [
    {
      "name": "voicevox-zundamon",
      "backend": "voicevox",
      "endpoint": "http://127.0.0.1:50021",
      "voice_id": "3",
      "voice_name": "ずんだもん / ノーマル",
      "prosody": "narration-ja"
    }
  ],
  "prosody_profiles": {
    "narration-ja": {
      "global": [
        { "op": "global", "args": {
            "speedScale": 1.15, "intonationScale": 1.1,
            "volumeScale": 1.0, "pauseLengthScale": 1.3 } }
      ],
      "pause_ms": {
        "sentence": 300, "paragraph": 600,
        "before_heading": 1000, "after_heading": 500, "before_section": 1500
      },
      "heading": [
        { "op": "global", "args": { "speedScale": 1.0 } }
      ]
    }
  },
  "chunk": { "max_chars": 200 },
  "concurrency": { "tts": 1 },
  "degrade_policy": "skip",
  "output": { "format": "m4a", "bitrate": "64k", "loudnorm": true },
  "pricing": {
    "openai": {
      "gpt-5.6-luna":  { "input": 0.20, "cached": 0.02, "output": 1.20 },
      "gpt-5.6-terra": { "input": 2.00, "cached": 0.20, "output": 12.00 }
    },
    "gcp_tts_per_million_chars": { "Chirp3-HD": 30.0, "Neural2": 16.0, "Standard": 4.0 }
  }
}
```

- **`concurrency.tts` の既定は 1**。実測で並列化に効果が無いことを確認済み（調査文書 §2.7）。設定項目としては残す（GPU版・GCP では意味が出る）。
- 料金表を外出しするのは、価格改定でコードを触らずに済ませるため。
- API キーは設定に入れず `OPENAI_API_KEY` から読む。

---

## 6. CLI

```
yomiagen run <input|job-id> [--preset NAME] [--from STAGE] [--to STAGE] [--force] [-o FILE]

yomiagen ingest     <input> [--preset NAME]
yomiagen script     <job-id> [--force]
yomiagen pronounce  <job-id> [--force]
yomiagen query      <job-id> [--force]          # ネイティブクエリ生成 + op 適用
yomiagen synthesize <job-id> [--force] [--only c0003,c0007]
yomiagen assemble   <job-id> [-o FILE]

yomiagen jobs                                    # 一覧とステージ状態
yomiagen show  <job-id> [stage]
yomiagen path  <job-id> [stage]                  # $EDITOR $(yomiagen path …) 用
yomiagen cost  [job-id] [--since DATE]
yomiagen voices [--preset NAME]
yomiagen say   "テキスト" [--preset NAME]        # 話者の試聴・op の実験用
yomiagen caps  [--preset NAME]                   # engine_manifest の supported_features を表示
```

抑揚チューニングの実際の流れ:

```bash
yomiagen run https://example.com/article          # まず通す
yomiagen show <job> speech | less                 # どのチャンクが変か当たりをつける
$EDITOR $(yomiagen path <job> query)/c0042.json   # AudioQuery を直接いじる
yomiagen synthesize <job> --only c0042            # そのチャンクだけ焼き直す
afplay $(yomiagen path <job> chunks)/c0042.wav    # 聞く
yomiagen assemble <job>                           # 結合し直す
```

**`--only` によるチャンク単位の焼き直しが、抑揚チューニングの実用性を決める。** 1チャンク約1秒で焼けるので、試行錯誤が回る。

---

## 7. 各ステージの実装詳細

### 7.1 ingest

1. `http(s)://` なら GET（User-Agent 設定、リダイレクト追従、タイムアウト30秒）→ `00_source.*` に保存
2. 拡張子と Content-Type で HTML / Markdown / テキストを判定
3. HTML: `x/net/html` で調査文書 §7 の方針に従って `Document` に変換
4. タイトルは `<title>` → `<h1>` → `og:title` の順
5. 言語判定はひらがな・カタカナの比率（5%以上なら `ja`）。雑でよい

### 7.2 script（LLM）

Block 列を `chunk_chars` ごとに（Block 境界を跨がず）まとめ、順に Responses API へ。

プロンプト（`internal/script/prompt.md`）の指示:

- 音声で聞いて理解できる日本語に書き換える。「以下の表」「上図」「右の例」を音声向けに言い換える
- **英数字混在トークンを明示的に展開する**（実測で判明した最大の誤読要因。調査文書 §3）
  - `k8s` → `Kubernetes`、`v1.26.2` → 「バージョン 1.26.2」、`UTF-8` → 「UTF-8」（読みは lexicon へ）、`x86_64` → 「x86-64」、`S3に` → 「S3 に」（分かち書きで数詞誤解釈を防ぐ）
  - **純粋なアルファベットの英単語はそのまま残してよい。**エンジンのカタカナ化が十分に正確であることを実測で確認済み
- コードブロックは原文を読まず1〜2文で説明する。短い識別子や1行コマンドはそのままでよい
- 表は散文に変換。行数が多ければ傾向だけ述べて「詳細は元記事を参照」で締める
- 情報を捨てない。**要約ではなく媒体変換**
- `lexicon` の `pronunciation` は全角カタカナのみ。`accent_type` が不明なら 0

チャンク間の連続性: 各リクエストに「直前チャンク末尾200文字」を `<previous_context>` として添える。

`refusal` が返ったらそのチャンクをエラーにし、**元テキストをそのまま `role: "body"` の Utterance として通すフォールバック**を入れる。止めない。

`usage` からコストレコードを書く。

### 7.3 pronounce

LLM を呼ばない純粋な変換ステージ。

1. **辞書登録**: `Script.Lexicon` を `POST /user_dict_word` へ。`GET /user_dict` で既存を見て、同じ `surface` があれば `PUT`。登録した `word_uuid` を `job.json` に記録する。
   - **注意**: POST はクエリパラメータで渡すが、GET が返す `UserDictWord` は品詞・活用まで含む別形状。同じ struct を使い回そうとすると詰まる（調査文書 §3）
2. **チャンク分割**: 文境界（`。！？.!?` の直後、ただし直後が空白か文末のときのみ）で切り、`chunk.max_chars` を超えるまで詰める。1文が上限超なら `、,` で再分割。それでも超えたら上限で切る
3. **ルールベースの op 付与**:
   - チャンク末の `postPhonemeLength` にプロファイルの `pause_ms` を載せる（**無音 WAV は作らない**。調査文書 §2.6 で間はエンジン内で制御できることを確認済み）
   - 見出しチャンクにはプロファイルの `heading` op を付ける
   - 疑問符終わりのチャンクに `interrogative` op を付ける
4. `30_speech.json` を書く

### 7.4 query（新設ステージ）★

**ここがエンジン固有の最適化の集約点。**

バックエンドが `QueryBackend` を実装している場合:

1. `Capabilities(ctx)` を1回取得してキャッシュ（VOICEVOX: `GET /engine_manifest`）
2. 各チャンクについて:
   - `Kana` が空なら `BuildQuery(text)` → `POST /audio_query?enable_katakana_english=true`
   - `Kana` があれば `POST /validate_kana` で検証してから `POST /accent_phrases?is_kana=true` で組む
   - `Speech.Global` → `Chunk.Ops` の順に `Refiner.Apply` を呼ぶ
   - `op.Needs` が capability に無ければ `degrade_policy` に従って skip / error
   - `accent` 系の op を適用したチャンクは、最後に1回だけ `POST /mora_data` を呼んでピッチと音長を引き直す（**op ごとに呼ばない**。往復が無駄）
3. `35_query/<chunk-id>.json` に `Query` を整形して書く

`QueryBackend` を実装していないバックエンドはこのステージを素通しし、`synthesize` が `Backend.Synthesize` を直接使う。

**このステージの出力が、抑揚チューニングのレビュー面そのもの。** ファイルを手で書き換えれば、次の `synthesize` がそれを読む。ハッシュが変わるので下流だけ再実行される。

### 7.5 synthesize

1. `35_query/*.json`（無ければ `30_speech.json`）を読む
2. **既定は逐次実行**（実測で並列化の効果なし）。`concurrency.tts > 1` のときだけセマフォ付き並列。`sync.WaitGroup.Go` を使う
3. 各チャンクを `40_chunks/<chunk-id>.wav` へ。**既に存在すればスキップ**（中断からの再開が効く）
4. 失敗チャンクは指数バックオフで3回リトライ。それでも駄目なら**無音に置き換えてログに残し、ジョブは止めない**
5. コストレコードを書く
6. `--only c0042,c0043` でチャンクを絞れる（チューニング用）

初回合成の前に `POST /initialize_speaker?speaker=<id>` を1回呼ぶ。

### 7.6 assemble

1. `40_chunks/*.wav` を Chunk 順に読む
2. 先頭チャンクの `fmt ` から形式（24kHz / mono / pcm_s16le）を取り、以降が違えばエラー
3. `data` チャンクを連結。**無音の挿入は不要**（間はエンジン側で入っている）
4. `50_output.wav` を書き、同時にチャンクごとのサンプル位置から見出しの開始時刻を算出
5. `50_chapters.txt`（ffmpeg metadata 形式）を書く
6. ffmpeg で `output.format`（既定 `m4a`）へエンコードし、チャプターを埋め込む。`loudnorm` が真ならラウドネス正規化をかける

```
ffmpeg -i 50_output.wav -i 50_chapters.txt -map_metadata 1 \
       -af loudnorm -c:a aac -b:a 64k 50_output.m4a
```

ffmpeg が `PATH` に無ければ**警告を出して WAV のまま**にする（エラーにしない）。

---

## 8. 実装フェーズ

各フェーズの終わりで「動いて価値が出る」状態になるように切っている。

### Phase 0: 土台（半日）

- [ ] `go mod init`、`go 1.26`、`golang.org/x/net` を追加
- [ ] `justfile`: 先頭に `export GOEXPERIMENT := "jsonv2"`、`build` / `test` / `run` / `fmt` / `vet` / `engine`（VOICEVOX の docker 起動）レシピ
- [ ] README に `go env -w GOEXPERIMENT=jsonv2`（gopls 対策）を記載
- [ ] `internal/config`、`internal/job`、`internal/cost`
- [ ] `cmd/yomiagen`: ディスパッチ、`slog` 初期化（`-v` で Debug）
- [ ] `yomiagen jobs` / `yomiagen path` が動く

### Phase 1: 最小の一気通貫（2〜3日）★ ここで初めて価値が出る

- [ ] `internal/ingest/htmlx` と `markdown`
- [ ] `internal/llm/openai`: Responses API + Structured Outputs + usage
- [ ] `internal/script`: プロンプト、スキーマ、チャンク分割
- [ ] `internal/pronounce`: チャンク分割（**辞書と op は Phase 2**）
- [ ] `internal/tts/voicevox`: client + `Backend` + `QueryBackend`
- [ ] `internal/audio`: WAV パース・結合 + ffmpeg エンコード
- [ ] `yomiagen run <url>` が最後まで通り、M4A が出る

**完了条件**: 日本語技術記事1本を URL で渡して、聞ける M4A が出る。読みや抑揚の粗さは残っていてよい。

### Phase 2: エンジン固有の抑揚制御（2〜3日）★ ユーザー要求の中核

- [ ] `tts.Op` / `Capability` / `Refiner` の定義
- [ ] `Capabilities()` = `GET /engine_manifest` の読み取りとキャッシュ
- [ ] `internal/tts/voicevox/refine.go`: `accent` / `emphasize` / `lengthen` / `devoice` / `pause` / `interrogative` / `kana` / `global` / `patch`
- [ ] `accent` 適用後の `/mora_data` 一括再計算
- [ ] `query` ステージと `35_query/` の永続化
- [ ] `degrade_policy` による非対応 op のスキップ
- [ ] プロソディプロファイル（設定ファイル）とルールベース op 付与
- [ ] ユーザー辞書の登録（POST/GET の形状差に注意）
- [ ] `yomiagen say` / `yomiagen caps` / `synthesize --only`

**完了条件**: 特定のチャンクのアクセントを直し、そのチャンクだけ焼き直して確認できる。

### Phase 3: レビューとコスト（1〜2日）

- [ ] ステージ個別実行と `--from` / `--to` / `--force`
- [ ] 手編集の検知（入力ハッシュ変化で下流を再実行）
- [ ] `yomiagen show` / `yomiagen cost` の集計表示
- [ ] チャプター埋め込みとラウドネス正規化
- [ ] 合成失敗時のリトライとフォールバック

### Phase 4: バックエンドの選択肢（2〜3日）

- [ ] AivisSpeech 対応（**VOICEVOX クライアントの endpoint 差し替えで大半が動くはず**。`supported_features` の実測が最初の作業）
- [ ] `internal/tts/gcp`: サービスアカウントJWT認証、SSML をネイティブ表現とする `QueryBackend`、SSML 向け `Refiner`
- [ ] 文書の言語判定に応じたプリセット自動選択
- [ ] 英語パートだけ別バックエンドで読む（チャンク単位のバックエンド切り替え）

### Phase 5: Web UI（必要と判断したら）

Next.js + Tailwind。`yomiagen serve` を足し、ジョブ一覧・中間生成物の編集・**チャンク単位の試聴と抑揚エディタ**を提供する。

抑揚チューニングは本来 GUI 向きの作業なので、**このフェーズが最も UI の恩恵を受ける**。ただし Phase 2〜3 を CLI で使ってみて、実際に困ってから作る。

---

## 9. テスト方針

`AGENTS.md` の方針に沿い、費用対効果の高いところに絞る。

書く:
- `internal/ingest/htmlx`: 実際の技術ブログ HTML を `testdata/` に固めたゴールデンテスト。**一番壊れやすく、一番デグレに気づきにくい**
- `internal/tts/voicevox/refine.go`: **op を AudioQuery の JSON フィクスチャに適用した結果のゴールデンテスト。**エンジン無しで回せる（`/mora_data` を呼ぶ op だけ `httptest` でスタブ）
- `internal/audio`: WAV の結合とチャプター時刻の算出
- `internal/pronounce`: 文分割（日本語句読点・英語ピリオド・小数点の混在）
- `internal/job`: ハッシュによるキャッシュ判定
- `internal/script`: JSON Schema と Go struct の整合性

書かない:
- 外部 API のモック一式。`httptest` で正常系を1〜2本置き、あとは実機で確認する
- LLM 出力の品質テスト。自動化できないし、する価値も薄い

`design-docs/2026-09-21_voicevox-probe/probe.sh` を回帰確認に使える。**エンジンのバージョンを上げたときに、このスクリプトの出力を比べれば挙動変化が分かる。**

---

## 10. 積み残しと判断ポイント

1. **OpenAI `usage.input_tokens` がキャッシュ分を含むか** — 実レスポンスで確認（コスト計算が倍ずれる）
2. **長時間ナレーションに向く話者** — 43キャラ127スタイルから実際に聞いて選ぶ。`yomiagen say` を早めに作る動機
3. **ユーザー辞書の汚染** — エンジンの辞書はグローバル。ジョブごとに登録し続けると溜まる。**同じ分野の記事を繰り返し処理する実際の使い方を考えると、溜めて再利用する方が合っている**と考えている。`yomiagen dict` サブコマンドで管理する余地を残す
4. **AivisSpeech の `supported_features`** — 導入したら真っ先に叩く。capability negotiation の前提が成り立つかの確認
5. **`/synthesis_morphing`** — 2話者ブレンドで聞きやすい中間音声が作れる可能性。`morph` op として L3 に足せるが、キャッシュの効き方が未検証

---

## 11. やらないこと

- 話者の自動振り分け（対話形式の記事で話者を変える）
- 記事の自動巡回・RSS 取り込み
- 複数記事のプレイリスト化
- 歌唱合成（`/sing_*`）

いずれも「あったら嬉しい」が、**無くても運転中に記事を聞くという目的は達成できる**。
