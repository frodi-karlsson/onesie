onesie asks the TypeSafe Jev model one typed question about a piece of text from the command line.
State goes in on stdin, `--state TEXT` or `--state-file PATH`, and `--state -` reads stdin. An
empty or all whitespace state exits 2 before any request. A typed answer comes out on stdout, and
the exit status is usable in a conditional.

There are three question shapes, and the shape decides what you get back:

```sh
onesie 'is this urgent' --state 'the server is down'
onesie --ask team='which team owns this' --pick billing,shipping,support --state 'my package never arrived'
onesie --ask anger='how angry is the writer' --rate calm,annoyed,furious --state 'I want a refund now'
```

A positional question with no `--ask` is keyed `answer` in the output, in `-o values`, and in
every `--assert` path.

With no `-o`, output is a table when stdout is a terminal and one json line otherwise, and always
json for a stream or under `--merge`. Pass `-o json` or `-o values` in a script, so a run in a
terminal parses the same way.

On a non zero exit or a key error, read `references/failures.md`.
