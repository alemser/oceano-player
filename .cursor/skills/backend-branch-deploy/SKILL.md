---
name: backend-branch-deploy
description: Manages the oceano-player backend delivery flow: create a branch from main for new scoped work, commit related changes, push and validate on Raspberry Pi with ./install.sh --branch <branch>, then merge to main and run ./install.sh. Use when the user starts a new backend scope, asks to prepare a backend branch, or requests backend deploy/validation steps.
---

# Backend Branch Deploy Flow

Use this workflow for `oceano-player` backend changes.

## Standard Routine

1. Start from `main` and create a branch for the new scope.
2. Implement the scope and make related commits.
3. Push the branch.
4. Validate on Raspberry Pi using:
   - `./install.sh --branch <branch>`
5. If tests are good, merge to `main`.
6. Deploy `main` on Raspberry Pi using:
   - `./install.sh`

## Command Sequence

```bash
# 1) Start a new scoped branch from main
git checkout main
git pull --ff-only
git checkout -b <scope-branch>

# 2) Work and commit related changes
git add <files>
git commit -m "<message>"

# 3) Push branch
git push -u origin <scope-branch>

# 4) Validate branch on Raspberry Pi
./install.sh --branch <scope-branch>

# 5) Merge to main after successful validation
git checkout main
git pull --ff-only
git merge --no-ff <scope-branch>
git push origin main

# 6) Deploy main on Raspberry Pi
./install.sh
```

## Execution Notes

- Keep commits grouped by related scope.
- Re-run `./install.sh --branch <scope-branch>` after fixes before merging.
- Do not skip backend validation on Pi before main merge.
- If branch validation fails, fix on branch, commit, push, and test again.
