# Contributing to PubCraft

Thanks for helping improve PubCraft. Testing, clear bug reports, documentation, translations, design feedback, and code contributions are all welcome.

## Before opening an issue

- Search existing issues to avoid duplicates.
- Include the PubCraft version, operating system, installation type, exact steps, and expected versus actual behavior.
- Use a small synthetic project to reproduce manuscript or layout problems.
- Never attach private manuscripts, unpublished writing, credentials, or copyrighted covers.

## Development setup

Requirements: Make, a C compiler, and Go 1.23 or newer.

```sh
git clone https://github.com/massigent/PubCraft.git
cd PubCraft
make
make check
```

Browser tests are documented in [`tests/README.md`](tests/README.md). They require the optional test dependencies and a dedicated Chromium/Brave remote-debugging session. Do not run tests against a personal browser profile or private manuscript.

## Pull requests

1. Open or link an issue for substantial changes so the intended behavior can be discussed first.
2. Keep changes focused and preserve Markdown project compatibility.
3. Add or update regression tests for behavior changes.
4. Run `make check`; run relevant browser tests when changing the UI.
5. Describe what changed, how it was tested, and any platform limitations.

Please do not include generated binaries, private book projects, or build output in pull requests. Font and third-party assets have separate licenses; check their notices before adding or redistributing assets.

## Alpha project expectations

PubCraft is an evolving volunteer project. A report or pull request is not a promise of a release date. Be kind, assume good intent, and focus discussion on reproducible behavior and useful improvements.
