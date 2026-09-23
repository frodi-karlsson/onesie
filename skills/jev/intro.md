jev asks the TypeSafe Jev model one typed question about a piece of text from the command line.
State goes in on stdin or `--state`, a typed answer comes out on stdout, and the exit status is
usable in a conditional.

There are three question shapes, and the shape decides what you get back:

```sh
jev 'is this urgent' --state 'the server is down'
jev --ask team='which team owns this' --pick billing,shipping,support --state 'my package never arrived'
jev --ask anger='how angry is the writer' --rate calm,annoyed,furious --state 'I want a refund now'
```

Batching is the default, not an optimisation. Ask every question you need about one piece of text
in a single call, and spend nothing finding out whether a command is well formed before you spend
anything on the model itself.
