# Branch management — supporting reference for `/git-commit`

Detailed rules for creating, naming, switching, pushing, and merging branches in this project. Referenced from [SKILL.md](SKILL.md).

## Default model — trunk-based with short-lived feature branches

```
main (protected, always green)
  ↑
  ├── feat/<short-name>     ← one feature, one area — 30 min to 1 day
  ├── fix/<short-name>      ← one bug
  ├── refactor/<short-name> ← one focused refactor
  ├── docs/<short-name>     ← non-trivial doc work (rare; trivial → straight to main)
  └── chore/<short-name>    ← dependency bumps, tooling, CI tweaks
```

**Properties:**
- Lifetime: hours to one day. Anything older smells.
- One PR per branch (even if you self-merge — for CI to run).
- Squash-merge or rebase-merge — **no merge-commits** with a "Merge branch X" message.
- Delete the branch after merge (`git push origin --delete <branch>` or via PR UI).

**Branches we do NOT use:**
- ❌ `develop` (Git Flow) — too heavy for a project this size
- ❌ `release/*` — main is the release branch
- ❌ Long-lived feature branches (>2 days) — break them into smaller slices

## Naming conventions

```
<type>/<short-kebab-name>
```

`<type>` matches the Conventional Commits type that will dominate the branch:

| Type | When |
|---|---|
| `feat/` | New functionality |
| `fix/` | Bug fix |
| `refactor/` | Internal rework, no behaviour change |
| `perf/` | Performance work |
| `docs/` | Documentation (only if non-trivial) |
| `chore/` | Maintenance (deps, tooling) |
| `ci/` | Workflow changes |

`<short-kebab-name>`:
- **3–6 words**, kebab-case
- Describes **the change**, not the affected area alone
- Mirrors the eventual commit subject (without the type/scope prefix)

### Good

```
feat/notes-service-layer
feat/zod-note-schemas
feat/note-editor-composable
fix/note-visibility-filter
fix/auth-token-refresh
refactor/extract-note-visibility-where
chore/bump-nuxt-4.0.1
ci/pin-pnpm-version
```

### Bad

```
feat/notes                     ← area, not change
fix/bug                        ← what bug?
my-branch                      ← no type, no clue
yurii-2026-05-28               ← who/when, not what
feat/new-feature               ← tautological
feat/add-the-thing-that-does-stuff-for-notes-and-auth  ← too long
```

## Creating a new branch — concrete commands

**Always start from a clean, up-to-date main:**

```sh
git switch main                          # or `git checkout main`
git pull --ff-only origin main           # fail-fast if local main diverged
git switch -c feat/<short-name>          # create & switch
```

`--ff-only` is important — it refuses to merge accidentally. If it fails, surface to the human; never auto-resolve.

**Equivalent older syntax (still works):**

```sh
git checkout main
git pull --ff-only origin main
git checkout -b feat/<short-name>
```

## Switching branches

```sh
git switch <branch>            # safe — refuses if dirty working tree
git switch -                   # toggle to the previous branch
```

**Before switching with dirty changes**, decide:
1. **Commit them** — if they belong on this branch
2. **Stash them** — `git stash push -m "<reason>"` (for temporary side-trip)
3. **Discard them** — only with explicit human confirmation

**Do not:** force-switch (`git switch -f`) without confirmation — loses uncommitted work.

## When the human says "start a new branch from here"

Three cases:

### Case A — On main, clean tree

```sh
git switch -c feat/<short-name>
```

Done.

### Case B — On main with uncommitted changes

These changes should probably be on the new branch:

```sh
git switch -c feat/<short-name>      # carries uncommitted changes with you
git status                            # confirm changes are still there
```

### Case C — On another feature branch

