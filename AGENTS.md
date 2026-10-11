# AGENTS.md — sethucms-adapter-template-go

Template for an adapter written in Go (gRPC).

This repository is one part of SethuCMS. **Read `../sethucms/AGENTS.md` first** for the whole picture, the security rules that must not be broken, and the working agreements.

## Build and test

```sh
sh scripts/dev.sh tools && sh scripts/dev.sh proto && go mod tidy && sh scripts/dev.sh test
```

## Notes

UNVERIFIED so far: the server code was written but never compiled. Run the commands first and fix what they report.

## Always

- Run the tests for what you changed and say what you ran and what you could not run.
- Do not `git commit` or `git push` unless the owner asks.
- Never print or store a secret, token or password.
