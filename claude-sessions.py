#!/usr/bin/env python3
"""List Claude Code sessions for a directory and all its subdirectories.

Usage: claude-sessions [DIR] [--all] [--full] [--days N]
  DIR       root directory (default: current directory)
  --all     include trivial sessions (no real prompt, or only /resume etc.)
  --full    show full session IDs (for `claude --resume <id>`)
  --days N  only sessions active in the last N days
"""
import argparse, datetime, glob, json, os, re

p = argparse.ArgumentParser(description="List Claude Code sessions under a directory tree.")
p.add_argument("dir", nargs="?", default=os.getcwd())
p.add_argument("--all", action="store_true", help="include trivial sessions")
p.add_argument("--full", action="store_true", help="show full session IDs")
p.add_argument("--days", type=int, help="only sessions active in the last N days")
a = p.parse_args()

root = os.path.realpath(os.path.expanduser(a.dir))
# Claude Code encodes a project path by replacing every non-alphanumeric char with '-'
prefix = re.sub(r"[^A-Za-z0-9]", "-", root)
base = os.path.expanduser("~/.claude/projects")
cutoff = datetime.datetime.now() - datetime.timedelta(days=a.days) if a.days else None


def prompt_text(o):
    c = o.get("message", {}).get("content")
    if isinstance(c, list):
        c = " ".join(x.get("text", "") for x in c if isinstance(x, dict) and x.get("type") == "text")
    return c.strip() if isinstance(c, str) else ""


rows = []
for d in os.listdir(base):
    if not (d == prefix or d.startswith(prefix + "-")):
        continue
    for f in glob.glob(os.path.join(base, d, "*.jsonl")):
        mtime = datetime.datetime.fromtimestamp(os.path.getmtime(f))
        if cutoff and mtime < cutoff:
            continue
        cwd = title = first = None
        n = 0
        try:
            with open(f, errors="replace") as fh:
                for line in fh:
                    try:
                        o = json.loads(line)
                    except ValueError:
                        continue
                    cwd = cwd or o.get("cwd")
                    t = o.get("type")
                    if t == "custom-title":
                        title = o.get("customTitle") or title  # user rename wins
                    elif t in ("summary", "ai-title") and not title:
                        title = o.get("summary") or o.get("aiTitle")
                    elif t == "user" and not o.get("isMeta"):
                        s = prompt_text(o)
                        if s and not s.startswith("<"):
                            n += 1
                            first = first or s
        except OSError:
            continue
        trivial = n == 0 or (n == 1 and (first or "").startswith(("/", ":")))
        if trivial and not a.all:
            continue
        # cwd can be outside root when the dir-name prefix matches a sibling (e.g. foo vs foo-bar)
        if cwd and not (cwd == root or cwd.startswith(root + os.sep)):
            continue
        sid = os.path.basename(f)[:-6]
        rel = os.path.relpath(cwd, root) if cwd else "?" + d
        rows.append((mtime, rel, sid if a.full else sid[:8], n, " ".join((title or first or "").split())))

rows.sort(reverse=True)
if not rows:
    print(f"no sessions under {root}")
    raise SystemExit
w_dir = min(max(len(r[1]) for r in rows), 60)
w_id = len(rows[0][2])
print(f"{'LAST ACTIVE':16}  {'DIR':{w_dir}}  {'ID':{w_id}}  {'MSGS':>4}  TITLE")
for m, rel, sid, n, title in rows:
    if len(rel) > w_dir:
        rel = "…" + rel[-(w_dir - 1):]
    print(f"{m:%Y-%m-%d %H:%M}  {rel:{w_dir}}  {sid:{w_id}}  {n:>4}  {title[:80]}")
print(f"\n{len(rows)} sessions under {root}")
