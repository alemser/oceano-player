---
name: backend-branch-deploy-strict
description: Enforces a strict oceano-player backend release workflow with quality gates: branch from main, scoped commits, full test pass, branch deploy on Raspberry Pi with ./install.sh --branch <branch>, smoke checks, merge to main, final deploy with ./install.sh, and rollback readiness checks. Use for backend work that must stay merge-ready and production-safe.
---

# Backend Branch Deploy Flow (Strict)

Use this workflow for high-confidence backend delivery in `oceano-player`.

## Mandatory Gates

- Gate 1: branch is created from up-to-date `main`.
- Gate 2: local tests pass before branch push.
- Gate 3: branch deploy on Raspberry Pi succeeds.
- Gate 4: smoke checks pass on Raspberry Pi.
- Gate 5: merge to `main` and final deploy succeed.
- Gate 6: post-deploy health is verified.

## Strict Sequence

```bash
# 1) Create branch from latest main
git checkout main
git pull --ff-only
git checkout -b <scope-branch>

# 2) Implement scope and commit related changes
git add <files>
git commit -m "<message>"

# 3) Run local quality gate
go test ./...

# 4) Push branch
git push -u origin <scope-branch>

# 5) Deploy and test branch on Raspberry Pi
./install.sh --branch <scope-branch>

# 6) Merge only after successful Pi validation
git checkout main
git pull --ff-only
git merge --no-ff <scope-branch>
git push origin main

# 7) Final deploy from main
./install.sh
```

## Raspberry Pi Smoke Checks

Run after `./install.sh --branch <scope-branch>` and after final `./install.sh`.

```bash
# Services healthy
systemctl status oceano-source-detector oceano-state-manager oceano-web --no-pager

# State file updates and has sane source/state values
cat /tmp/oceano-state.json

# API basic health
curl -fsS http://localhost:8080/api/status
curl -fsS http://localhost:8080/api/config > /dev/null
```

## Merge Decision Rules

- Do not merge if any test or smoke check fails.
- If branch deploy fails, fix on branch, commit, push, redeploy branch, and retest.
- Keep commits scoped; unrelated work goes to another branch.

## Rollback Readiness

If production behavior regresses after main deploy:

1. Identify last known good commit on `main`.
2. Re-deploy that commit on Pi immediately.
3. Open a follow-up branch for root-cause fix.

Use fast rollback over prolonged live debugging.
