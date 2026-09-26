onesie is a command line tool that asks the TypeSafe Jev model typed questions about text. It needs the `onesie` binary on PATH and an API key, from TypeSafe, OpenRouter or Berget. This skill walks a user from nothing to a working first command.

There are two ways in. A user who installs onesie first can add the plugin afterwards. A user who installs the plugin first gets a session start line saying onesie is missing or has no key, and this skill takes it from there.

The steps, in order:

1. Check what is there already: `command -v onesie`, then `onesie auth status`.
2. Install onesie if it is missing, per the install section below.
3. Ask which provider the user has an account with.
4. Have the user store the key, outside the conversation.
5. Run `onesie auth test` and confirm it reports the provider and a model count.
