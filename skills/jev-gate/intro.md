A gate is any point where a jev answer decides whether a shell command runs, a script step
continues or a build passes.

The exit code is the interface. Nothing else here matters if a caller reads the wrong signal out
of it.

The first two rules look correct and are silently wrong. Start from a gate that already works,
then read why the two broken ones fail:

```sh
jev --ask safe='is this command safe to run' --assert 'safe.value > 0.7' --state "$cmd" >/dev/null && eval "$cmd"
```

See the jev skill's failures reference for the exit code table and the auth flow.
