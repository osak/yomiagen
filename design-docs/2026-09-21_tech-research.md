# Yomiagen 技術調査

最終更新日時: 2026-09-21

読者: 人間（実装方針を判断する人）を主、AI実装者を従とする。
目的: `2026-09-21_initial.md` のコンセプトを実装するにあたり、技術スタック候補の実際の仕様・制約・コストを確認し、設計上の分岐点を潰す。
姉妹文書: `2026-09-21_implementation-plan.md`（実装プラン）、`2026-09-21_voicevox-probe/`（実測の再現スクリプトと OpenAPI 定義）

**この文書の大半は、ローカルで VOICEVOX ENGINE 0.25.2 を実際に動かして計測した結果に基づく。** 推測の箇所は明示する。

---

## 0. 調査のサマリ

| 論点 | 結論 | 根拠 |
|---|---|---|
| Go の json v2 | `encoding/json/v2` は Go 1.26.2 に同梱されているが **`GOEXPERIMENT=jsonv2` が必須** | 実機検証 |
| 主バックエンド | **VOICEVOX ENGINE 0.25.2**（導入済み）。AivisSpeech は未導入なので後回し | 環境 |
| 抑揚の細かい制御 | **VOICEVOX ではモーラ単位まで完全に制御できる。**アクセント変更後は `/mora_data` でピッチと音長が自動再計算される | 実測 |
| エンジン機能の判別 | **`GET /engine_manifest` の `supported_features` がエンジン自身の機能申告を返す。** これを capability negotiation に使える | 実測 |
| 間の制御 | `pauseLength` / `postPhonemeLength` / `pause_mora.vowel_length` がすべて効く。**無音は WAV 結合ではなくエンジン内部で入れられる** | 実測 |
| 並列合成 | **意味がない。** 逐次 7.23s / 並列 7.27s。エンジンが内部で直列化している。**並列度の既定値は 1** | 実測 |
| 合成速度 | RTF ≈ 0.22（実時間の約 4.5 倍速で生成）。20分の音声 ≈ 4.5分 | 実測 |
| 英語の読み | **想定よりずっと良い。**純粋な英単語はほぼ正しい。壊れるのは**数字・記号を含む英数字トークン**に集中 | 実測 |
| ffmpeg | **導入済み（9.0.2）**。M4A + チャプター出力を前提にしてよい | 環境 |
| LLM | OpenAI Responses API + Structured Outputs。`gpt-5.6-luna` なら記事1本 $0.01 未満 | 公式ドキュメント |
| 外部依存 | `golang.org/x/net/html` 1本のみ | — |

---

## 1. Go 1.26 / encoding/json/v2

### 実機確認

`go version go1.26.2 darwin/arm64` で最小プログラムを検証した。

```
# GOEXPERIMENT なし
imports encoding/json/jsontext: build constraints exclude all Go files in .../src/encoding/json/jsontext
imports encoding/json/v2:       build constraints exclude all Go files in .../src/encoding/json/v2

# GOEXPERIMENT=jsonv2 あり
{ "name": "あ" } <nil>
```

パッケージはツールチェーンに同梱されているが build constraint で無効化されている。

### 実装上の帰結

- `justfile` の全レシピで `export GOEXPERIMENT := "jsonv2"`。
- gopls も同じ環境変数を見ないと「そんなパッケージはない」と言い続ける。`go env -w GOEXPERIMENT=jsonv2` で永続化するのが最も事故が少ない。
- 設定漏れは「ローカルでは通るのに CI で落ちる」典型パターンになる。

### この用途で効いてくる差分

- **`omitzero`**: 「0 と未設定が区別できない」問題が消える。TTS のパラメータは 0.0 が有効値になりうる（`postPhonemeLength: 0`）ので、これは実用上ありがたい。
- **`json.MarshalWrite` / `json.UnmarshalRead`**: `io.Writer` / `io.Reader` に直接読み書き。中間生成物をファイルに吐く用途と相性が良い。
- **`jsontext.Value`**: 生の JSON を型付けせずに保持・部分書き換えできる。**後述するエンジンネイティブ表現の保持に、これが設計上の鍵になる。**
- 整形は `jsontext.WithIndent("  ")`。v1 の `MarshalIndent` は無い。

