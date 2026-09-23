## Which policy flag is legal on which shape

| flag | legal on | needs |
| --- | --- | --- |
| `--threshold` | a yes or no question only | nothing else |
| `--min-confidence` | a pick or rate question only | `--fallback` |
| `--fallback` alone | any shape | does not gate on low confidence, it only substitutes when the request fails |
| `-q` | a pick or rate question | `--min-confidence` with `--fallback`, or an `--assert` |
