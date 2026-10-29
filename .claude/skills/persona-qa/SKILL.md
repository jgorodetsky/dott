---
name: persona-qa
description: Test dott the way first-time users do, before a release. Six subagents each play one persona. The personas are impatient, skeptic, small and slow terminal, literal reader, breaker and fresh Mac. Each persona drives dott on a fake machine with cmd/dott-drive and reports what confused it or failed. Then each finding that the user picks becomes a journey test that fails first, and a fix. Use this skill before scripts/release.sh. Also use it when the user says "persona QA", "run the personas", "test dott like a new user", "first users" or "QA before the release". Do not use it on the real HOME, and do not run it on every push.
metadata:
  argument-hint: "[persona names to run, or nothing for all six]"
---

# Persona QA for dott

Six personas use dott for the first time and tell you where it fails them. Each persona is a subagent. The subagents only run `cmd/dott-drive`, which builds a fake machine (the test fixture, with stub `brew`, `npx` and `uvx`) in a temp folder. Thus no persona touches the real HOME, the real dotfiles repo or a network.

## Safety rules

- Run dott only through `go run ./cmd/dott-drive`, from the dott repo root.
- Do not run the `dott` binary directly. Do not set `HOME`, `DOTT_REPO` or `DOTT_CONFIG`.
- The personas report. They do not change files in the repo.
- This skill costs tokens. Run it only when the user asks, before a release. It is not a push gate.

## Steps

1. Make sure that the driver works. This command must print a screen with `dott ›`:

   ```sh
   go run ./cmd/dott-drive -step 'wait dott ›' -step snap -step 'key esc'
   ```

2. Read `references/personas.md`. If the user named personas, use only those. Otherwise use all six.
3. Start one subagent for each persona, all in one message, so that they run at the same time. Give each subagent this brief, with its persona text:
   - The persona text from `references/personas.md`.
   - The driver usage: the comment at the top of `cmd/dott-drive/main.go`.
   - The README sections "The picker", "Add things" and "Setup and updates".
   - The safety rules above.
   - Its own folder name for `-keep` runs: the persona name and a number, like `skeptic-3`. Personas run at the same time, and two runs in one folder break both.
   - The report format below.
4. When all reports are back, merge them into one table. Remove duplicates. Put each finding in one class:
   - **bug**: dott did something wrong, or something that it says it never does.
   - **confusing**: dott did the right thing, but the persona could not tell what to do or what happened.
   - **as designed**: dott does what it must do. Give the reason in one line.
5. Show the merged table to the user. Put the bugs first. Then stop, and let the user pick which findings to fix.
6. For each finding that the user picks:
   1. Write a journey test in `cmd/dott/pty_test.go` that shows the problem. Run it and see it fail.
   2. Fix the code.
   3. Run `scripts/check.sh`. All checks must pass.

## Report format

Each persona returns this, and nothing else:

```
summary: <3 lines at most: what worked, what did not>

| # | severity | what I did | what I expected | what happened |
|---|---|---|---|---|
| 1 | high/medium/low | the full dott-drive command | one line | one line, then a screen excerpt of 10 lines at most |
```

Severity:

- **high**: data loss, a change without a yes, a crash, a hang, or a lie on the screen.
- **medium**: the persona could not finish a task, or had to guess.
- **low**: a word, a layout or a hint that can be better.
