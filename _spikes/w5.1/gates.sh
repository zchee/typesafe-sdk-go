#!/bin/sh
# Checks each commit given alone, in a detached worktree of the repository
# this script runs in: go build, go vet, the section 11 lint chain (govulncheck
# aside, which needs the network and does not depend on the commit) and
# go test -race -count=1 ./..., under GOEXPERIMENT=nosimd,noruntimesecret on
# (M). Prints one start and one PASS or FAIL line per commit, each with its
# date; exits non-zero when any commit fails. results/gates-M.txt is its output
# for W5.1's commits. golangci-lint waits for another lane's run to end
# (--allow-serial-runners) instead of failing on its lock.
#
# Usage: gates.sh WORKDIR SHA...
#   WORKDIR  a directory for the temporary worktree (a scratchpad, never /tmp).
set -u
work=$1
shift
repo=$(git rev-parse --show-toplevel) || exit 2
wt=$work/wt-gates
export GOEXPERIMENT=nosimd,noruntimesecret
rc=0
for sha in "$@"; do
	git -C "$repo" worktree remove --force "$wt" 2>/dev/null
	git -C "$repo" worktree add -q --detach "$wt" "$sha" || exit 2
	echo "== $sha $(git -C "$wt" log --format=%s -1) start $(date '+%Y-%m-%d %H:%M:%S %Z')"
	if (cd "$wt" && go build ./... && go vet ./... && test -z "$(gofumpt -extra -l .)" &&
		modernize -test ./... 2>/dev/null && golangci-lint run --allow-serial-runners ./... && staticcheck ./... &&
		go mod tidy -diff && go test -race -count=1 ./...); then
		echo "== $sha PASS $(date '+%Y-%m-%d %H:%M:%S %Z')"
	else
		echo "== $sha FAIL $(date '+%Y-%m-%d %H:%M:%S %Z')"
		rc=1
	fi
done
git -C "$repo" worktree remove --force "$wt"
exit $rc
