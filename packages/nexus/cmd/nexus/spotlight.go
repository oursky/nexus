package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
)

func runSpotlightCommand(args []string) {
	if len(args) == 0 {
		printSpotlightUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "start":
		runSpotlightStartCommand(rest)
	case "list", "ls":
		runSpotlightListCommand(rest)
	case "close":
		runSpotlightStopCommand(rest)
	case "port":
		runSpotlightPortCommand(rest)
	default:
		printSpotlightUsage()
		fmt.Fprintf(os.Stderr, "\nunknown spotlight subcommand: %s\n", sub)
		os.Exit(2)
	}
}

func runSpotlightStartCommand(args []string) {
	fs := flag.NewFlagSet("spotlight start", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	workspaceID := fs.String("workspace", "", "workspace ID (required)")
	localPort := fs.Int("local-port", 0, "local port to expose (required)")
	remotePort := fs.Int("remote-port", 0, "remote port (defaults to local-port)")
	protocol := fs.String("protocol", "", "protocol (tcp/http)")
	_ = fs.Parse(args)

	if *workspaceID == "" || *localPort == 0 {
		fmt.Fprintf(os.Stderr, "nexus spotlight start: --workspace and --local-port are required\n")
		fs.Usage()
		os.Exit(2)
	}
	if *remotePort == 0 {
		*remotePort = *localPort
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight start: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	spec := spotlight.ExposeSpec{
		WorkspaceID: *workspaceID,
		LocalPort:   *localPort,
		RemotePort:  *remotePort,
		Protocol:    *protocol,
	}
	var result struct {
		Forward *spotlight.Forward `json:"forward"`
	}
	if err := daemonRPC(conn, "spotlight.start", map[string]any{
		"workspaceId": *workspaceID,
		"spec":        spec,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight start: %v\n", err)
		os.Exit(1)
	}
	if result.Forward != nil {
		fmt.Printf("spotlight started (%s)\n", result.Forward.ID)
	} else {
		fmt.Println("spotlight started")
	}
}

func runSpotlightListCommand(args []string) {
	fs := flag.NewFlagSet("spotlight list", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	workspaceID := fs.String("workspace", "", "filter by workspace ID (required)")
	asJSON := fs.Bool("json", false, "output as JSON")
	_ = fs.Parse(args)

	if *workspaceID == "" {
		fmt.Fprintf(os.Stderr, "nexus spotlight list: --workspace is required\n")
		fs.Usage()
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight list: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Forwards []*spotlight.Forward `json:"forwards"`
	}
	if err := daemonRPC(conn, "spotlight.list", map[string]any{
		"workspaceId": *workspaceID,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight list: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		printJSON(result.Forwards)
		return
	}

	if len(result.Forwards) == 0 {
		fmt.Println("no forwards")
		return
	}
	fmt.Printf("%-36s  %-36s  %-10s  %-10s  %s\n", "ID", "WORKSPACE", "LOCAL", "REMOTE", "STATE")
	fmt.Printf("%-36s  %-36s  %-10s  %-10s  %s\n",
		"------------------------------------", "------------------------------------",
		"----------", "----------", "-----")
	for _, fwd := range result.Forwards {
		fmt.Printf("%-36s  %-36s  %-10d  %-10d  %s\n",
			fwd.ID, fwd.WorkspaceID, fwd.LocalPort, fwd.RemotePort, fwd.State)
	}
}

func runSpotlightStopCommand(args []string) {
	fs := flag.NewFlagSet("spotlight stop", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "skip confirmation prompt")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nexus spotlight stop <id> [--force]")
		os.Exit(2)
	}
	id := rest[0]

	if !*force {
		fi, _ := os.Stdin.Stat()
		isTTY := (fi.Mode() & os.ModeCharDevice) != 0
		if isTTY {
			if !confirmPrompt(fmt.Sprintf("stop spotlight forward %s?", id)) {
				fmt.Println("aborted")
				return
			}
		}
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight stop: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := daemonRPC(conn, "spotlight.stop", map[string]any{"id": id}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("stopped spotlight forward %s\n", id)
}

func runSpotlightPortCommand(args []string) {
	if len(args) == 0 {
		printSpotlightPortUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		runSpotlightPortListCommand(rest)
	case "add":
		runSpotlightPortAddCommand(rest)
	case "remove", "rm":
		runSpotlightPortRemoveCommand(rest)
	default:
		printSpotlightPortUsage()
		fmt.Fprintf(os.Stderr, "\nunknown spotlight port subcommand: %s\n", sub)
		os.Exit(2)
	}
}

func runSpotlightPortListCommand(args []string) {
	fs := flag.NewFlagSet("spotlight port list", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	workspaceID := fs.String("workspace", "", "workspace ID (required)")
	asJSON := fs.Bool("json", false, "output as JSON")
	_ = fs.Parse(args)

	if *workspaceID == "" {
		fmt.Fprintf(os.Stderr, "nexus spotlight port list: --workspace is required\n")
		fs.Usage()
		os.Exit(2)
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port list: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	var result struct {
		Forwards []*spotlight.Forward `json:"forwards"`
	}
	if err := daemonRPC(conn, "workspace.ports.list", map[string]any{
		"workspaceId": *workspaceID,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port list: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		printJSON(result.Forwards)
		return
	}

	if len(result.Forwards) == 0 {
		fmt.Println("no ports")
		return
	}
	fmt.Printf("%-10s  %-10s  %-10s  %s\n", "LOCAL", "REMOTE", "PROTOCOL", "STATE")
	fmt.Printf("%-10s  %-10s  %-10s  %s\n", "----------", "----------", "----------", "-----")
	for _, fwd := range result.Forwards {
		proto := fwd.Protocol
		if proto == "" {
			proto = "tcp"
		}
		fmt.Printf("%-10d  %-10d  %-10s  %s\n", fwd.LocalPort, fwd.RemotePort, proto, fwd.State)
	}
}

func runSpotlightPortAddCommand(args []string) {
	fs := flag.NewFlagSet("spotlight port add", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	workspaceID := fs.String("workspace", "", "workspace ID (required)")
	localPort := fs.Int("port", 0, "local port to expose (required)")
	remotePort := fs.Int("remote-port", 0, "remote port (defaults to --port)")
	protocol := fs.String("protocol", "", "protocol (tcp/http)")
	_ = fs.Parse(args)

	if *workspaceID == "" || *localPort == 0 {
		fmt.Fprintf(os.Stderr, "nexus spotlight port add: --workspace and --port are required\n")
		fs.Usage()
		os.Exit(2)
	}
	if *remotePort == 0 {
		*remotePort = *localPort
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port add: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	spec := spotlight.ExposeSpec{
		WorkspaceID: *workspaceID,
		LocalPort:   *localPort,
		RemotePort:  *remotePort,
		Protocol:    *protocol,
	}
	var result struct {
		Forward *spotlight.Forward `json:"forward"`
	}
	if err := daemonRPC(conn, "workspace.ports.add", map[string]any{
		"workspaceId": *workspaceID,
		"spec":        spec,
	}, &result); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port add: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("port %d exposed\n", *localPort)
}

func runSpotlightPortRemoveCommand(args []string) {
	fs := flag.NewFlagSet("spotlight port remove", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	workspaceID := fs.String("workspace", "", "workspace ID (required)")
	forwardID := fs.String("forward-id", "", "forward ID to remove (required)")
	force := fs.Bool("force", false, "skip confirmation prompt")
	_ = fs.Parse(args)

	if *workspaceID == "" || *forwardID == "" {
		fmt.Fprintf(os.Stderr, "nexus spotlight port remove: --workspace and --forward-id are required\n")
		fs.Usage()
		os.Exit(2)
	}

	if !*force {
		fi, _ := os.Stdin.Stat()
		isTTY := (fi.Mode() & os.ModeCharDevice) != 0
		if isTTY {
			if !confirmPrompt(fmt.Sprintf("remove port forward %s?", *forwardID)) {
				fmt.Println("aborted")
				return
			}
		}
	}

	conn, err := ensureDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port remove: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := daemonRPC(conn, "workspace.ports.remove", map[string]any{
		"workspaceId": *workspaceID,
		"forwardId":   *forwardID,
	}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nexus spotlight port remove: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("removed port forward %s\n", *forwardID)
}

func printSpotlightUsage() {
	fmt.Fprint(os.Stderr, `usage: nexus spotlight <subcommand> [options]

subcommands:
  start --workspace <id> --local-port <n> [--remote-port <n>] [--protocol <proto>]
  list --workspace <id> [--json]
  stop <id> [--force]
  port list --workspace <id> [--json]
  port add --workspace <id> --port <n> [--remote-port <n>] [--protocol <proto>]
  port remove --workspace <id> --forward-id <id> [--force]

`)
}

func printSpotlightPortUsage() {
	fmt.Fprint(os.Stderr, `usage: nexus spotlight port <subcommand> [options]

subcommands:
  list --workspace <id> [--json]
  add --workspace <id> --port <n> [--remote-port <n>] [--protocol <proto>]
  remove --workspace <id> --forward-id <id> [--force]

`)
}