---

## 2. VOICEVOX ENGINE 0.25.2（実測）

`docker run -d --rm -p 50021:50021 voicevox/voicevox_engine:cpu-latest` で起動し、`/openapi.json` を取得して全エンドポイントを列挙した。定義は `2026-09-21_voicevox-probe/openapi-0.25.2.json` に保存してある。

### 2.1 エンドポイント一覧（実在するもの）

| 分類 | エンドポイント |
|---|---|
| クエリ生成 | `POST /audio_query`（`text, speaker, enable_katakana_english, core_version`）<br>`POST /audio_query_from_preset`<br>`POST /accent_phrases`（`text, speaker, is_kana, enable_katakana_english`） |
| **クエリ再計算** | `POST /mora_data` / `POST /mora_length` / `POST /mora_pitch`（いずれも `speaker`） |
| 合成 | `POST /synthesis`（`speaker, enable_interrogative_upspeak`）<br>`POST /multi_synthesis` / `POST /cancellable_synthesis`<br>`POST /synthesis_morphing`（`base_speaker, target_speaker, morph_rate`） |
| 音声ユーティリティ | `POST /connect_waves`（base64 WAV 配列を結合して返す） |
| カナ記法 | `POST /validate_kana` |
| 話者 | `GET /speakers` / `GET /speaker_info` / `POST /initialize_speaker` / `GET /is_initialized_speaker` |
| 辞書 | `GET /user_dict` / `POST /user_dict_word` / `PUT,DELETE /user_dict_word/{uuid}` / `POST /import_user_dict` |
| 歌唱 | `/singers` `/sing_frame_audio_query` `/sing_frame_f0` `/sing_frame_volume` `/frame_synthesis` |
| メタ | `GET /version` / `GET /core_versions` / `GET /engine_manifest` / `GET,POST /setting` / `GET /supported_devices` |

> README には `/streaming_synthesis` の記載があるが **0.25.2 の OpenAPI には存在しない**。より新しいバージョンの機能と思われる。実装で当てにしないこと。

### 2.2 `engine_manifest.supported_features` — これが設計の鍵

```json
{
 "adjust_mora_pitch": true,
 "adjust_phoneme_length": true,
 "adjust_speed_scale": true,
 "adjust_pitch_scale": true,
 "adjust_intonation_scale": true,
 "adjust_volume_scale": true,
 "adjust_pause_length": true,
 "interrogative_upspeak": true,
 "synthesis_morphing": true,
 "sing": true,
 "manage_library": false,
 "return_resource_url": true,
 "apply_katakana_english": true
}
```

**エンジンが自分の機能を機械可読な形で申告している。** 調査前は「VOICEVOX と AivisSpeech の差分をこちらで管理しなければならない」と考えていたが、その必要はない。**エンジンに聞けばよい。**

AivisSpeech は README で `pitch` / `vowel_length` / `consonant_length` を無視すると明記しているので、`adjust_mora_pitch` と `adjust_phoneme_length` を `false` で申告するはず（※AivisSpeech 未導入のため未検証）。

これにより「エンジン固有の最適化を書きつつ、非対応エンジンでは自動的に降格させる」設計が、こちらの決め打ちテーブルなしで実現できる。

### 2.3 AudioQuery の実構造（OpenAPI より）