Decision needed:
- **Branch from here** (rare — only if there's a real dependency between branches):
  ```sh
  git switch -c feat/<next> --no-track
  ```
- **Stash, switch to main, create from main** (default — independent slices):
  ```sh
  git stash push -m "wip on <current-branch>"
  git switch main && git pull --ff-only
  git switch -c feat/<next>
  ```

Surface this choice to the human. **Default: from main**.

## Push conventions

**First push** of a new branch — sets the upstream:

```sh
git push -u origin feat/<short-name>
```

**Subsequent pushes** — just:

```sh
git push
```

**Never:**
- ❌ `git push --force` to main (and almost never to a shared branch)
- ❌ `git push --force-with-lease` without confirming — even the "safer" force-push is destructive

**Force-push to your own feature branch** is OK after a `git rebase` / `git commit --amend` **before** the PR is reviewed. Once review starts, prefer adding fixup commits.

## Merge strategies (main is protected)

Three options when merging a feature branch into main. Pick by branch's commit history:

### 1. Squash merge (default for most slices)

`gh pr merge --squash` or GitHub UI button.

- All branch commits compressed into one commit on main
- The squash commit message becomes the **branch's purpose**
- Branch history lost — that's the point: linear, clean main

**Use when:** the branch has many small WIP commits that are not individually meaningful (`"WIP"`, `"fix typo"`, `"actually working now"`).

### 2. Rebase + fast-forward merge

```sh
git switch feat/<name>
git rebase main
git switch main
git merge --ff-only feat/<name>
git push origin main
git branch -d feat/<name>
git push origin --delete feat/<name>
```

- Branch's individual commits land on main as-is
- Main stays linear (no merge-commit)

**Use when:** the branch's commits are **individually meaningful and atomic** (each one passes `pnpm run typecheck && pnpm run test`, each one has a real Conventional-Commit message). This is **the goal** of disciplined branch work — and exactly what `/git-commit` produces.

### 3. Merge commit (avoid in this project)

`git merge feat/<name>` without `--ff-only` — creates a merge commit. **Don't use** unless preserving the branch as a parallel history is a real requirement. Almost never.

### Quick decision table

| Branch shape | Merge strategy |
|---|---|
| 1 atomic commit | Fast-forward |
| 2–5 atomic Conventional-Commits | Rebase + fast-forward |
| Many WIP commits | Squash |

## PR conventions

```sh
gh pr create --title "<type>(<scope>): <subject>" --body "$(cat <<'EOF'
## Summary
- <bullet>
- <bullet>

## Test plan
- [ ] `pnpm run typecheck` is clean
- [ ] `pnpm run lint` reports no problems
- [ ] `pnpm run test` passes
- [ ] Manual: <describe>

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

**PR title = best commit subject** of the branch (or the squash-merge subject if squashing).

**PR body** includes:
- Short summary (3–5 bullets, what changed and why)
- Test plan (concrete checks, not "I ran tests")
- Co-Author trailer if AI generated substantive parts

## Pulling latest main into your branch (during long work)

If `main` moved while you were working, rebase to stay current:

```sh
git fetch origin
git rebase origin/main          # on your feature branch
```

Resolve conflicts (if any), then continue. **Don't `git merge origin/main` into your branch** — it creates an ugly merge-commit you'll have to deal with later.

**If conflicts are complex or you're unsure** — stop and ask the human. Never run `git rebase --skip` or `git rebase --abort` without confirmation.

## What to **never** do without explicit human confirmation

- ❌ `git push --force` (any branch)
- ❌ `git reset --hard` (destroys uncommitted work)
- ❌ `git checkout .` / `git restore .` (same)
- ❌ `git branch -D <branch>` (force-delete unmerged branch)
- ❌ `git rebase --skip` / `--abort`
- ❌ Switch with `-f` flag
- ❌ Delete `main` or rename it
- ❌ Merge to main without a PR (depends on team policy — surface and confirm)
- ❌ `git clean -fd` (deletes untracked files)

If a destructive operation seems necessary, **always** describe what will happen and wait for confirmation.
