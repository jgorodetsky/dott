# dott

One fuzzy picker for your dotfiles: what is fine, what needs you, and the fixes.

> Status: early. The picker works, on demo rows: picking one changes nothing yet. The scan of a real dotfiles repo comes next.

```
dott              open the picker over every item
dott aws          the same, filtered: type any words, pick from what matches
dott --json       every item's state as JSON, for scripts
```

Marks in the picker:

| Mark | Kind | What happens when you pick it |
|---|---|---|
| ● | read-only | runs at once |
| ◆ | change | shows exactly what it will do, then asks `Run it? [Y/n/copy]` |
| ▲ | dangerous | asks you to type `yes` |
| ▸ | guided | opens a follow-up picker |

## Develop

```sh
git config core.hooksPath githooks   # once per clone: checks run before every push
scripts/check.sh                     # gofmt, go vet, go test -race, staticcheck, govulncheck
go build ./cmd/dott
```

## License

MIT
