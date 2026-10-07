# Yomiagen

HTML、Markdown、テキストを読み上げ向けに整形し、VOICEVOX で音声化する CLI です。各工程の結果はジョブディレクトリに JSON として残るため、アクセントや読みを手で調整して一部だけ再合成できます。

## セットアップ

Go 1.26 の JSON v2 を使用します。エディタからも認識できるよう、最初に一度だけ設定してください。

```sh
go env -w GOEXPERIMENT=jsonv2
just build
```

VOICEVOX ENGINE を `127.0.0.1:50021` で起動してから使います。Docker を使う場合は `just engine` で起動できます。`OPENAI_API_KEY` を設定すると、OpenAI による読み上げ台本への変換に加え、各チャンクの読み・アクセント句・区切りを VOICEVOX のカナ記法で自動設計します。生成されたカナ記法は VOICEVOX で検証され、不正な場合はそのチャンクだけ通常の自動解析に戻ります。未設定の場合は原文の構造を保ったローカル変換で最後まで処理します。

発音の自動設計だけを無効にする場合は、設定ファイルの `openai.pronunciation` を `"off"` にします。未指定または `"auto"` なら有効です。

```sh
./yomiagen run article.html
./yomiagen jobs
./yomiagen show <job-id> speech
./yomiagen path <job-id> query
./yomiagen synthesize <job-id> --only c0003 --force
./yomiagen assemble <job-id>
```

データは既定で `~/.local/share/yomiagen` に保存されます。`YOMIAGEN_HOME` で変更できます。
