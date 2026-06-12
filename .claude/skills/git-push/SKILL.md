---
name: git-push
description: Push local commits to the remote with safety checks — verify before push, handle first-push tracking, refuse force-push to protected branches, surface next-step options (PR, merge).
---

# Git Push

When the human says "push" / "git push" / "push it" / "publish this branch":

Push local commits to the remote with the right flags and safety checks. **This is the gate where work becomes visible to other people** — treat it with more care than a local commit.

This skill picks up where [`/git-commit`](../git-commit/SKILL.md) leaves off. If you haven't committed yet, do that first.

**Invocation is always explicit.** Never auto-push after a commit, never bundle a push into another workflow. The human must say "push" / invoke `/git-push` / similar.

## Workflow

### Step 0 — Pre-flight state

Run **in parallel**:

```sh
git branch --show-current                # what branch are we pushing?
git status --short --branch              # ahead / behind state, dirty files
git log @{u}..HEAD --oneline 2>/dev/null # commits that would be pushed (empty if no upstream)
git rev-parse --abbrev-ref @{u} 2>/dev/null   # current upstream, if any
```

Interpret the output:

```
Are there uncommitted changes (dirty working tree)?
├── YES → Surface them. Ask: "Stash, commit, or push only what's already committed?"
│        Never auto-stash, never run `git add .` before pushing.
│
└── NO → Is there an upstream branch (`@{u}` exists)?
    ├── YES → Regular push (Step 2)
    │
    └── NO  → First push — needs `-u origin <branch>` (Step 2)
```

```
What branch are we on?
├── main / master
│   → STOP. Do NOT push from main. This mirrors the same rule
│     `/git-commit` applies: main is for receiving merges, not for
│     hand-pushed commits.
│   → Propose moving the local commits to a feature branch and
│     pushing the branch instead. See the "Recovery" section below
│     for the exact commands.
│   → Do not execute the migration without confirmation.
│
├── feature branch (typical case)
│   → Continue.
│
└── Detached HEAD
   → STOP. Don't guess. Surface to the human.
```

### Step 1 — Show what will be pushed

If `git log @{u}..HEAD` returned anything (or this is the first push), show the human the commit list and ask for confirmation **before** running `git push`:

```
I'm about to push 3 commits to origin/feat/notes-service-layer:

  abc1234 feat(note): add createNote service + tests
  def5678 chore(deps): bump zod to 3.24.1
  ghi9012 docs(readme): document the notes service layer

Push?
```

Skip the confirmation if the human said "push it" / "auto-push" / similar.

If `git log @{u}..HEAD` is empty AND an upstream exists → nothing to push, say so and exit. Don't run `git push` for no reason.

### Step 2 — Run verify (gate before publishing)

Even if `/git-commit` already verified, the local commits may have come from a different tool (IDE, another terminal) that skipped verify. Running it again before push is cheap insurance.

```sh
pnpm run typecheck     # vue-tsc
pnpm run lint          # eslint
pnpm run test          # vitest
```

If any fails — **do not push**. Tell the human and stop. Pushing broken code wastes everyone's CI time and creates revert pressure. (CI runs the same `verify` job on the PR, so a local failure will fail CI too.)

### Step 3 — Pick the push command

| Situation | Command |
|---|---|
| First push of this branch (no upstream) | `git push -u origin <branch>` |
| Subsequent push of this branch | `git push` |
| Pushing a branch that was rebased / amended | `git push --force-with-lease` (NEVER plain `--force`) |
| Pushing to `main` / `master` | only after explicit "yes, push main" from human; never `--force` |

**The `-u` flag matters on first push** — it sets the upstream tracking branch so subsequent `git pull` / `git status` know what to compare against. Forgetting it leads to "no tracking information" confusion later.

### Step 4 — Execute the push

```sh
git push -u origin <branch>     # first push
# OR
git push                         # subsequent push
```

Show the human the output. Look for:
- `Branch 'X' set up to track 'origin/X'` — first push succeeded
- `* [new branch]` — created remotely
- `<old>..<new>` — fast-forward succeeded
- `! [rejected]` — non-fast-forward, see Step 5

### Step 5 — Handle rejection (non-fast-forward)

If push is rejected with `non-fast-forward`:

```
To origin
 ! [rejected]        feat/foo -> feat/foo (non-fast-forward)
error: failed to push some refs to 'origin'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart.
```

**Do NOT run `git push --force`**. Instead:

1. Tell the human the push was rejected.
2. Suggest: `git fetch && git log HEAD..@{u} --oneline` to see what's on the remote that you don't have.
3. Two safe paths:
   - **`git pull --rebase`** if your local commits should sit on top of the remote ones (typical).
   - **`git merge @{u}`** if you want a merge commit (rare for feature branches).
4. After resolving, push again with regular `git push` (no force needed if rebase / merge cleanly applied).

Only if the human explicitly says "force push" / "I rewrote history and want to overwrite" — use `git push --force-with-lease` (refuses to push if the remote moved since your last fetch — safer than `--force`).

### Step 6 — Report and surface next-step options

After a successful push:

```
✅ Pushed 3 commits to origin/feat/notes-service-layer

Next options (none executed without your say-so):
  1. Open a PR        gh pr create --base main --head feat/notes-service-layer
  2. View on GitHub   gh browse
  3. Watch CI         gh run watch
  4. Continue work    (stay on this branch, more commits to come)
  5. Switch to main   git switch main && git pull --ff-only
```

**Do NOT** auto-create PRs, auto-merge, or auto-switch branches. Surface, wait.

If you've been asked to also open a PR ("push and open a PR"), proceed with `gh pr create` and use the recent commit messages as the PR body draft (see [branches.md](../git-commit/branches.md) for PR conventions).

