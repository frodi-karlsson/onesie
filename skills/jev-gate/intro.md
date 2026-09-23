A gate is any point where a jev answer decides whether a shell command runs, a script step
continues or a build passes.

The exit code is the interface. Nothing else here matters if a caller reads the wrong signal out
of it.

Two recipes below look correct and are silently wrong: `threshold-polarity` and
`drop-q-with-assert`. Read those first, since they are the ones that bite.
