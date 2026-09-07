# Ciel Agent Go

Go rewrite of Ciel Agent, built incrementally from the Phase 0 foundation.

## Development

```powershell
go test ./...
go build -o ciel.exe ./cmd/hermes
./ciel.exe version
./ciel.exe doctor
./ciel.exe sessions
./ciel.exe memory get user
./ciel.exe memory set user --value "Prefers concise Go examples"
./ciel.exe skills
```

The current foundation loads `config.yaml` when present, otherwise uses the default
state path under the user's home directory. SQLite is opened with WAL mode and the
initial session/message schema is applied automatically.

Memory is stored in SQLite under the `user` and `memory` targets and injected into
the system prompt within the configured character budget. Skills are directories
containing a `SKILL.md` file with optional YAML frontmatter:

The assistant persona is configured separately and can be multiline:

```yaml
persona: |
	You are a calm senior engineer.
	Prefer practical answers and explain tradeoffs briefly.
```

```markdown
---
name: code-review
description: Review code carefully
---
Check tests, errors, and security boundaries.
```