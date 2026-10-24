// claude-sessions lists Claude Code sessions for a directory and all its subdirectories.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/urfave/cli/v3"
)

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	cyan   = "\x1b[36m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
)

type row struct {
	mtime time.Time
	rel   string
	id    string // possibly shortened, for display
	sid   string // full session ID
	cwd   string // directory to resume in
	n     int
	title string
	name  string // session title, empty if the session has none
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// parentSessionVars are set by a running Claude Code session and mark its children;
// inheriting them makes the resumed session think it is a nested child (e.g. transcript saving off).
var parentSessionVars = []string{
	"CLAUDECODE", "CLAUDE_PID",
	"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
}

func cleanEnv() []string {
	drop := make(map[string]bool, len(parentSessionVars))
	for _, k := range parentSessionVars {
		drop[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	return env
}

// tabTitle is the session's name if it has one, else its directory.
func tabTitle(r row) string {
	if r.name != "" {
		return r.name
	}
	return filepath.Base(r.cwd)
}

// resumeHere replaces this process with `claude --resume sid`, run from dir.
func resumeHere(dir, sid string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	return syscall.Exec(bin, []string{"claude", "--resume", sid}, cleanEnv())
}

// openSession opens the session in a new tab of the running kitty (needs remote control),
// falling back to a detached new kitty window. The tab is titled title and cd's to dir.
func openSession(dir, title, sid string) error {
	tab := exec.Command("kitty", "@", "launch", "--type=tab", "--tab-title", title, "--cwd", dir, "claude", "--resume", sid)
	if err := tab.Run(); err == nil {
		return nil
	}
	win := exec.Command("kitty", "--title", title, "--directory", dir, "claude", "--resume", sid)
	win.Env = cleanEnv()
	win.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := win.Start(); err != nil {
		return err
	}
	return win.Process.Release()
}

type entry struct {
	Type        string `json:"type"`
	Cwd         string `json:"cwd"`
	IsMeta      bool   `json:"isMeta"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
	Summary     string `json:"summary"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func promptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// scan reads one session file; ok is false if it could not be opened.
func scan(path string) (cwd, title, first string, n int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	ok = true
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var e entry
			if json.Unmarshal(line, &e) == nil {
				if cwd == "" {
					cwd = e.Cwd
				}
				switch {
				case e.Type == "custom-title":
					if e.CustomTitle != "" {
						title = e.CustomTitle // user rename wins
					}
				case e.Type == "summary" || e.Type == "ai-title":
					if title == "" {
						title = e.Summary
						if title == "" {
							title = e.AITitle
						}
					}
				case e.Type == "user" && !e.IsMeta:
					if s := promptText(e.Message.Content); s != "" && !strings.HasPrefix(s, "<") {
						n++
						if first == "" {
							first = s
						}
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func run(_ context.Context, c *cli.Command) error {
	dir := c.Args().First()
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[1:])
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	all, full := c.Bool("all"), c.Bool("full")
	var cutoff time.Time
	if d := c.Int("days"); d > 0 {
		cutoff = time.Now().AddDate(0, 0, -d)
	}

	// Claude Code encodes a project path by replacing every non-alphanumeric char with '-'
	prefix := nonAlnum.ReplaceAllString(root, "-")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(base)
	if err != nil {
		return err
	}

	var rows []row
	for _, d := range dirs {
		name := d.Name()
		if !(name == prefix || strings.HasPrefix(name, prefix+"-")) {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(base, name, "*.jsonl"))
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				continue
			}
			mtime := st.ModTime()
			if !cutoff.IsZero() && mtime.Before(cutoff) {
				continue
			}
			cwd, title, first, n, ok := scan(f)
			if !ok {
				continue
			}
			trivial := n == 0 || (n == 1 && (strings.HasPrefix(first, "/") || strings.HasPrefix(first, ":")))
			if trivial && !all {
				continue
			}
			// cwd can be outside root when the dir-name prefix matches a sibling (e.g. foo vs foo-bar)
			if cwd != "" && !(cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator))) {
				continue
			}
			sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			rel := "?" + name
			if cwd != "" {
				if rel, err = filepath.Rel(root, cwd); err != nil {
					rel = cwd
				}
			}
			full_ := sid
			if !full {
				sid = truncate(sid, 8)
			}
			rcwd := cwd
			if rcwd == "" {
				rcwd = root
			}
			name := strings.Join(strings.Fields(title), " ")
			if title == "" {
				title = first
			}
			rows = append(rows, row{mtime, rel, sid, full_, rcwd, n, strings.Join(strings.Fields(title), " "), name})
		}
	}

	if len(rows) == 0 {
		fmt.Printf("no sessions under %s\n", root)
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mtime.After(rows[j].mtime) })

	color := isTTY(os.Stdout) && os.Getenv("NO_COLOR") == ""
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + reset
	}

	wDir := 0
	for _, r := range rows {
		wDir = max(wDir, utf8.RuneCountInString(r.rel))
	}
	wDir = min(wDir, 60)
	wID := utf8.RuneCountInString(rows[0].id)
	wNum := len(strconv.Itoa(len(rows)))
	fmt.Println(paint(bold+dim, fmt.Sprintf("%*s  %-16s  %-*s  %-*s  %4s  TITLE", wNum, "#", "LAST ACTIVE", wDir, "DIR", wID, "ID", "MSGS")))
	for i, r := range rows {
		rel := r.rel
		if rl := []rune(rel); len(rl) > wDir {
			rel = "…" + string(rl[len(rl)-(wDir-1):])
		}
		fmt.Printf("%s  %s  %s  %s  %s  %s\n",
			paint(bold+yellow, fmt.Sprintf("%*d", wNum, i+1)),
			paint(dim, r.mtime.Format("2006-01-02 15:04")),
			paint(blue, fmt.Sprintf("%-*s", wDir, rel)),
			paint(cyan, fmt.Sprintf("%-*s", wID, r.id)),
			paint(green, fmt.Sprintf("%4d", r.n)),
			truncate(r.title, 80))
	}
	fmt.Printf("\n%s\n", paint(dim, fmt.Sprintf("%d sessions under %s", len(rows), root)))

	if !isTTY(os.Stdin) || !isTTY(os.Stdout) || c.Bool("no-select") {
		return nil
	}
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(paint(bold, "Open # (enter to quit): "))
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" || line == "q" {
			return nil
		}
		k, convErr := strconv.Atoi(line)
		if convErr != nil || k < 1 || k > len(rows) {
			fmt.Println(paint(yellow, "invalid selection"))
			if err != nil {
				return nil
			}
			continue
		}
		r := rows[k-1]
		if !c.Bool("tab") {
			return resumeHere(r.cwd, r.sid)
		}
		if err := openSession(r.cwd, tabTitle(r), r.sid); err != nil {
			return fmt.Errorf("opening session: %w", err)
		}
		fmt.Printf("opened %s in %s\n", r.sid[:8], r.cwd)
		return nil
	}
}

func main() {
	app := &cli.Command{
		Name:      "claude-sessions",
		Usage:     "List Claude Code sessions under a directory tree",
		ArgsUsage: "[DIR]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "include trivial sessions (no real prompt, or only /resume etc.)"},
			&cli.BoolFlag{Name: "full", Usage: "show full session IDs (for claude --resume <id>)"},
			&cli.BoolFlag{Name: "tab", Aliases: []string{"t"}, Usage: "open the selected session in a new kitty tab instead of this terminal"},
			&cli.BoolFlag{Name: "no-select", Usage: "just list; don't prompt to resume a session"},
			&cli.IntFlag{Name: "days", Usage: "only sessions active in the last `N` days"},
		},
		Action: run,
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
