#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <path-to.xcresult> [run-id]" >&2
  exit 1
fi

RESULT_BUNDLE="$1"
RUN_ID="${2:-${RUN_ID:-}}"

if [[ ! -e "$RESULT_BUNDLE" ]]; then
  echo "Error: '$RESULT_BUNDLE' does not exist" >&2
  exit 1
fi

if [[ -z "$RUN_ID" ]]; then
  TIMESTAMP=$(date +%Y%m%d-%H%M)
  GIT_SHA=$(git rev-parse --short=8 HEAD 2>/dev/null || echo "nosha")
  RUN_ID="${TIMESTAMP}-${GIT_SHA}"
fi

BUILD_DIR="packages/nexus-swift/.build/xcresults"
REPORT_DIR="${BUILD_DIR}/reports/${RUN_ID}"
SCREENSHOTS_DIR="${BUILD_DIR}/screenshots/${RUN_ID}"
INDEX_DIR="${BUILD_DIR}/index"

mkdir -p "$REPORT_DIR" "$SCREENSHOTS_DIR" "$INDEX_DIR"

# ── Step 1: Extract test summaries ───────────────────────────────────────────
TOP_JSON=$(xcrun xcresulttool get --legacy --path "$RESULT_BUNDLE" --format json)
TESTS_REF_ID=$(echo "$TOP_JSON" | python3 -c "
import sys, json
d = json.load(sys.stdin)
actions = d.get('actions', {}).get('_values', [])
if actions:
    ref = actions[0].get('actionResult', {}).get('testsRef', {}).get('id', {}).get('_value', '')
    print(ref)
")

# ── Step 2: Export attachments and build report data ─────────────────────────
python3 - "$RESULT_BUNDLE" "$RUN_ID" "$SCREENSHOTS_DIR" "$BUILD_DIR" "$TESTS_REF_ID" <<'PYEOF'
import sys, json, os, subprocess, datetime, re

result_bundle = sys.argv[1]
run_id = sys.argv[2]
screenshots_dir = sys.argv[3]
build_dir = sys.argv[4]
tests_ref_id = sys.argv[5]

def xcresult_get(bundle, ref_id=None):
    """Fetch a JSON object from an xcresult bundle by ref ID (or root if no ID)."""
    cmd = ['xcrun', 'xcresulttool', 'get', '--legacy', '--format', 'json', '--path', bundle]
    if ref_id:
        cmd += ['--id', ref_id]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0 or not result.stdout.strip():
        return {}
    try:
        return json.loads(result.stdout)
    except Exception:
        return {}

data = xcresult_get(result_bundle, tests_ref_id) if tests_ref_id else {}
run_time = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')

passed = 0
failed = 0
skipped = 0
total_duration = 0.0
test_records = []

def safe_filename(name):
    return re.sub(r'[^\w\-]', '_', name)

def export_attachment(bundle, attach_id, dest_path):
    """Export a single attachment by its ref ID."""
    try:
        os.makedirs(os.path.dirname(dest_path), exist_ok=True)
        result = subprocess.run(
            ['xcrun', 'xcresulttool', 'export', '--legacy',
             '--type', 'file',
             '--path', bundle,
             '--id', attach_id,
             '--output-path', dest_path],
            capture_output=True, text=True
        )
        if result.returncode != 0:
            print(f"  Warning: export failed for {attach_id}: {result.stderr.strip()}", file=sys.stderr)
            return False
        return True
    except Exception as e:
        print(f"  Warning: export exception for {attach_id}: {e}", file=sys.stderr)
        return False

def collect_activity_attachments(activities):
    """Recursively collect (name, payloadRef_id) from activitySummaries."""
    attachments = []
    if not isinstance(activities, list):
        return attachments
    for act in activities:
        if not isinstance(act, dict):
            continue
        for att in act.get('attachments', {}).get('_values', []):
            name = att.get('name', {}).get('_value', 'screenshot')
            payload_ref = att.get('payloadRef', {}).get('id', {}).get('_value', '')
            if payload_ref:
                attachments.append((name, payload_ref))
        # Recurse into subactivities
        sub = act.get('subactivities', {}).get('_values', [])
        attachments.extend(collect_activity_attachments(sub))
    return attachments

def collect_tests(node):
    global passed, failed, skipped, total_duration
    if not isinstance(node, dict):
        return
    type_name = node.get('_type', {}).get('_name', '')

    if 'testStatus' in node and type_name not in (
        'ActionTestPlanRunSummaries', 'ActionTestPlanRunSummary',
        'ActionTestableSummary', 'ActionTestSummaryGroup'
    ):
        name = node.get('name', {}).get('_value',
               node.get('identifier', {}).get('_value', 'Unknown'))
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

        # Export attachments for this test
        # Prefer inline activitySummaries; fall back to summaryRef fetch
        activities = node.get('activitySummaries', {}).get('_values', [])
        if not activities:
            summary_ref_id = node.get('summaryRef', {}).get('id', {}).get('_value', '')
            if summary_ref_id:
                summary_node = xcresult_get(result_bundle, summary_ref_id)
                activities = summary_node.get('activitySummaries', {}).get('_values', [])
        raw_attachments = collect_activity_attachments(activities)

        exported_screenshots = []
        test_dir = os.path.join(screenshots_dir, safe_filename(name))
        seen_names = {}
        for idx, (att_name, att_id) in enumerate(raw_attachments, start=1):
            base = safe_filename(att_name)
            # Deduplicate: if attachment name already carries a leading NN- prefix, use as-is;
            # otherwise prefix with index to guarantee uniqueness and sort order.
            if re.match(r'^\d{2}-', base):
                filename = f"{base}.png"
            else:
                filename = f"{idx:02d}-{base}.png"
            abs_dest = os.path.join(test_dir, filename)
            rel_dest = os.path.relpath(abs_dest, build_dir)
            if export_attachment(result_bundle, att_id, abs_dest):
                exported_screenshots.append({'name': att_name, 'path': rel_dest})

        test_records.append({
            'name': name,
            'status': status_raw,
            'statusIcon': icon,
            'durationSec': round(dur, 2),
            'screenshots': exported_screenshots,
        })
        return

    subtests = node.get('subtests', {}).get('_values', [])
    for st in subtests:
        collect_tests(st)
    for key in ('summaries', 'testableSummaries', 'tests'):
        val = node.get(key, {})
        items = val.get('_values', []) if isinstance(val, dict) else (val if isinstance(val, list) else [])
        for item in items:
            collect_tests(item)

collect_tests(data)

# ── Write index JSON ──────────────────────────────────────────────────────────
index_path = os.path.join(build_dir, 'index', f'{run_id}.json')
index_data = {
    'runId': run_id,
    'resultBundle': os.path.relpath(result_bundle, '.'),
    'reportPath': os.path.join('reports', run_id, 'report.md'),
    'summary': {
        'passed': passed,
        'failed': failed,
        'skipped': skipped,
        'durationSec': round(total_duration, 2),
    },
    'tests': test_records,
}
with open(index_path, 'w') as f:
    json.dump(index_data, f, indent=2)
print(f"Index: {index_path}")

# ── Write report markdown ─────────────────────────────────────────────────────
report_path = os.path.join(build_dir, 'reports', run_id, 'report.md')
lines = [
    '# UI test report',
    '',
    f'**Run ID:** `{run_id}`  ',
    f'**Time:** {run_time}  ',
    f'**Result bundle:** {result_bundle}  ',
    f'**Open:** `open {result_bundle}`',
    '',
    '| Test | Status | Duration | Screenshots |',
    '|------|--------|----------|-------------|',
]
for t in test_records:
    ss_links = ' '.join(
        f'[{s["name"]}]({os.path.join("../..", s["path"])})' for s in t['screenshots']
    )
    lines.append(f'| {t["name"]} | {t["statusIcon"]} | {t["durationSec"]:.1f}s | {ss_links} |')

lines.append('')
lines.append(f'**Total: {passed} passed, {failed} failed, {skipped} skipped — {round(total_duration, 2):.1f}s**')
report_content = '\n'.join(lines)
with open(report_path, 'w') as f:
    f.write(report_content)
print(f"Report: {report_path}")
print(report_content)
PYEOF

# ── Step 3: latest symlinks ───────────────────────────────────────────────────
LATEST_REPORT="${BUILD_DIR}/reports/latest"
LATEST_SCREENSHOTS="${BUILD_DIR}/screenshots/latest"

rm -f "$LATEST_REPORT"
ln -sf "$RUN_ID" "$LATEST_REPORT"

rm -f "$LATEST_SCREENSHOTS"
ln -sf "$RUN_ID" "$LATEST_SCREENSHOTS"
