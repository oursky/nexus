package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// ── project list ──────────────────────────────────────────────────────────────

func runProjectListCommand(args []string) {
	fs := flag.NewFlagSet("project list", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project list: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Projects []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			RepoURL string `json:"repoUrl"`
		} `json:"projects"`
	}
	if err := daemonRPC(conn, "project.list", map[string]any{}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus project list: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(result.Projects)
		return
	}

	if len(result.Projects) == 0 {
		fmt.Println("no projects")
		return
	}
	fmt.Printf("%-20s  %-20s  %s\n", "ID", "NAME", "REPO")
	fmt.Printf("%-20s  %-20s  %s\n", "--------------------", "--------------------", "----")
	for _, p := range result.Projects {
		fmt.Printf("%-20s  %-20s  %s\n", p.ID, p.Name, p.RepoURL)
	}
}

// ── project create ────────────────────────────────────────────────────────────

func runProjectCreateCommand(args []string) {
	fs := flag.NewFlagSet("project create", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "project name (required)")
	repoURL := fs.String("repo", "", "repository URL")
	_ = fs.Parse(args)

	if *name == "" {
		fmt.Fprintf(os.Stderr, "nexus project create: --name is required\n")
		fs.Usage()
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project create: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Project struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			RepoURL string `json:"repoUrl"`
		} `json:"project"`
	}
	if err := daemonRPC(conn, "project.create", map[string]any{
		"name":    *name,
		"repoUrl": *repoURL,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus project create: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("created project %s (%s)\n", result.Project.Name, result.Project.ID)
}

// ── project get ───────────────────────────────────────────────────────────────

func runProjectGetCommand(args []string) {
	fs := flag.NewFlagSet("project get", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus project get <id|name> [--json]")
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project get: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	id, err := resolveProjectID(context.Background(), conn, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project get: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		Project struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			RepoURL string `json:"repoUrl"`
		} `json:"project"`
	}
	if err := daemonRPC(conn, "project.get", map[string]any{"id": id}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus project get: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(result.Project)
		return
	}

	p := result.Project
	fmt.Printf("id:      %s\n", p.ID)
	fmt.Printf("name:    %s\n", p.Name)
	fmt.Printf("repoUrl: %s\n", p.RepoURL)
}

// ── project remove ────────────────────────────────────────────────────────────

func runProjectRemoveCommand(args []string) {
	fs := flag.NewFlagSet("project remove", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "skip confirmation prompt")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus project remove <id|name> [--force]")
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project remove: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	id, err := resolveProjectID(context.Background(), conn, rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus project remove: %v\n", err)
		os.Exit(1)
	}

	if !*force {
		fi, _ := os.Stdin.Stat()
		isTTY := (fi.Mode() & os.ModeCharDevice) != 0
		if isTTY {
			if !confirmPrompt(fmt.Sprintf("remove project %s?", id)) {
				fmt.Println("aborted")
				return
			}
		}
	}

	if err := daemonRPC(conn, "project.remove", map[string]any{"id": id}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus project remove: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("removed project %s\n", id)
}

// ── top-level project dispatcher ──────────────────────────────────────────────

func runProjectCommand(args []string) {
	if len(args) == 0 {
		printProjectUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		runProjectListCommand(rest)
	case "create":
		runProjectCreateCommand(rest)
	case "get":
		runProjectGetCommand(rest)
	case "remove", "rm", "delete":
		runProjectRemoveCommand(rest)
	default:
		printProjectUsage()
		fmt.Fprintf(os.Stderr, "\nunknown project subcommand: %s\n", sub)
		os.Exit(2)
	}
}

func printProjectUsage() {
	fmt.Fprint(os.Stderr, `usage: nexus project <subcommand> [options]

subcommands:
  list [--json]                          list all projects
  create --name <name> [--repo <url>]    create a project
  get <id|name> [--json]                 show project details
  remove <id|name> [--force]             remove a project

`)
}
