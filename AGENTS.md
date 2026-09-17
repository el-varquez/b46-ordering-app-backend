# Repository Workflow

## Implementation and commits

Complete and verify the full requested implementation before preparing commits.
Leave all completed changes uncommitted until the user explicitly queues an
atomic commit. When queued, split the completed implementation into logical,
stage-aligned atomic commits with Conventional Commit subjects. Each commit
should be independently understandable and buildable where practical; do not
commit partial work merely because a stage is still in progress. Report every
resulting commit.

The user owns remote delivery. Leave every commit local for the user to push,
open as a pull request, and merge through the repository ruleset.
