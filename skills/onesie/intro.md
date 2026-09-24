onesie asks the TypeSafe Jev model one typed question about a piece of text from the command line.
State goes in on stdin or `--state`, a typed answer comes out on stdout, and the exit status is
usable in a conditional.

There are three question shapes, and the shape decides what you get back:

```sh
onesie 'is this urgent' --state 'the server is down'
onesie --ask team='which team owns this' --pick billing,shipping,support --state 'my package never arrived'
onesie --ask anger='how angry is the writer' --rate calm,annoyed,furious --state 'I want a refund now'
```

A positional question with no `--ask` is keyed `answer` in the output, in `-o values`, and in
every `--assert` path.

On a non zero exit or a key error, read `references/failures.md`.
