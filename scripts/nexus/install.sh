#!/usr/bin/env bash
set -euo pipefail

target_dir="."
source_dir=""
update_mode="false"
force_mode="false"
symlink_mode="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) target_dir="$2"; shift 2 ;;
    --source-dir) source_dir="$2"; shift 2 ;;
    --update) update_mode="true"; shift ;;
    --force) force_mode="true"; shift ;;
    --symlink) symlink_mode="true"; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "$source_dir" ]]; then
  source_dir="$(cd "$script_dir/../../skills/nexus" && pwd)"
fi

if [[ ! -f "$source_dir/VERSION" ]]; then
  echo "Missing VERSION in source skill suite: $source_dir/VERSION" >&2
  exit 1
fi

suite_version="$(<"$source_dir/VERSION")"
target_dir="$(cd "$target_dir" && pwd)"
install_root="$target_dir/.agents/skills/nexus"
version_file="$install_root/.version"

mkdir -p "$target_dir/.agents/skills"

if [[ "$symlink_mode" == "true" ]]; then
  rm -rf "$install_root"
  ln -s "$source_dir" "$install_root"
  echo "Installed Nexus skill suite symlink to $source_dir"
  echo "Installed path: $install_root"
  echo "Entry skill: .agents/skills/nexus/handoff/SKILL.md"
  exit 0
fi

if [[ -f "$version_file" ]]; then
  installed_version="$(<"$version_file")"
else
  installed_version=""
fi

if [[ -d "$install_root" && "$update_mode" != "true" && "$force_mode" != "true" ]]; then
  echo "Already installed at $install_root (version: ${installed_version:-unknown})."
  echo "Run with --update to refresh."
  exit 0
fi

if [[ -d "$install_root" && "$force_mode" == "true" ]]; then
  rm -rf "$install_root"
fi

mkdir -p "$install_root"
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete "$source_dir/" "$install_root/"
else
  rm -rf "$install_root"
  mkdir -p "$install_root"
  cp -R "$source_dir/." "$install_root/"
fi

echo "$suite_version" > "$version_file"

if [[ -n "$installed_version" && "$update_mode" == "true" && "$installed_version" != "$suite_version" ]]; then
  echo "Updated Nexus skill suite: $installed_version -> $suite_version"
elif [[ -n "$installed_version" && "$update_mode" == "true" ]]; then
  echo "Nexus skill suite is already up to date (version $suite_version)"
else
  echo "Installed Nexus skill suite version $suite_version"
fi

echo "Installed path: $install_root"
echo "Entry skill: .agents/skills/nexus/handoff/SKILL.md"
