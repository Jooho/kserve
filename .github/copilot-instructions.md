# Copilot Agent Instructions

## Release tasks

When assigned to an issue with the `release` label, follow `.github/agents/release-agent.md` exactly.

- Do NOT run `make test`, `make py-lint`, `make lint`, or any other validation/build targets
- Do NOT add your own plan or validation steps
- Only run the commands specified in `release-agent.md`
- Do NOT create baseline checks or pre-change state captures
