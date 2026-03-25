# Gump

**Declarative workflow runtime for AI coding agents.**
Define your workflow in YAML. Gump runs the agents, validates each step, retries on failure, and captures cost, duration, and outcomes for every step.

> **Status: Alpha.** Core engine works. CLI surface and workflow schema may change before v1. Feedback welcome.

```
gump run spec.md --workflow tdd
```

```
✓ decompose    claude-opus     plan   3 items     $0.42   12s
✓ build/item-1
  ✓ tests      claude-haiku    diff   14 turns    $0.08    2m
  ✓ impl       claude-haiku    diff   22 turns    $0.11    3m
✓ build/item-2
  ✓ tests      claude-haiku    diff   11 turns    $0.06    1m
  ✓ impl       claude-haiku    diff   18 turns    $0.09    2m
  ⟳ impl       claude-sonnet   diff   12 turns    $0.31    2m  (escalated, attempt 4)
✓ build/item-3
  ✓ tests      claude-haiku    diff    9 turns    $0.05    1m
  ✓ impl       claude-haiku    diff   15 turns    $0.07    2m
✓ quality      gate            pass   compile + lint + test

run passed — 3 items, 6 steps, 1 escalation, $1.19, 13m
→ gump apply     merge result
→ gump report    full metrics
```

---

## Why This Exists

You launch an AI coding agent, watch the terminal, hope it doesn't go off the rails, and start over when it does. The agent is powerful, but unsupervised. You're babysitting.

Gump makes this structured. You describe the workflow once — decompose, implement, gate, review — and Gump executes it. Steps are validated by explicit pass/fail checks (compile, test, lint). Failed steps retry automatically or escalate to a stronger model. Every step produces metrics.

The thesis: **the value isn't just in the agent, it's also in the workflow around it.** The right agent for the right step, with the right guardrails, at the right cost. Measured, not guessed.

### What Gump is not

- **Not an agent.** Gump orchestrates agents. It makes zero LLM calls itself.
- **Not an observability tool.** It doesn't capture sessions after the fact. It structures the work before it starts.
- **Not a framework.** No SDK, no plugins, no runtime dependency in your code. It's a CLI that reads YAML, runs agents, and gets out of the way.

---

## Install

```
# macOS / Linux
brew install gump-run/tap/gump
```

Or download the latest binary from the [Releases](https://github.com/gump-run/gump/releases) page.

Requires at least one supported agent CLI installed: Claude Code, Codex, Gemini CLI, Qwen Code, OpenCode, or Cursor CLI.

```
gump doctor   # verify your environment
```

---

## Quickstart

```
# Run a built-in TDD workflow against a spec file
gump run spec.md --workflow tdd

# Preview execution plan without running anything
gump run spec.md --workflow tdd --dry-run

# Merge the result into your branch
gump apply
```

Gump creates an isolated Git worktree for every run. Your working branch is never touched until you explicitly `gump apply`.

### What every run gives you

- An isolated Git worktree — the original branch stays clean
- Validation gates between steps — compile, test, lint, schema
- Automatic retries and model escalation on failure
- Structured metrics — cost, tokens, turns, duration, files changed, retries, per step and per run

---

## Example Workflow

The built-in `tdd` workflow, slightly shortened. [See the full version →](docs/playbook.md)

```yaml
name: tdd
max_budget: 5.00

steps:
  - name: decompose
    agent: claude-opus
    output: plan
    prompt: |
      Analyze this spec and the codebase.
      Decompose into independent items.
    gate: [schema]

  - name: build
    foreach: decompose
    steps:
      - name: tests
        agent: claude-haiku
        output: diff
        prompt: "Write tests for: {item.description}"
        guard: { max_turns: 40 }
        gate: [compile, tests_found]
        on_failure:
          retry: 2
          strategy: [same, "escalate: claude-sonnet"]

      - name: impl
        agent: claude-haiku
        output: diff
        session: reuse
        prompt: "Implement code to pass all tests."
        guard: { max_turns: 60 }
        gate: [compile, test]
        on_failure:
          retry: 5
          strategy: ["same: 3", "escalate: claude-sonnet"]
          restart_from: tests

  - name: quality
    gate: [compile, lint, test]
```

`decompose` uses Opus to produce a plan. `build` iterates over each item — tests first, then implementation. If `impl` fails 3 times, it escalates to Sonnet. If that fails, it restarts from `tests`. `quality` is a standalone gate with no agent.

---

## Built-in Workflows

| Workflow | Strategy |
|----------|----------|
| `tdd` | Decompose → tests first → implement → quality gate |
| `cheap2sota` | Start cheap, escalate to SOTA on failure |
| `parallel-tasks` | Decompose into disjoint items, implement in parallel |
| `adversarial-review` | Implement → parallel multi-agent review → arbitrate → converge |
| `bugfix` | Reproduce with test → patch → verify |
| `refactor` | Decompose → refactor with test preservation |
| `freeform` | Single agent, no plan, minimal gates |

```
gump playbook list
gump playbook show tdd
```

Write your own workflows in `.gump/workflows/` or `~/.gump/workflows/`.

---

## Current Limitations

- **Alpha software.** Workflow schema and CLI flags may change.
- **No sandboxing.** Agents run with your permissions. Docker isolation is planned, not shipped.
- **Guards are reactive.** They kill the agent after detecting a violation, not before. File mutations are rolled back, but external side effects are not.
- **Cost is estimated.** Token-based cost estimation for providers that don't report native cost.
- **Linux and macOS only.** No Windows support yet.

---

## Documentation

| Topic | Link |
|-------|------|
| Core concepts (steps, gates, guards, state bag) | [docs/concepts.md](docs/concepts.md) |
| Workflow reference (output modes, on_failure, session) | [docs/workflows.md](docs/workflows.md) |
| Agent compatibility | [docs/agents.md](docs/agents.md) |
| Configuration reference | [docs/config.md](docs/config.md) |
| CLI reference | [docs/cli.md](docs/cli.md) |
| Metrics and reporting | [docs/metrics.md](docs/metrics.md) |

---

## Contributing

[TODO — link to CONTRIBUTING.md]

## License

[TODO]