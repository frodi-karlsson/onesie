A gate is any point where a onesie answer decides whether a shell command runs, a script step
continues or a build passes.

The exit code is the interface. Nothing else here matters if a caller reads the wrong signal out
of it.

The first rule looks correct and is silently wrong. Start from a gate that already works, then
read why the broken one fails:

```sh
onesie --ask safe='is this command safe to run' --assert 'safe.value > 0.7' --state "$cmd" >/dev/null && eval "$cmd"
```

See the onesie skill's failures reference for the exit code table and the auth flow.
