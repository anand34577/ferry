# Contributing

Thanks for helping! Please:

1. Open an issue first for larger changes so we can agree on the approach.
2. Set up the dev environment as described in [docs/development.md](docs/development.md).
3. Keep changes focused; match the surrounding style. Prefer the standard library over new dependencies.
4. Add or update tests: `go test ./...` (backend, also against Postgres if you touch SQL), `npm test && npm run lint` (web), `./gradlew testDebugUnitTest` (Android).
5. User-facing errors must be plain language with a next step; never log secrets or share tokens.
6. Transfer state names must stay identical across server, web and Android.

By contributing you agree your work is licensed under the MIT license. Security issues: see [SECURITY.md](SECURITY.md).
