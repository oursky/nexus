package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	idPattern   = regexp.MustCompile(`\b(?:DAEMON|AUTH|PRJ|PTY|SPOT|WS|CLI|ERR|INV|RPC|VM-PROOF|VM|MACAPP-PROOF|MACAPP)-\d{3,}\b`)
	testPattern = regexp.MustCompile(`^func\s+(Test[^(\s]+)\s*\(`)
)

type testRef struct {
	File string
	Name string
	Line int
}

type waiver struct {
	Reason string
}

func main() {
	var (
		specDir    = flag.String("spec-dir", "../../docs", "path to spec directory")
		outputPath = flag.String("out", "test/e2e/coverage/coverage-map.md", "output markdown path")
		waiverPath = flag.String("waivers", "test/e2e/coverage/waivers.txt", "waiver definitions")
		checkOnly  = flag.Bool("check", false, "fail on missing unwaived spec obligations")
	)
	flag.Parse()

	specIDs, err := parseSpecIDs(*specDir)
	if err != nil {
		fatalf("parse spec ids: %v", err)
	}
	if len(specIDs) == 0 {
		fatalf("no spec ids found in %s", *specDir)
	}

	testRoots := []string{"test/e2e", "internal"}
	coverage, unknownRefs, err := parseTestReferences(testRoots)
	if err != nil {
		fatalf("parse test references: %v", err)
	}

	specSet := make(map[string]struct{}, len(specIDs))
	for _, id := range specIDs {
		specSet[id] = struct{}{}
	}

	for _, id := range unknownRefs {
		if _, ok := specSet[id]; ok {
			continue
		}
		fatalf("unknown spec id referenced in tests: %s", id)
	}

	waivers, err := parseWaivers(*waiverPath)
	if err != nil {
		fatalf("parse waivers: %v", err)
	}

	report, missing := renderCoverageReport(specIDs, coverage, waivers, *specDir)
	if err := os.WriteFile(*outputPath, []byte(report), 0o644); err != nil {
		fatalf("write report: %v", err)
	}

	fmt.Printf("wrote %s (%d ids)\n", *outputPath, len(specIDs))
	if len(missing) > 0 {
		fmt.Printf("missing spec ids: %s\n", strings.Join(missing, ", "))
		if *checkOnly {
			os.Exit(1)
		}
	}
}

func parseSpecIDs(dir string) ([]string, error) {
	seen := map[string]struct{}{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, id := range idPattern.FindAllString(string(data), -1) {
			seen[id] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func parseTestReferences(roots []string) (map[string][]testRef, []string, error) {
	coverage := map[string][]testRef{}
	unknown := map[string]struct{}{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if strings.Contains(path, string(filepath.Separator)+"coverage") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			refs, unknownIDs, err := parseTestFile(path)
			if err != nil {
				return err
			}
			for id, list := range refs {
				coverage[id] = append(coverage[id], list...)
			}
			for _, id := range unknownIDs {
				unknown[id] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	unknownIDs := make([]string, 0, len(unknown))
	for id := range unknown {
		unknownIDs = append(unknownIDs, id)
	}
	sort.Strings(unknownIDs)
	for id := range coverage {
		sort.Slice(coverage[id], func(i, j int) bool {
			if coverage[id][i].File == coverage[id][j].File {
				return coverage[id][i].Line < coverage[id][j].Line
			}
			return coverage[id][i].File < coverage[id][j].File
		})
	}
	return coverage, unknownIDs, nil
}

func parseTestFile(path string) (map[string][]testRef, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	refs := map[string][]testRef{}
	unknownSet := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	pendingIDs := []string{}

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, "Spec:") {
			ids := idPattern.FindAllString(trimmed, -1)
			if len(ids) == 0 {
				continue
			}
			pendingIDs = append(pendingIDs, ids...)
			continue
		}

		m := testPattern.FindStringSubmatch(trimmed)
		if len(m) == 2 {
			if len(pendingIDs) > 0 {
				uniqueIDs := dedupeStrings(pendingIDs)
				for _, id := range uniqueIDs {
					refs[id] = append(refs[id], testRef{File: filepath.ToSlash(path), Name: m[1], Line: lineNo})
				}
				pendingIDs = nil
			}
			continue
		}

		if len(pendingIDs) > 0 && trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			for _, id := range pendingIDs {
				unknownSet[id] = struct{}{}
			}
			pendingIDs = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(pendingIDs) > 0 {
		return nil, nil, errors.New("dangling Spec: marker at end of file: " + path)
	}

	unknown := make([]string, 0, len(unknownSet))
	for id := range unknownSet {
		unknown = append(unknown, id)
	}
	sort.Strings(unknown)
	return refs, unknown, nil
}

func parseWaivers(path string) (map[string]waiver, error) {
	waivers := map[string]waiver{}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return waivers, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		id := strings.TrimSpace(parts[0])
		if !idPattern.MatchString(id) {
			return nil, fmt.Errorf("%s:%d invalid id %q", path, lineNo, id)
		}
		reason := ""
		if len(parts) == 2 {
			reason = strings.TrimSpace(parts[1])
		}
		waivers[id] = waiver{Reason: reason}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return waivers, nil
}

func renderCoverageReport(specIDs []string, coverage map[string][]testRef, waivers map[string]waiver, specDir string) (string, []string) {
	var b strings.Builder
	missing := []string{}

	b.WriteString("# Spec Coverage Map\n\n")
	b.WriteString("Source spec directory: `" + specDir + "`\n\n")
	b.WriteString("| Spec ID | Status | Test References | Notes |\n")
	b.WriteString("|---|---|---|---|\n")

	for _, id := range specIDs {
		refs := coverage[id]
		wv, waived := waivers[id]
		status := "covered"
		note := ""
		if len(refs) == 0 {
			if waived {
				status = "waived"
				note = escapeTable(wv.Reason)
			} else {
				status = "missing"
				missing = append(missing, id)
			}
		}

		refCol := "-"
		if len(refs) > 0 {
			items := make([]string, 0, len(refs))
			for _, ref := range refs {
				items = append(items, fmt.Sprintf("`%s` (%s:%d)", ref.Name, ref.File, ref.Line))
			}
			refCol = strings.Join(items, "<br>")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", id, status, refCol, note)
	}

	b.WriteString("\n")
	b.WriteString("Generated by `go run ./test/e2e/coverage --check`.\n")

	return b.String(), missing
}

func escapeTable(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spec-coverage: "+format+"\n", args...)
	os.Exit(1)
}