## Force-push safety

**Hard rule**: never `git push --force` without explicit human confirmation, and never to `main` / `master` under any circumstance.

When force-push is legitimate:
- You rebased your feature branch to clean up history before review
- You amended a commit and the remote already has the old version
- You squashed multiple WIP commits

When force-push is **not** legitimate:
- The branch is shared (others have pulled it) — coordinate first or use a new branch
- It's `main` / `master` — never
- You're not sure why a regular push was rejected — `git fetch` first to see what's there

When you do force-push, **use `--force-with-lease`**, never plain `--force`:

```sh
git push --force-with-lease
```

`--force-with-lease` refuses if the remote moved since your last fetch — protects against overwriting commits a teammate pushed in the meantime. `--force` would happily destroy them.

## Pushing to `main` / `master`

**Don't.** Same rule `/git-commit` enforces — main is for receiving merges from PRs, not for hand-pushed commits. The skill blocks this path; there is no "if the human really insists" escape.

The correct workflow is always:

```
feature branch → /git-push → gh pr create → review → squash-merge on GitHub
```

See [branches.md](../git-commit/branches.md) for naming conventions, PR title/body shape, and the merge / rebase / squash decision.

If you already have local commits on `main` that should have been on a branch, use the "Recovery" workflow below to move them off — don't paper over it by pushing main.

## Recovery: I'm on `main` with commits to push

When `/git-push` stops you on main because there are unpushed commits, move them to a feature branch and push the branch:

```sh
# 1. Create a new branch at the current HEAD — this carries all the
#    unpushed commits over to the branch without losing them.
git switch -c <type>/<short-descriptive-name>

# 2. (Optional but recommended) Reset main back to the remote so it
#    mirrors origin/main again. This is safe because the commits are
#    already preserved on the new branch you just created.
git switch main
git reset --hard origin/main

# 3. Switch back to the new branch and push it.
git switch <type>/<short-descriptive-name>
git push -u origin <type>/<short-descriptive-name>
```

After this:
- The feature branch is on origin, ready for a PR
- Local `main` matches `origin/main` again
- No commits are lost; the same SHAs that were on main now live on the feature branch

**Surface these commands to the human before running them.** The `reset --hard` on main, while safe given Step 1, deserves a confirmation gate. Never run it automatically.

Naming the branch: see [branches.md](../git-commit/branches.md). Default pattern `<type>/<short-descriptive-name>` matching the commits' Conventional Commits type (e.g., `feat/notes-service-layer`, `fix/note-visibility-filter`, `docs/git-push-skill`).

## When commits were already pushed and you regret them

Once pushed, you can't un-publish. Options:

- **`git revert <sha>`** — create a new commit that undoes the changes; preserves history; safe on shared branches. **Preferred.**
- **`git reset --hard <prev> && git push --force-with-lease`** — rewrite history; only acceptable if the branch is yours and no one else pulled it; never on `main`.

Tell the human which one fits their situation. Don't choose for them.

## Hard rules

- ❌ **Never push from `main` / `master`** — propose the Recovery workflow above and wait for confirmation. No escape, no "if you really want to". Same rule `/git-commit` enforces on its end.
- ❌ **Never `git push --force`** — always `--force-with-lease` when force is justified
- ❌ **Never force-push to `main` / `master`** — even with `--force-with-lease`
- ❌ **Never push when `pnpm run typecheck` / `test` is failing** — fix first
- ❌ **Never auto-push** after committing — `/git-commit` ends on the branch; pushing is a separate, explicit step
- ❌ **Never auto-create PRs** after pushing — surface the option, wait for the human
- ❌ **Never `git push --no-verify`** to skip pre-push hooks — unless the human explicitly asks
- ❌ **Never push a dirty working tree's uncommitted changes** — only committed commits are pushable; surface the dirt and ask first

## Examples

### Typical first push of a feature branch

```
$ git status --short --branch
## feat/notes-service-layer...origin/feat/notes-service-layer [gone]
$ git log @{u}..HEAD --oneline
fatal: no upstream configured for branch 'feat/notes-service-layer'
```

→ No upstream. Show commits since branching from main, run verify, push with `-u`:

```sh
pnpm run typecheck && pnpm run lint && pnpm run test
git push -u origin feat/notes-service-layer
```

### Subsequent push (upstream exists)

```
$ git status --short --branch
## feat/notes-service-layer...origin/feat/notes-service-layer [ahead 2]
```

→ Show the 2 commits, run verify, push:

```sh
git log @{u}..HEAD --oneline
pnpm run typecheck && pnpm run lint && pnpm run test
git push
```

### Push rejected (someone else pushed)

```
$ git push
 ! [rejected]   feat/foo -> feat/foo (non-fast-forward)
```

→ Don't `--force`. Pull first:

```sh
git fetch
git log HEAD..@{u} --oneline    # see what's on the remote
git pull --rebase                # replay your commits on top
git push                         # now fast-forward, should succeed
```

### Force push after rebase (legitimate)

The human said "I rebased to clean up history, push the new version":

```sh
git push --force-with-lease
```

Never plain `--force`. If `--force-with-lease` is rejected, the remote moved — fetch + investigate.

## Composition with other skills

```
/git-commit              ← stage, message, verify, local commit
   ↓
/git-push                ← what this skill does — push to remote
   ↓
(optional) gh pr create  ← open PR (mention to human; don't auto-run)
   ↓
/git-sync                ← after PR is merged: pull main, prune branches
```

See [branches.md](../git-commit/branches.md) for branch naming, PR title/body conventions, and merge / rebase / squash decisions.
