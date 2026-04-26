#!/usr/bin/env bash
set -euo pipefail

if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
  echo "/dev/kvm already accessible — skipping udev rule"
elif [ -d /etc/udev/rules.d ]; then
  echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' \
    | sudo tee /etc/udev/rules.d/99-kvm4all.rules >/dev/null
  sudo udevadm control --reload-rules
  sudo udevadm trigger --name-match=kvm
else
  sudo chmod 0666 /dev/kvm 2>/dev/null || true
fi
ls -la /dev/kvm
