# Worktree Before Plan

When using the superpowers workflow, create the feature worktree **before** brainstorming and writing-plans — not after. Otherwise the spec and plan commits land on local `main`, diverge from `origin/main` after the PR's squash merge, and force a `reset --hard` every time.
