## Which policy flag is legal on which shape

| flag | legal on | needs |
| --- | --- | --- |
| `--threshold` | a yes or no question only | nothing else. It sets `.decision` to whether the probability is at least the threshold |
| `--min-confidence` | a pick or rate question only | `--fallback`. Below it `.decision` is the fallback text and `.fallback` is `low_confidence` |
| `--fallback` | any shape | on a yes or no question it takes `true`, `false`, `yes` or `no`, and the decision stays a boolean. Alone it does not gate, it only fills `.decision` when the request fails |
| `-q` | a yes or no question | nothing else. It exits 0 when the probability is at least `--threshold`, 0.5 by default |
| `-q` | a pick or rate question | `--min-confidence` with `--fallback`, or an `--assert` |

A request that fails after retries still prints the record under a fallback, with `.error` beside
the answer, `.fallback` set to `error` and `.decision` set to the fallback. The exit code stays
the failure's, 2 through 5, so a gate still fails closed.
