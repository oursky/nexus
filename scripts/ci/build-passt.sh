#!/usr/bin/env bash
set -euo pipefail

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

git clone --depth 1 https://passt.top/passt "$TMPDIR/passt"
cd "$TMPDIR/passt"

python3 -c '
with open("isolation.c", "r") as f:
    lines = f.readlines()

new_lines = []
i = 0
while i < len(lines):
    line = lines[i]
    if "if (unshare(flags)) {" in line:
        new_lines.append(line)
        new_lines.append("\t\tif (errno == EPERM) {\n")
        new_lines.append("\t\t\twarn(\"Skipping sandboxing: namespace creation blocked\");\n")
        new_lines.append("\t\t\treturn 0;\n")
        new_lines.append("\t\t}\n")
        i += 1
        continue
    if "prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog))" in line:
        new_lines.append(line.rstrip().rstrip(";") + " {\n")
        i += 1
        if i < len(lines) and "die_perror(\"Failed to apply seccomp filter\")" in lines[i]:
            new_lines.append("\t\tif (errno == EPERM) {\n")
            new_lines.append("\t\t\twarn(\"Skipping seccomp filter: not permitted\");\n")
            new_lines.append("\t\t\treturn;\n")
            new_lines.append("\t\t}\n")
            new_lines.append(lines[i])
            new_lines.append("\t}\n")
            i += 1
        continue
    new_lines.append(line)
    i += 1

with open("isolation.c", "w") as f:
    f.writelines(new_lines)
'

make clean
make CFLAGS="-static -DGLIBC_NO_STATIC_NSS" passt
chmod +x passt
cp passt /tmp/passt-patched
/tmp/passt-patched --help >/dev/null

echo "Built patched passt → /tmp/passt-patched"

if [ -n "${GITHUB_ENV:-}" ]; then
    echo "NEXUS_PASST_PATH=/tmp/passt-patched" >> "$GITHUB_ENV"
fi
