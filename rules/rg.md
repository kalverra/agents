### rg

Prefer `rg` over `grep`. Always pass an explicit path argument (e.g. `rg "pattern" .`); without one, non-interactive subshells hang waiting on stdin.
