# Publishing

Herdr's [official marketplace](https://herdr.dev/plugins/) automatically indexes
community plugins from GitHub. Listing does not imply review or endorsement.

## Repository

Publish as `spancerxing/herdr-telegram-bridge`, a public, non-archived GitHub
repository. The marketplace
excludes GitHub forks, so use an independent repository for this project and
retain the upstream attribution in `THIRD_PARTY_NOTICES.md`.

Keep `herdr-plugin.toml` on the default branch and add the GitHub repository
topic `herdr-plugin`. The marketplace refreshes approximately every 30 minutes.
There is no submission PR to a central official plugin repository.

The public plugin ID is `spancerxing.telegram-bridge`. Changing it later creates
a different plugin registration and config/state directory, so treat the ID as
fixed once published.

## Distribution

The current manifest builds from source during installation. Users need Go at
the version specified in `go.mod`, Git, and Herdr 0.9.0 or later:

```sh
herdr plugin install spancerxing/herdr-telegram-bridge
```

CI runs Go tests with the race detector, static checks, Pi extension tests, and the
build on Linux and macOS. Tests requiring a live Herdr instance skip on CI;
the mutating Pi end-to-end test requires an explicit `make e2e` invocation.

Prebuilt release binaries are an optional follow-up to remove the user's Go
toolchain requirement; they are not required for marketplace discovery.

## Components

The `herdr-tg` executable and manifest are the Herdr plugin. The Pi companion
extension is embedded in the executable and is installed with
`herdr-tg install-extension`; it does not need its own package or marketplace
listing. Pi users also run `herdr integration install pi` to install Herdr's
own state-reporting extension. Claude, Codex, and agy do not use the Pi extension.

## Local checks

```sh
make test
make vet
make build
```

Only publish source, tests, and documentation. `.gitignore` excludes local
binaries, bot config, mapping state, logs, and environment files. Configuration
and credentials normally live outside the repository. After publishing, test
installation from GitHub on a clean machine and check the CI results before
announcing the plugin.
