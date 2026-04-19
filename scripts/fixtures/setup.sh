#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPOS_DIR="$SCRIPT_DIR/repos"

mkdir -p "$REPOS_DIR"

clone_or_pull() {
  local url="$1"
  local dest="$2"
  if [ -d "$dest/.git" ]; then
    echo "Updating $dest..."
    git -C "$dest" pull
  else
    echo "Cloning $url -> $dest..."
    git clone "$url" "$dest"
  fi
}

clone_or_pull "https://github.com/IniZio/nexus-fixture-plain-git.git" "$REPOS_DIR/plain-git"
clone_or_pull "https://github.com/IniZio/nexus-fixture-vite-spa.git" "$REPOS_DIR/vite-spa"
clone_or_pull "https://github.com/IniZio/nexus-fixture-compose-app.git" "$REPOS_DIR/compose-app"
clone_or_pull "https://github.com/IniZio/nexus-fixture-run-script.git" "$REPOS_DIR/run-script"

cat > "$SCRIPT_DIR/.env" <<EOF
NEXUS_FIXTURE_PLAIN_GIT_PATH=$REPOS_DIR/plain-git
NEXUS_FIXTURE_VITE_SPA_PATH=$REPOS_DIR/vite-spa
NEXUS_FIXTURE_COMPOSE_APP_PATH=$REPOS_DIR/compose-app
NEXUS_FIXTURE_RUN_SCRIPT_PATH=$REPOS_DIR/run-script
EOF

echo "✓ Fixtures ready at scripts/fixtures/repos/"