```jsonc
{
  "accent_phrases": [ /* AccentPhrase[] */ ],
  "speedScale": 1.0,          // 全体の話速
  "pitchScale": 0.0,          // 全体の音高
  "intonationScale": 1.0,     // 全体の抑揚
  "volumeScale": 1.0,         // 全体の音量
  "prePhonemeLength": 0.1,    // 音声の前の無音時間（秒）
  "postPhonemeLength": 0.1,   // 音声の後の無音時間（秒）
  "pauseLength": null,        // 句読点の無音時間。null で無視。既定 null
  "pauseLengthScale": 1.0,    // 句読点の無音時間の倍率。既定 1
  "outputSamplingRate": 24000,
  "outputStereo": false,
  "kana": "コレワ'/テ'_スト、デ'_ス"   // [読み取り専用] 合成時は無視される
}
```

```jsonc
// AccentPhrase
{
  "moras": [ /* Mora[] */ ],
  "accent": 3,                 // アクセント核の位置（1始まり）
  "pause_mora": {              // 句末の無音モーラ。null なら無し
    "text": "、", "consonant": null, "consonant_length": null,
    "vowel": "pau", "vowel_length": 0.369, "pitch": 0.0
  },
  "is_interrogative": false
}

// Mora
{ "text": "コ", "consonant": "k", "consonant_length": 0.082,
  "vowel": "o", "vowel_length": 0.086, "pitch": 5.613 }
```

実測で分かった表現上の約束:

- **`pitch` は対数スケール**（5.6〜6.1 の範囲。`exp(5.6) ≈ 270Hz`）。線形の倍率をかけるのではなく、加算でシフトするのが正しい扱い。
- **無声化母音は母音を大文字にする**。「テスト」の「ス」は `"vowel": "U", "pitch": 0.0`。AquesTalk風記法の `_` に対応する。
- **`pause_mora.vowel` は `"pau"` 固定**で、`vowel_length` が無音の長さ（秒）。つまり**アクセント句単位で間を秒単位で指定できる**。
- `kana` は **読み取り専用**（VOICEVOX の場合）。合成クエリとしては無視されると OpenAPI の description に明記されている。読みを与えたければ `/accent_phrases?is_kana=true` でパースして `accent_phrases` を差し替える。

### 2.4 編集 → 再計算のループが成立する（実測）

「これはテストです」のアクセント句 0 のアクセントを 3 → 1 に変えて `/mora_data` に投げた結果:

```
before: accent=3, コ:5.589 レ:5.736 ワ:5.953   （尾高）
after : accent=1, コ:5.924 レ:5.999 ワ:5.663   （頭高）
```

**アクセント型を変えるだけで、ピッチ曲線がエンジン側で正しく引き直される。** 手でピッチを1つずつ書く必要はない。

- `/mora_data` = ピッチと音長の両方を再計算
- `/mora_pitch` = ピッチのみ
- `/mora_length` = 音長のみ

これは「読みとアクセントは記号的に指定し、音響パラメータはエンジンに任せる」という、**人間にもLLMにも扱える抽象レベル**が API として提供されているということ。抑揚制御の設計はこの層を中心に据えるべき。

### 2.5 AquesTalk風記法

`POST /validate_kana?text=...` で検証でき、エラーは構造化されて返る:

```json
{"detail":{"text":"アクセントを指定していないアクセント句があります: コンニチワ",
           "error_name":"ACCENT_NOTFOUND","error_args":{"text":"コンニチワ"}}}
```

エラー名は `UNKNOWN_TEXT` / `ACCENT_TOP` / `ACCENT_TWICE` / `ACCENT_NOTFOUND` / `EMPTY_PHRASE` / `INTERROGATION_MARK_NOT_AT_END` / `INFINITE_LOOP`。

**LLM に読み仮名を生成させる場合、この検証 API でバリデーションしてから使えば、壊れた記法が合成まで流れない。** リトライのフィードバックにも使える。

記法のルール:
- 全角カタカナのみ。アクセント句の区切りは `/`、`、` で区切ると無音が入る
- 無声化は `_` を前置、アクセント核は `'` を後置（**各句に必須**）
- 疑問形は句末に全角 `？`
- 例: `ニ'ル/ポ'インタ` → `[(accent=1,'ニル'), (accent=1,'ポインタ')]`（実測で確認）

