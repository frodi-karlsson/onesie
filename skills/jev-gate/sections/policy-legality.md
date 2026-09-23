## Which policy flag is legal on which shape

| flag | legal on | needs |
| --- | --- | --- |
| `--threshold` | a yes/no question only | nothing else |
| `--min-confidence` | a pick or rate question only | `--fallback` |
| `--fallback` alone | any shape | nothing, it substitutes only when the request itself fails |
| `-q` on a pick or rate question | that question | `--min-confidence` with `--fallback`, or an `--assert` |
