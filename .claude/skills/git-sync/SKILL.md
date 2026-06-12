---
name: git-sync
description: Sync local git state with remote — fetch + prune, pull main, delete merged local branches, optionally rebase current feature branch.
---

# Git Sync

When the human says "sync" / "pull" / "I just merged the PR, update my local" / "what's the state?":

Refresh local git against the remote without losing work. Most common trigger: a PR was merged on GitHub, and the developer's local `main` is now stale.

## Workflow

### Step 0 — Pre-flight (always)

Run **in parallel** to understand current state:

```sh
git status --short --branch          # current branch + dirty marker
git fetch --prune origin             # refresh remote refs, drop deleted ones
git branch -vv                       # local branches with tracking info
```

(`--prune` removes remote-tracking refs for branches deleted on GitHub — important after PR merges where GitHub auto-deletes the source branch.)

**If working tree is dirty** (uncommitted or unstaged changes), STOP. Show the human and ask:

1. **Stash** — `git stash push -m "wip on <current-branch> before sync"` (safe, recoverable)
2. **Commit** — invoke [`/git-commit`](../git-commit/SKILL.md) if changes belong here
3. **Abort sync** — bail out without touching anything

Do not auto-stash. The human picks.

### Step 1 — Determine state

After `fetch --prune`, parse output to decide what to do. Key facts:

- **Current branch** — `main` or feature branch?
- **`main`'s status vs `origin/main`** — `behind N`? `up to date`?
- **Current feature branch** (if any) — was its remote deleted (PR merged + auto-cleanup)?
- **Merged-but-still-local branches** — branches whose tip is reachable from `origin/main`

```sh
git status -sb                       # "## main...origin/main [behind 3]"
git branch --merged origin/main      # candidates for deletion
```

### Step 2 — Update `main`

If `main` is behind `origin/main`:

```sh
git switch main                      # if not already there
git pull --ff-only origin main
```

`--ff-only` refuses to create a merge commit. If it fails (i.e., local `main` has diverged), **stop and surface to the human** — never auto-rebase or auto-merge `main`. Diverged `main` is a real problem that needs human attention.

### Step 3 — Clean up merged local branches

After updating `main`, find local feature branches whose work is now in `main`:

```sh
# Anchored regex (`$`) excludes only branches NAMED exactly `main` or
# `master`, not any branch whose name happens to start with those
# substrings. A shell loop is portable.
for branch in $(git branch --merged main | grep -vE '^\s*\*?\s*(main|master)$'); do
  git branch -d "$branch"
done
```

On Windows/PowerShell, the equivalent:

```powershell
git branch --merged main |
  ForEach-Object { $_.Trim().TrimStart('* ').Trim() } |
  Where-Object { $_ -and $_ -notmatch '^(main|master)$' } |
  ForEach-Object { git branch -d $_ }
```

`git branch -d` is **safe** — it refuses to delete branches that have unmerged commits. So this is non-destructive even if the heuristic is wrong.

**Report deletions:**

```
Deleted local branches (work already in main):
  - feat/zod-note-schemas
  - feat/notes-service-layer
```

### Step 4 — Special case: current branch was the merged one

If the human ran `/git-sync` while on a feature branch whose remote was just deleted (PR merged + GitHub's auto-cleanup):

```sh
# 1. Leave the merged branch (you cannot delete the branch you're on).
git switch main

# 2. Bring main up to date with origin/main (same as Step 2).
git pull --ff-only origin main

# 3. Safe-delete the now-merged branch locally.
git branch -d <merged-branch>
```

Report:
> "Branch `feat/X` was merged into main. Switched to main, pulled latest, deleted `feat/X` locally."

### Step 5 — Optional: rebase current feature branch on new `main`

If the human is on a **non-merged** feature branch and `main` moved, ask:

> "main moved by 3 commits while you were on `feat/Y`. Rebase `feat/Y` on the new main? (Will run `git rebase main`.)"

Wait for confirmation. On yes:

```sh
git switch feat/Y                    # back to feature branch
git rebase main                      # replay feature commits on top
```

**On rebase conflicts:** stop, do not `--continue`, `--skip`, or `--abort`. Surface to the human:

```
Rebase conflict on commit <sha>:
  CONFLICT (content): Merge conflict in server/<file>

Files with conflicts:
  - server/services/notes.ts

What now?
  1. Resolve manually, then `git add <file> && git rebase --continue`
  2. Abort: `git rebase --abort` (returns to pre-rebase state)
```

### Step 6 — Final report

Summarise what changed:

```
Sync done.

Updated:
  ✓ main: 3 new commits from origin
  ✓ pruned 2 stale remote refs

Cleaned up:
  ✓ deleted local branch feat/zod-note-schemas (merged)

Current state:
  • on branch: main
  • working tree: clean
  • main: up to date with origin/main

Optionally:
  - Open PRs: 2 (run `gh pr list --author @me` to see)
```

If `gh` (GitHub CLI) is installed, append open PR count. Don't list them unless asked — keep the report short.

## Hard rules

- ❌ **Never `git pull` without `--ff-only`** on main — accidental merge commits pollute history
- ❌ **Never `git push` from this skill** — `/git-sync` is pull-only
- ❌ **Never `git branch -D`** (force delete) — only `-d` (safe). If `-d` refuses, the branch has unmerged work; surface that
- ❌ **Never auto-resolve rebase conflicts** — surface, let human resolve
- ❌ **Never `--rebase` or `--no-ff` pull on main** — diverged main = human attention
- ❌ **Never delete the current branch** — switch first
- ❌ **Never run with dirty tree without confirming** — stash / commit / abort first

## When sync isn't enough

`/git-sync` does not handle:

- **Diverged `main`** — local `main` has commits not on remote (e.g., committed directly). Human must rebase or reset; this skill won't guess.
- **Force-pushed remote branch** — remote `feat/X` was force-pushed while you had local work on it. Human must decide whether to discard local or rebase.
- **Long-running branches needing manual conflict resolution** — surface, don't attempt.
- **Stale `origin` URL** — `origin` points wrong (e.g., after fork transfer). Run `git remote -v` and surface.

In these cases, **report the situation in plain language and stop**. Do not attempt destructive recovery.

## When to invoke

- ✅ After merging a PR on GitHub
- ✅ At the start of a coding session (sync before branching off)
- ✅ Periodically during long work (every few hours)
- ✅ Before starting a new feature — ensures the new branch starts from latest `main`
- ❌ Right after [`/git-commit`](../git-commit/SKILL.md) — sync is for pulling, not pushing

## Integration with other skills

- Before starting new work — run `/git-sync` first so the new feature branch is from the latest `main`
- Between pieces of work — if a slice was merged (PR closed), `/git-sync` cleans up before the next branch is created

## Resume one-liner

> **`/git-sync` = fetch + prune + ff-only pull on main + safe-delete merged local branches + optional rebase of current feature branch on new main. Pull-only, never pushes. Refuses to touch dirty tree without confirmation. Surfaces conflicts, never resolves them.**