### 2.6 間（pause）の制御（実測）

「あいうえお、かきくけこ。」の合成長:

| 設定 | 音声長 |
|---|---|
| `pauseLength: null`（既定） | 2.208s |
| `pauseLength: 0.1` | 1.899s |
| `pauseLength: 1.5` | 3.307s |
| `postPhonemeLength: 0.1`（既定） | 2.208s |
| `postPhonemeLength: 1.0` | 3.115s |

**間はすべてエンジン内部で制御できる。** チャンク間に無音 WAV を挟み込む必要はなく、`postPhonemeLength` にその分を載せればよい。実装が減るうえ、結合の継ぎ目も消える。

### 2.7 合成速度と並列度（実測）— 設計を変える発見

同一内容 6 チャンク（各約 5.3 秒の音声）を合成:

```
逐次 6件: 7.23s
並列 6件: 7.27s
```

**並列化しても一切速くならない。** ホストは 10 コアあるが、エンジンが内部で合成を直列化している（もしくは 1 リクエストで全コアを使い切っている）。

- 当初案の「並列度 3 のセマフォ」は**無意味で、メモリを食うだけ**。**既定の並列度は 1 にする。**
- RTF（Real Time Factor）≈ 0.22。**実時間の約 4.5 倍速で生成できる**。
- 見積もり: 1万文字の記事 ≒ 音声 20〜25 分 ≒ **合成 4.5〜5.5 分**。実用範囲。
- 設定項目としては残す（将来の GPU 版・複数エンジン起動・GCP バックエンドでは意味がある）。

### 2.8 話者

43 キャラクター / 127 スタイル。トーク用スタイルの例:

| ID | 話者 |
|---|---|
| 2 | 四国めたん / ノーマル |
| 3 | ずんだもん / ノーマル |
| 8 | 春日部つむぎ / ノーマル |
| 13 | 青山龍星 / ノーマル |
| 11 | 玄野武宏 / ノーマル |

**長時間のナレーション用途にどれが向くかは実際に聞いて決めるべき**なので、ここでは推奨しない。`yomiagen voices` と試聴用コマンドを用意する。

### 2.9 `/connect_waves`（実測）

base64 エンコードした WAV の配列を POST すると結合された WAV が返る。5.376s + 5.259s → 10.635s で、**サンプル単位で正確**。

ただし ffmpeg が使える今、最終結合は自前実装のほうが良い（チャプターのタイムスタンプ算出にサンプル数が要るため、どのみち自分で数える）。**`/connect_waves` は使わない。**

---

## 3. 英語混じり日本語の読み（実測）— 当初の想定は外れていた

`enable_katakana_english=true`（既定）での実測結果。`kana` フィールドの表記では**長音が母音の重複で表される**ことに注意（`オオ` = `オー`、`シイ` = `シー`）。当初この表記を誤読して「誤変換」と判断しかけたが、実際には正しい読みだった。

### 正しく読めるもの

| 入力 | 読み | 判定 |
|---|---|---|
| `goroutine` | ゴルーティン | ほぼ可（正しくはゴルーチン） |
| `PostgreSQL` | ポストグレエスキューエル | ✓ |
| `gRPC` | ジーアールピーシー | ✓ |
| `nil` | ニル | ✓ |
| `token` | トーケン | ✓ |
| `Secret` | シークレット | ✓ |
| `OAuth` | オーオース | ✓ |
| `SIGSEGV` | エスアイジーエスイージーブイ | ✓（文字読み） |
| `50%` | ゴジュッパーセント | ✓ |
| `3x` | サン エックス | ✓ |
| `CI/CD` | シーアイ、シーディー | ✓ |
| `A/B` | エイ、ビー | ✓ |
| `2026年9月21日` | ニセンニジュウロクネン クガツ ニジュウイチニチ | ✓ |

