`onesie calibrate` asks a question about records whose right answer is already known, and prints
what each cut catches and what it flags by mistake. It never picks the cut. That is your call,
made from the report.

`-i` is jsonl, csv or tsv. `--map` picks the text the question reads, and a `--label` jq
expression takes the right answer from the same record:

```sh
onesie calibrate --ask urgent='is this urgent' -i jsonl --map '.body' \
    --label urgent='.is_urgent' --id '.id' --out answers.jsonl --resume < labelled.jsonl
```

A yes or no label is true, false, yes, no, 1 or 0 in any case. A pick or rate label is an option or
level name exactly as declared. A null, empty or missing label leaves the record out for that
question. A question file's `assert`, `abstain_if`, `threshold`, `min_confidence` and `fallback`
are ignored, so a gated file calibrates as it stands, while the same flags typed on the command
line exit 2.

The built in starter sets come calibrated already. `-f shell-safety` and the others that
`onesie questions` lists as `built in` ship with cuts measured on labelled records, so use one
as it stands before you write and calibrate a gate of your own.

For a flag this skill does not cover, run `onesie --help` or `onesie calibrate --help`, and `onesie -V` for the version and the built in limits.
