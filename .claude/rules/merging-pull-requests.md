# Merging pull requests

Project-wide rule for how a pull request lands on `main`. It applies to every merge, including a merge an agent performs with `gh pr merge` and a release prep PR.

## Top-level principle

A pull request lands with a merge commit, so the branch's commits stay in the history of `main` as they were reviewed and tested. Squash-merging is the exception and needs a stated reason.

## Default

- Merge with `gh pr merge <number> --merge --match-head-commit <sha>`, where `<sha>` is the head commit the checks passed on. Pinning the head commit refuses the merge when the branch moved after CI ran.
- Do not pass `--squash` by default. A squash collapses the branch into one commit and discards the per-commit messages, the per-step history, and the ability to bisect or revert one step of a multi-commit change.
- Rebase-merging (`--rebase`) rewrites the branch's commits onto `main` with new hashes. Use it only when the user asks for it.

## When a squash is justified

Squash only when the branch's individual commits carry no information worth keeping, and state the reason in the conversation or the PR before merging. The following cases qualify:

- The branch consists of fix-up, work-in-progress, or review-response commits whose messages do not describe a self-contained change.
- The user asks for a squash.

A branch with many commits is not on its own a reason to squash when each commit is a self-contained, described step.

## Where this rule applies

- Every pull request merged into `main` or a `release/*` branch.
- Agents, workflows, and skills that merge pull requests.
