#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <path-to.xcresult>" >&2
  exit 1
fi

RESULT_BUNDLE="$1"

if [[ ! -e "$RESULT_BUNDLE" ]]; then
  echo "Error: '$RESULT_BUNDLE' does not exist" >&2
  exit 1
fi

REPORT_DIR="packages/nexus-swift/.build/xcresults"
mkdir -p "$REPORT_DIR"
REPORT_PATH="$REPORT_DIR/latest-report.md"

# Step 1: Get the top-level record, extract the testsRef ID
TOP_JSON=$(xcrun xcresulttool get --legacy --path "$RESULT_BUNDLE" --format json)
TESTS_REF_ID=$(echo "$TOP_JSON" | python3 -c "
import sys, json
d = json.load(sys.stdin)
actions = d.get('actions', {}).get('_values', [])
if actions:
    ref = actions[0].get('actionResult', {}).get('testsRef', {}).get('id', {}).get('_value', '')
    print(ref)
")

# Step 2: Fetch the actual test summaries using the testsRef ID
if [[ -z "$TESTS_REF_ID" ]]; then
  echo "Warning: could not extract testsRef from xcresult bundle" >&2
  JSON="{}"
else
  JSON=$(xcrun xcresulttool get --legacy --path "$RESULT_BUNDLE" --format json --id "$TESTS_REF_ID")
fi

REPORT=$(python3 -c "
import sys, json, datetime

data = json.loads(sys.argv[1])
result_bundle = sys.argv[2]
run_time = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')

rows = []
passed = 0
failed = 0
skipped = 0
total_duration = 0.0

def collect_tests(node):
    \"\"\"Walk the xcresult JSON tree, collecting leaf test results.\"\"\"
    global passed, failed, skipped, total_duration
    if not isinstance(node, dict):
        return
    type_name = node.get('_type', {}).get('_name', '')

    # Leaf: a test case with testStatus
    if 'testStatus' in node and type_name not in ('ActionTestPlanRunSummaries', 'ActionTestPlanRunSummary', 'ActionTestableSummary', 'ActionTestSummaryGroup'):
        name = node.get('name', {}).get('_value', node.get('identifier', {}).get('_value', 'Unknown'))
        status_raw = node.get('testStatus', {}).get('_value', 'Unknown')
        dur = float(node.get('duration', {}).get('_value', 0))
        total_duration += dur
        if status_raw == 'Success':
            icon = '✅ Pass'
            passed += 1
        elif status_raw == 'Failure':
            icon = '❌ Fail'
            failed += 1
        elif status_raw == 'Skipped':
            icon = '⏭ Skip'
            skipped += 1
        else:
            icon = status_raw
        rows.append((name, icon, dur))
        return

    # Recurse into subtests if present
    subtests = node.get('subtests', {}).get('_values', [])
    for st in subtests:
        collect_tests(st)

    # Also recurse into summaries → testableSummaries → tests
    for key in ('summaries', 'testableSummaries', 'tests'):
        val = node.get(key, {})
        items = val.get('_values', []) if isinstance(val, dict) else (val if isinstance(val, list) else [])
        for item in items:
            collect_tests(item)

collect_tests(data)

lines = [
    '# NexusUITests Report',
    '',
    f'**Run:** {run_time}  ',
    f'**Result bundle:** {result_bundle}  ',
    f'**Open:** \`open {result_bundle}\`',
    '',
    '| Test | Status | Duration |',
    '|------|--------|----------|',
]
for name, icon, dur in rows:
    lines.append(f'| {name} | {icon} | {dur:.1f}s |')
lines.append('')
lines.append(f'**Total: {passed} passed, {failed} failed, {skipped} skipped — {total_duration:.1f}s**')
print('\n'.join(lines))
" "$JSON" "$RESULT_BUNDLE")

echo "$REPORT"
echo "$REPORT" > "$REPORT_PATH"

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "$REPORT" >> "$GITHUB_STEP_SUMMARY"
fi
