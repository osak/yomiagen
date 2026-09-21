#!/usr/bin/env bash
# VOICEVOX ENGINE の挙動を実測するための再現スクリプト。
# 使い方:
#   docker run -d --rm -p 50021:50021 --name yomiagen-vv voicevox/voicevox_engine:cpu-latest
#   ./probe.sh
set -euo pipefail
VV=${VV:-http://127.0.0.1:50021}
SPEAKER=${SPEAKER:-3}
enc() { python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$1"; }
kana() { curl -s -X POST "$VV/audio_query?text=$(enc "$1")&speaker=$SPEAKER&enable_katakana_english=${2:-true}" \
         | python3 -c "import json,sys;print(json.load(sys.stdin)['kana'])"; }

echo "## version: $(curl -s "$VV/version")"

echo "## 技術用語の読み（enable_katakana_english=true）"
for w in "goroutineを起動" "PostgreSQLのindex" "HTTP/2のstream" "O(n log n)の計算量" \
         "v1.26.2をリリース" "3xの高速化" "S3にupload" "gRPCのinterceptor" \
         "UTF-8でencode" "x86_64向けbuild" "50%の削減" "A/Bテスト" "CI/CDパイプライン" "k8sのSecret"; do
  printf '%-24s -> %s\n' "$w" "$(kana "$w")"
done

echo "## 並列合成に意味があるか（逐次 vs 並列）"
for i in 1 2 3 4 5 6; do
  curl -s -X POST "$VV/audio_query?text=$(enc "これは並列合成のテストです。チャンク番号は${i}番になります。")&speaker=$SPEAKER" > "/tmp/q$i.json"
done
echo "--- 逐次 6件 ---"
time (for i in 1 2 3 4 5 6; do curl -s -X POST "$VV/synthesis?speaker=$SPEAKER" -H 'Content-Type: application/json' -d @/tmp/q$i.json -o /tmp/s$i.wav; done)
echo "--- 並列 6件 ---"
time (for i in 1 2 3 4 5 6; do curl -s -X POST "$VV/synthesis?speaker=$SPEAKER" -H 'Content-Type: application/json' -d @/tmp/q$i.json -o /tmp/p$i.wav & done; wait)

echo "## アクセント変更 -> /mora_data でピッチが再計算されるか"
curl -s -X POST "$VV/accent_phrases?text=$(enc 'これはテストです')&speaker=$SPEAKER" > /tmp/ap.json
python3 -c "
import json; d=json.load(open('/tmp/ap.json'))
print('before:',[(p['accent'],[(m['text'],round(m['pitch'],3)) for m in p['moras']]) for p in d])
d[0]['accent']=1; json.dump(d,open('/tmp/ap2.json','w'),ensure_ascii=False)"
curl -s -X POST "$VV/mora_data?speaker=$SPEAKER" -H 'Content-Type: application/json' -d @/tmp/ap2.json \
  | python3 -c "import json,sys;d=json.load(sys.stdin);print('after :',[(p['accent'],[(m['text'],round(m['pitch'],3)) for m in p['moras']]) for p in d])"

echo "## 間の制御（pauseLength / postPhonemeLength）"
curl -s -X POST "$VV/audio_query?text=$(enc 'あいうえお、かきくけこ。')&speaker=$SPEAKER" > /tmp/pq.json
for pl in null 0.1 1.5; do
  python3 -c "
import json; d=json.load(open('/tmp/pq.json')); d['pauseLength']=None if '$pl'=='null' else float('$pl')
json.dump(d,open('/tmp/pq2.json','w'))"
  curl -s -X POST "$VV/synthesis?speaker=$SPEAKER" -H 'Content-Type: application/json' -d @/tmp/pq2.json -o /tmp/pl.wav
  echo "pauseLength=$pl -> $(ffprobe -v error -show_entries format=duration -of csv=p=0 /tmp/pl.wav)s"
done

echo "## エンジンの機能申告"
curl -s "$VV/engine_manifest" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['supported_features'],ensure_ascii=False,indent=1))"
