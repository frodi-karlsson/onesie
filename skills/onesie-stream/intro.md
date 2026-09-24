onesie streams over many records with `-i jsonl`, `-i lines` or `-i request`, one record per line
in and one answer per line out.

A failed record does not stop the run. It prints an error record in its place and the run keeps
going, so a consumer has to check which kind of line it is looking at before it reads an answer
out of it. See the onesie skill's failures reference for the exit code table.

```sh
onesie --ask urgent='is this urgent' -i jsonl -j 8 < tickets.jsonl | jq -c 'select(.error == null)'
```