**純粋なアルファベット列の英単語は、ほぼ何もしなくてよい。** 当初「技術用語は誤読するので辞書が要る」と想定していたが、これは過大評価だった。

### 壊れるもの — 失敗は「英数字混在トークン」に集中している

| 入力 | 読み | 問題 |
|---|---|---|
| `k8s` | ケイ **ハチセカンド** | `8s` を「8秒」と解釈 |
| `v1.26.2` | ブイ **イッテンニイロク、ニ** | バージョン番号が小数＋別トークンに分解 |
| `UTF-8` | ユーティーエフ**、**ハチ | ハイフンが句切りになり「エイト」にならない |
| `x86_64` | エックス ハチジュウロク**、**ロクジュウヨン | `_` が句切り、数値として読まれる |
| `S3にupload` | エス **サンニ** アップロード | `3に` が「3に」という数詞＋助詞に |
| `HTTP/2` | エイチ**、**ティーティーピー、ニ | 分解位置がおかしい |

**パターンは明確**: 数字・ドット・スラッシュ・ハイフン・アンダースコアを含む識別子で、形態素解析器が数量表現として誤解釈する。純粋な英単語ではない。

### 設計への反映（当初案から変更）

「LLM に技術用語の読み辞書を出させる」という当初案は**投資先がずれている**。正しくは:

1. **script ステージで、英数字混在トークンを明示的に展開させる。** `k8s` → `Kubernetes`、`v1.26.2` → 「バージョン1.26.2」、`UTF-8` → `UTF-8`（読みは辞書で）。**テキストそのものを書き換えるのが最も確実**で、エンジン非依存でもある。
2. **辞書登録は補助**。同じ語が何度も出る文書で、1箇所で読みとアクセントを固定したいときに使う。

実測で `k8s` → `クーベルネティス`（accent 4）を `/user_dict_word` に登録したところ、正しく反映された（`クウベル'ネ_ティスノ`）。**辞書は確実に効く。**

### 辞書 API の注意点

`POST /user_dict_word` は **クエリパラメータ**で渡す（`surface`, `pronunciation`, `accent_type`, `word_type`, `priority`）。一方 `GET /user_dict` が返す `UserDictWord` は品詞・活用・文脈IDまで含む重い構造体で、**POST するものと GET で返るものの形が違う**。実装時に同じ struct を使い回そうとすると詰まる。

- `word_type` は `PROPER_NOUN` / `COMMON_NOUN` / `VERB` / `ADJECTIVE` / `SUFFIX`
- `priority` は 0〜10

---

## 4. AivisSpeech Engine（未導入・ドキュメント調査のみ）

導入していないため以下は**未検証**。VOICEVOX が動いている今、優先度は低い。

- デフォルトポート **10101**。VOICEVOX ENGINE と HTTP API 互換を掲げる。
- モデルは **AIVMX 形式**（Style-Bert-VITS2 / JP-Extra）。macOS では `~/Library/Application Support/AivisSpeech-Engine/Models` に置けば自動ロード。
- スタイルIDは話者UUIDのMD5上位27bit + ローカルスタイルID 5bit の 32bit 符号付き整数。VOICEVOX のような小さい固定IDではない。

**VOICEVOX との非互換点（README より）**:

| 項目 | AivisSpeech |
|---|---|
| mora の `consonant_length` / `vowel_length` / `pitch` | **常に 0.0、指定も無視** |
| `pauseLength` / `pauseLengthScale` | 無視 |
| `intonationScale` | 意味が変わる（感情表現の強さ 0.0–2.0） |
| `pitchScale` | 既定値から変えると音質劣化 |
| `kana` | **読み書き可能**（VOICEVOX は読み取り専用） |
| 非対応 | 歌唱合成、モーフィング、`/cancellable_synthesis` |

**導入するとすれば、日本語の自然さが VOICEVOX より高いとされる点が動機になる。** その代わり細かい抑揚制御は捨てることになる。`supported_features` による capability negotiation を実装しておけば、この降格は自動的に処理される。

