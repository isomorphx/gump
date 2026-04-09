# Gump

**Workflow runtime for AI coding agents. With stats.**
Define your workflow in YAML. Gump runs the agents, validates each step, retries on failure, and captures cost, duration, and outcomes — per step, per agent, per attempt.

> **Status: Alpha.** Core engine works. CLI surface and workflow schema may change before v1. Feedback welcome.

```
gump run implement-spec --spec spec.md
```

```
[decompose]                  pass     3 tasks              $0.31   8s
[build/task-1/converge]      pass     5 files changed      $0.18   54s
[build/task-1/smoke]         pass                          $0.04   12s
[build/task-2/converge]      pass     8 files changed      $0.36   4m   (4 attempts, escalated to opus)
[build/task-2/smoke]         pass                          $0.06   15s
[build/task-3/converge]      pass     3 files changed      $0.14   48s
[build/task-3/smoke]         pass                          $0.03   10s
[quality]                    gate     compile ✓  lint ✓  test ✓

run passed — 3 tasks, 8 steps, 1 escalation, $1.12, 7m
→ gump apply     merge result
→ gump report    full metrics
```

---

## Why This Exists

You launch an AI coding agent, watch the terminal, hope it doesn't go off the rails, and start over when it does. The agent is powerful, but unsupervised. You're babysitting.

Gump makes this structured. You describe the workflow once — decompose, implement, gate, review — and Gump executes it. Each step follows three phases: **GET** the context, **RUN** the agent, **GATE** the result. Failed steps retry with the error context injected back into the prompt, escalate to a stronger model, or restart clean. Every step produces metrics.

The thesis: **the value isn't just in the agent, it's in the workflow around it.** The right agent for the right step, with the right guardrails, at the right cost. Measured, not guessed.

### What Gump is not

- **Not an agent.** Gump orchestrates agents. It makes zero LLM calls itself.
- **Not an observability tool.** It doesn't reconstruct what happened after the fact. It structures the execution before it starts and tracks metrics live from the agent stream.
- **Not a framework.** No SDK, no plugins, no runtime dependency in your code. It's a CLI that reads YAML, runs agents, and gets out of the way.

---

## Install

```
# macOS / Linux
brew install isomorphx/tap/gump
```

Or download the latest binary from the [Releases](https://github.com/isomorphx/gump/releases) page.

Requires at least one supported agent CLI installed: Claude Code, Codex, Gemini CLI, Qwen Code, OpenCode, or Cursor CLI.

```
gump doctor   # verify your environment
```

---

## Quickstart

```
# Run a built-in workflow against a spec file
gump run tdd --spec spec.md

# Preview execution plan without running anything
gump run tdd --spec spec.md --dry-run

# Merge the result into your branch
gump apply
```

Gump creates an isolated Git worktree for every run. Your working branch is never touched until you explicitly `gump apply`.

### What every run gives you

- An isolated Git worktree — the original branch stays clean
- Validation gates between steps — compile, test, lint, schema, or your own validator workflows
- Automatic retries with error context, model escalation, and worktree reset on failure
- Structured metrics — cost, tokens, turns, duration, context window usage, files changed, retries — per step and per run
- Resume a crashed run or replay from any step

---

## Example Workflow

The built-in `tdd` workflow, shortened. [See the full version →](docs/playbook.md)

```yaml
name: tdd
max_budget: 5.00

steps:
  - name: decompose
    type: split
    get:
      prompt: |
        Analyze this spec and the codebase.
        Decompose into independent tasks.
    run:
      agent: claude-opus
    gate: [schema]
    each:
      - name: tests
        type: code
        get:
          prompt: "Write tests for: {task.description}"
        run:
          agent: claude-haiku
          guard: { max_turns: 40 }
        gate: [compile, tests_found]
        retry:
          - exit: 3

      - name: impl
        type: code
        get:
          prompt: "Implement code to pass all tests."
          session: from: tests
        run:
          agent: claude-haiku
          guard: { max_turns: 60 }
        gate: [compile, test]
        retry:
          - attempt: 3
            agent: claude-sonnet
            session: new
          - exit: 5

  - name: quality
    gate: [compile, lint, test]
```

`decompose` uses Opus to split the spec into tasks. For each task, `tests` writes failing tests, then `impl` makes them pass. If `impl` fails 3 times with Haiku, Gump switches to Sonnet with a fresh session. `quality` is a standalone gate — no agent, just compile + lint + test on the final worktree.

---

## Built-in Workflows

| Workflow | Strategy |
|----------|----------|
| `freeform` | Single agent, no plan, minimal gates |
| `tdd` | Decompose → tests first → implement → quality gate |
| `cheap2sota` | Start cheap, escalate to SOTA on failure |
| `parallel-tasks` | Decompose into disjoint tasks, implement in parallel |
| `implement-spec` | Decompose → implement → adversarial review → converge → smoke |
| `bugfix` | Reproduce with test → patch → verify |
| `refactor` | Decompose → refactor with test preservation |

```
gump playbook list
gump playbook show tdd
```

Write your own workflows in `.gump/workflows/` or `~/.gump/workflows/`.

---

## Telemetry

Gump collects anonymous usage metrics to understand which workflows and agents are used in practice. This is opt-in by default and can be disabled at any time.

**What is collected:** workflow name, agent names, step count, pass/fail, duration, cost per step, turns, retries, guard triggers, tokens, context window usage, TTFD, OS, architecture, repo language, repo size.

**What is never collected:** source code, prompts, file paths, spec content, task names, or anything that identifies you or your project.

Telemetry is tied to an anonymous UUID stored in `~/.gump/anonymous_id`. To opt out:

```
gump config set analytics false
```

See [docs/telemetry.md](docs/telemetry.md) for the full list of fields and details.

---

## Current Limitations

- **Alpha software.** Workflow schema and CLI flags may change.
- **No sandboxing.** Agents run with your permissions. Docker isolation is planned, not shipped.
- **Guards are reactive.** They kill the agent after detecting a violation, not before. File mutations are rolled back, but external side effects are not.
- **Cost is estimated.** Token-based cost estimation (~80% confidence) for providers that don't report native cost.
- **Linux and macOS only.** No Windows support yet.

---

## Documentation

| Topic | Link |
|-------|------|
| Core concepts (steps, types, gates, guards, state, retry) | [docs/concepts.md](docs/concepts.md) |
| Workflow reference (GET/RUN/GATE, split/each, session, retry) | [docs/workflows.md](docs/workflows.md) |
| Agent compatibility | [docs/agents.md](docs/agents.md) |
| Configuration reference | [docs/config.md](docs/config.md) |
| CLI reference | [docs/cli.md](docs/cli.md) |
| Metrics and reporting | [docs/metrics.md](docs/metrics.md) |

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).