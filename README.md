# claude-sessions

A small Go CLI that lists [Claude Code](https://claude.com/claude-code) sessions for a directory and all its subdirectories, and lets you pick one to resume. Built with [urfave/cli](https://github.com/urfave/cli) v3. It is a port of `claude-sessions.py`.

Sessions are read from `~/.claude/projects`. Claude Code stores each project under a directory named after the project path with every non-alphanumeric character replaced by `-`; the tool matches that name against the root directory and then checks each session's recorded `cwd`, so sibling directories such as `foo` and `foo-bar` aren't mixed up.

## Install

```sh
go build -o ~/.local/bin/claude-sessions .
```

## Usage

```
claude-sessions [options] [DIR]
```

`DIR` defaults to the current directory.

| Option | Alias | Description |
|---|---|---|
| `--all` | | Include trivial sessions (no real prompt, or only a slash command such as `/resume`) |
| `--full` | | Show full session IDs instead of the first 8 characters |
| `--days N` | | Only show sessions active in the last `N` days |
| `--tab` | `-t` | Open the selected session in a new kitty tab instead of the current terminal |
| `--no-select` | | Only list sessions; don't prompt for a selection |
| `--help` | `-h` | Show help |

## Output

Sessions are listed newest first:

```
#  LAST ACTIVE       DIR     ID        MSGS  TITLE
1  2026-10-24 13:34  claude  4ea8b73a     3  Golang CLI with urfave/cli
2  2026-10-24 13:33  epxgo   3b3dfa5d   401  EPXGO
```

- **DIR** is the session's working directory relative to the root you passed.
- **MSGS** counts real user prompts, not tool results or meta messages.
- **TITLE** is the session's custom or AI title, or the first prompt if it has none.
- Colors are used only when stdout is a terminal, and `NO_COLOR` turns them off.

## Resuming a session

When run interactively, the tool prompts `Open # (enter to quit):`. Enter a row number to resume that session; enter or `q` quits.

- **Default:** changes into the session's directory and replaces the process with `claude --resume <id>`, so it runs in the current terminal.
- **`--tab`:** runs `kitty @ launch --type=tab` with the session's directory. The tab is titled with the session's name, or the directory name if the session has none. This needs kitty remote control (`allow_remote_control`). If it isn't available, a new kitty window is opened instead.

Variables that a running Claude Code session sets in its environment (for example `CLAUDE_CODE_CHILD_SESSION`) are removed before launching. Otherwise the resumed session would think it was nested and turn off transcript saving.

The prompt is skipped when stdin or stdout isn't a terminal, or with `--no-select`.

## Requirements

- Go (to build)
- `claude` on your `PATH`
- kitty, only for `--tab`