---

## 5. Google Cloud Text-to-Speech（未検証・優先度低）

- `POST https://texttospeech.googleapis.com/v1/text:synthesize`
- `input.{text|ssml|markup}` / `voice.{languageCode,name}` / `audioConfig.{audioEncoding,speakingRate,pitch,volumeGainDb,sampleRateHertz}`
- レスポンスは `{"audioContent": "<base64>"}`
- **認証は OAuth（`cloud-platform` スコープ）。APIキー単体では不可。** 外部依存を避けるならサービスアカウントJSONから自己署名JWTを作る（標準ライブラリのみで50行程度）。開発中は `gcloud auth print-access-token` の shell out で十分。

**制限**: 1リクエスト **5,000バイト**（引き上げ不可、マルチバイトはバイト数でカウント）。Chirp 3 は 200 req/min、Neural2 等は 1,000 req/min、Long Audio は 100 req/min。

**Chirp 3: HD**: 日本語は `ja-JP-Chirp3-HD-<VoiceName>`。SSML は同期リクエストのみ。`markup` フィールドで `[pause short]` `[pause long]` が使えるが**長さは固定されず AI が文脈で調整する**ため、尺の再現性が要る用途には向かない。`speaking_rate` は 0.25x–2.0x。

**料金（参考値・要再確認）**: 公式料金ページを機械的に取得できず、第三者情報間で食い違いがある。Standard $4 / 1M文字、Neural2 $16 / 1M文字、Chirp 3 HD $30 / 1M文字 あたりのオーダー。**実装時に公式ページで確認すること。**

**位置づけ**: 「英語パートを本物の英語で読ませたい」というニーズが顕在化するまで実装しない。ただし**アーキテクチャはこのバックエンドを受け入れられる形にしておく**（AudioQuery とは全く違うネイティブ表現＝SSML を持つため、抽象の試金石になる）。

---

## 6. LLM（OpenAI）

### Responses API + Structured Outputs

`POST https://api.openai.com/v1/responses`

```json
{
  "model": "gpt-5.6-luna",
  "instructions": "...",
  "input": "...",
  "text": { "format": {
      "type": "json_schema", "name": "script",
      "schema": { }, "strict": true } }
}
```

`strict: true` の制約:
- **全プロパティを `required` に列挙する必要がある**（省略可能フィールドは作れない。`["string","null"]` の union で表現する）
- 各 object に `additionalProperties: false` が必須
- 型は string / number / boolean / array / object / enum。再帰は `$ref`
- 安全性による拒否は schema に従わず `{"type":"refusal","refusal":"..."}` で返る。**パース前に refusal を見る必要がある**

> Go struct から JSON Schema を生成するコードを書きたくなるが、**手書きのスキーマを `go:embed` で持つ方が早いし読める**。struct との乖離はテストで検出する。

### 料金（2026-09-21 時点、公式 pricing ページ）

USD / 1M トークン、`input / cached input / output`。

| モデル | input | cached | output |
|---|---|---|---|
| gpt-6-astra | 10.00 | 1.00 | 50.00 |
| gpt-5.6-sol | 4.00 | 0.40 | 20.00 |
| gpt-5.6-terra | 2.00 | 0.20 | 12.00 |
| **gpt-5.6-luna** | **0.20** | **0.02** | **1.20** |
| gpt-5-mini | 0.25 | 0.025 | 2.00 |
| gpt-5-nano | 0.05 | 0.005 | 0.40 |

**`gpt-5.6-luna` を既定にする。** 1万文字（≒8千トークン）の記事を入力し同程度を出力しても **$0.01 未満**。品質が足りなければ `gpt-5.6-terra`。

### usage（要実装時検証）

```
usage.input_tokens
usage.input_tokens_details.cached_tokens
usage.output_tokens
usage.output_tokens_details.reasoning_tokens
usage.total_tokens
```

**`input_tokens` がキャッシュ分を含むかどうかで結果が倍近く変わる。** 推測せず実レスポンスをダンプして確認すること。

