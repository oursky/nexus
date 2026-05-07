#!/usr/bin/env bash
# Create minimal host config fixtures for E2E test host config drive injection.
# These files are gathered by buildHostConfigDirLibkrun() and injected into VMs.
set -euo pipefail

HOME="${HOME:-$(eval echo ~)}"

# .gitconfig
if [ ! -f "$HOME/.gitconfig" ]; then
  cat > "$HOME/.gitconfig" <<'EOF'
[user]
	name = Nexus CI
	email = ci@nexus.dev
EOF
fi

# .ssh directory + known_hosts
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
if [ ! -f "$HOME/.ssh/known_hosts" ]; then
  cat > "$HOME/.ssh/known_hosts" <<'EOF'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
EOF
fi

# .ssh/config
if [ ! -f "$HOME/.ssh/config" ]; then
  cat > "$HOME/.ssh/config" <<'EOF'
Host github.com
	Hostname github.com
	User git
	IdentityFile ~/.ssh/id_ed25519
EOF
  chmod 600 "$HOME/.ssh/config"
fi

# API key env file for .nexus-env injection testing.
# Set OPENAI_API_KEY so the test can verify env injection works end-to-end.
mkdir -p "$HOME/.config/nexus"
if [ ! -f "$HOME/.config/nexus/api-keys.env" ]; then
  cat > "$HOME/.config/nexus/api-keys.env" <<'EOF'
OPENAI_API_KEY=e2e-test-sentinel-key
EOF
fi

echo "setup-host-config-fixtures: created fixture files in $HOME"
ls -la "$HOME/.gitconfig" "$HOME/.ssh/known_hosts" "$HOME/.ssh/config" "$HOME/.config/nexus/api-keys.env"
