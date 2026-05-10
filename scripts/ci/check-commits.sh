#!/usr/bin/env bash
# Validate commit messages follow Conventional Commits or Lore intent-line format.
# Mirrors the CI commit-policy workflow (.github/workflows/commit-policy.yml).
#
# Usage:
#   ./check-commits.sh [BASE_SHA] [HEAD_SHA]
#   BASE_SHA=abc123 HEAD_SHA=def456 ./check-commits.sh
#
# Defaults to origin/main..HEAD (all commits on current branch not in main).

set -euo pipefail

# ── Resolve commit range ──────────────────────────────────────────────────────

BASE_SHA="${1:-${BASE_SHA:-}}"
HEAD_SHA="${2:-${HEAD_SHA:-HEAD}}"

if [ -z "$BASE_SHA" ]; then
  REMOTE="origin"
  BRANCH="main"

  # Ensure we have a remote reference to compare against
  if ! git remote get-url "$REMOTE" &>/dev/null; then
    echo "ERROR: No git remote '$REMOTE' found. Set BASE_SHA explicitly." >&2
    exit 1
  fi

  # Fetch the remote branch if the local ref doesn't exist yet
  if ! git rev-parse --verify "$REMOTE/$BRANCH" &>/dev/null; then
    echo "Fetching $REMOTE/$BRANCH..."
    git fetch "$REMOTE" "$BRANCH" --quiet 2>/dev/null || {
      echo "ERROR: Failed to fetch $REMOTE/$BRANCH. Set BASE_SHA explicitly." >&2
      exit 1
    }
  fi

  BASE_SHA="$REMOTE/$BRANCH"
fi

# Verify the range is valid
if ! git rev-parse --verify "$BASE_SHA" &>/dev/null; then
  echo "ERROR: BASE_SHA '$BASE_SHA' is not a valid git ref." >&2
  exit 1
fi

if ! git rev-parse --verify "$HEAD_SHA" &>/dev/null; then
  echo "ERROR: HEAD_SHA '$HEAD_SHA' is not a valid git ref." >&2
  exit 1
fi

# ── Validation patterns (mirrors CI) ──────────────────────────────────────────

TYPES="feat|fix|docs|chore|refactor|test|ci|build|perf|revert"
CONVENTIONAL_PATTERN="^(${TYPES})(\(.+\))?(!)?: .+"
LORE_INTENT_PATTERN="^[A-Z][A-Za-z0-9 ,._()'/-]{12,}$"

# ── Check commits ─────────────────────────────────────────────────────────────

ERRORS=0
TOTAL=0
FAILED_MSGS=()

while IFS= read -r msg; do
  TOTAL=$((TOTAL + 1))
  if ! echo "$msg" | grep -qP "$CONVENTIONAL_PATTERN" && ! echo "$msg" | grep -qP "$LORE_INTENT_PATTERN"; then
    FAILED_MSGS+=("$msg")
    echo "FAIL: $msg"
    ERRORS=$((ERRORS + 1))
  fi
done < <(git log --format="%s" "$BASE_SHA".."$HEAD_SHA")

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo "Range: $BASE_SHA..$HEAD_SHA ($TOTAL commits)"

if [ "$ERRORS" -gt 0 ]; then
  echo ""
  echo "Commit messages must follow either Conventional Commits or Lore intent-line format:"
  echo "  Conventional: <type>(<scope>): <description>"
  echo "  Lore intent:  Sentence-style intent line (capitalized, 13+ chars)"
  echo "  Allowed types: ${TYPES//|/, }"
  echo ""
  echo "Failed $ERRORS/$TOTAL commit messages."
  exit 1
fi

echo "All $TOTAL commit messages are valid."