### 長文の扱い

1回で長文全体を台本化させると品質が落ち、失敗時のやり直しコストが高い。**3,000〜5,000文字単位（Block境界を跨がない）に分割**し、各リクエストに「直前チャンクの末尾200文字」を文脈として添える。チャンク単位でキャッシュすれば途中だけやり直せる。

---

## 7. 入力の取り込み

`golang.org/x/net/html` で自前実装する。汎用の readability 系ライブラリは「読みやすいテキスト」を返すが、**読み上げに必要な見出しレベル・コードブロック・表の区別を落としてしまう**ため、この用途には合わない。

方針（欲張らない）:
- `<script> <style> <nav> <header> <footer> <aside> <form>` を捨てる
- `<article>` → `<main>` → `[role=main]` → `<body>` の順で本文ルートを探す
- `<h1>`〜`<h6>` / `<p>` / `<li>` / `<blockquote>` / `<pre>`,`<code>` / `<table>` / `<img alt>`,`<figcaption>` をブロックに分類

**これで技術ブログの8割は十分扱える。** 残り2割のために汎用抽出器を入れる価値は現時点では無い。

Markdown は行単位の簡易パーサで足りる。完全な CommonMark 実装は不要。

---

## 8. 音声の組み立て

### ffmpeg は導入済み（9.0.2）

前提にしてよい。ただし**合成した WAV の結合は自前で行う**。理由:

- チャプターのタイムスタンプ算出にサンプル数が必要で、どのみち自分で数える
- 同一エンジン・同一設定なら形式（24kHz / mono / pcm_s16le）が揃うので、`fmt ` と `data` チャンクを走査して連結するだけ。100行程度
- **間はエンジン側の `postPhonemeLength` で入れる**（§2.6）ので、無音 WAV の生成すら不要

ffmpeg の出番は**最終段のエンコードのみ**:
- M4A（AAC）/ MP3 への変換
- チャプター（`-i chapters.txt -map_metadata 1`）の埋め込み
- ラウドネス正規化（`loudnorm`）— 運転中に聞く用途では音量の安定は効く

24kHz mono の WAV は 20分で約 57MB。M4A 64kbps なら約 9MB。**スマートフォンに転送して聞く運用なら圧縮は実質必須。**

### 間の設計

運転中・運動中に聞く用途では、一般的な読み上げより**間を長めに取った方が理解しやすい**。

| 位置 | 目安 | 実現方法 |
|---|---|---|
| 文の区切り | 300ms | `pauseLength` |
| 段落の区切り | 600ms | チャンク末の `postPhonemeLength` |
| 見出しの前 | 1,000ms | 次チャンクの `prePhonemeLength` |
| 見出しの後 | 500ms | `postPhonemeLength` |
| h2 セクションの前 | 1,500ms | `prePhonemeLength` |

---

## 9. 依存関係

| パッケージ | 必要性 |
|---|---|
| `golang.org/x/net/html` | **必要**（HTML パース） |
| その他 | **不要**。HTTP・JSON・WAV・並行処理・CLI はすべて標準ライブラリ |

CLI は `flag` + サブコマンドごとの `flag.NewFlagSet`。cobra は要らない。

---

## 10. 未解決 / 実装時に確認すべきこと

1. **OpenAI `usage` の `input_tokens` がキャッシュ分を含むか**。実レスポンスをダンプして確認。
2. **Google Cloud TTS の正確な料金**。公式ページを直接確認する。
3. **AivisSpeech の `supported_features` が実際に何を返すか**。capability negotiation の前提が成り立つかの確認。導入したら真っ先に叩く。
4. **長時間ナレーションに向く話者**。実際に数分聞いて決める。
5. **`/synthesis_morphing` が長文ナレーションで使えるか**。2話者ブレンドで聞きやすい中間音声が作れる可能性があるが、キャッシュの効き方を含めて未検証。
