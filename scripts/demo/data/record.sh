#!/bin/sh
# Records real model answers for the README demo, which scripts/demo/make-assets.py replays. Run it yourself, with your own onesie profile.
# It asks 34 questions: 2 simple asks, 2 single tickets and 30 labelled tickets for calibrate.
# It writes only answers and the calibrate report to this directory, never a key.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
cd "$here"

(cd "$repo" && go build -o "$here/onesie" ./cmd/onesie)
./onesie version | head -n 1

echo 'the checkout page is down for everyone' |
	./onesie 'is this urgent' -o json > simple-yes-no.json
echo 'I was charged twice for my order' |
	./onesie 'who should handle this' --pick billing,shipping,technical -o json > simple-pick.json

./onesie --ask urgent='does this ticket need someone within the hour' --print-questions > urgent.yaml

echo 'checkout is down for everyone, customers cannot pay' |
	./onesie -f urgent.yaml -o json > single-urgent.json
echo 'please update the billing address on my invoice' |
	./onesie -f urgent.yaml -o json > single-calm.json

./onesie calibrate -f urgent.yaml -i jsonl --map .text --label .urgent --cuts 0.3,0.5,0.7,0.9 \
	--out answers.jsonl < tickets.jsonl > report.txt

echo "recorded:"
ls -1 simple-yes-no.json simple-pick.json urgent.yaml single-urgent.json single-calm.json answers.jsonl report.txt
cat simple-yes-no.json simple-pick.json single-urgent.json single-calm.json
cat report.txt
