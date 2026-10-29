# The six personas

Each persona has a temperament, the tasks to try, and what to watch for. Each persona runs at least 8 journeys with `cmd/dott-drive`. Use `-step snap` after each important key, so that the report can show the screen.

## 1. Impatient

You never read. You press Enter and Esc fast, and you type before the screen is ready.

- Tasks:
  - Fix everything.
  - Open an item, then leave it.
  - Quit from three screens deep.
- Try: `key enter` and `key esc` with no `wait` or `still` between them. Also `type` text and `key enter` at once.
- Watch for: a lost key, a pick of the wrong row, a screen that does not come back, a hang.

## 2. Skeptic

You do not trust dott. You check that a look changes nothing, and that each change asks first.

- Tasks:
  - Open every item and every folded list, and change nothing.
  - Answer `n` to each question.
- Try: read the end of each driver report. It lists the changed paths. After a look, only `home/.local/state/dott` (picks, history) can change.
- Watch for:
  - A changed file after a look or after a `n`.
  - A change that did not ask first.
  - A plan that does not agree with what changed.

## 3. Small, slow and offline

Your terminal is 80x24, every tool is slow, and you have no network.

- Tasks: the main screen, an item, `refresh`, `upgrade`, `add brew`.
- Try: `-size 80x24`, `-size 60x20`, `-slow`, `-origin` (a remote with news), `-no-brew`.
- Watch for:
  - Text that dott cuts so that it means something different.
  - Columns that do not line up.
  - A cut header or legend.
  - A wait with no message, or a hang.

## 4. Literal reader

You read each word and do exactly what it says.

- Tasks: follow the hints. The hints are the grey text in the query, the preview lines, the `next:` lines, the `Run it?` choices and the legend.
- Try: type the exact words that a hint shows (for example the grey example in the empty query). Make sure that they work.
- Watch for:
  - A hint that is wrong.
  - A word that the screen uses for two different things.
  - A row that does not do what its words say.

## 5. Breaker

You try to break dott.

- Tasks:
  - Press ctrl-c during a question and during a fix.
  - Press ctrl-d at a question.
  - Give bad input to each question: empty, very long, `../..`, a path that does not exist, a quote.
  - Run `add file` with a link, with a folder, and with the repo itself.
- Try: `key ctrl-c`, `key ctrl-d`, `line ../../etc/passwd`, `line ~`.
- Watch for:
  - A crash or a Go panic.
  - A terminal that stays broken after the exit.
  - A half-done change with no backup.
  - A file outside the fake root.

## 6. Fresh Mac

You have a new Mac. Nothing is ready, you did not answer a question yet, and Homebrew is not there.

- Tasks: the first run, the optional parts question, fix everything, then a second run.
- Try: `-setup fresh`, and `-setup fresh -no-brew`.
- Watch for:
  - A question that you cannot answer.
  - A blocked item that does not say what to do next.
  - A second run that asks the same thing again.
