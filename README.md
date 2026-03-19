# Pudding

Orchestrate code agents via declarative YAML recipes.

## Step 1 (current)

- `pudding cook <spec> --recipe tdd` — dry-run: parse recipe, load config, validate, print execution plan (no agent execution).
- `pudding cookbook list` — list available recipes (project, user, built-in).
- `pudding cookbook show <name>` — show recipe YAML.
- `pudding config` — show merged configuration and source of each value.
- `pudding doctor` — stub: git, config, built-in recipes check.
- `pudding --version` — print version.

## Build and test

```bash
go build .
go test ./e2e/ -v
```

E2E tests run the `pudding` binary with an environment where `ANTHROPIC_API_KEY` is unset, to avoid the Claude CLI ByteString error (doctor or adapter). If you run `pudding doctor` manually and hit that error, run `unset ANTHROPIC_API_KEY` in your shell or leave the variable empty in your `.env` / Cursor settings.
