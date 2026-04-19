#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

task build:workspace-daemon
task lint:workspace-daemon
task test:workspace-daemon
