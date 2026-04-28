package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func buildSchema() Schema {
	definitions, notifications := buildDefinitions()
	return Schema{
		Version:       "1",
		Methods:       buildMethods(),
		Notifications: notifications,
		Definitions:   definitions,
	}
}

func main() {
	var outFile string
	flag.StringVar(&outFile, "out", "", "write output to file instead of stdout")
	flag.Parse()

	schema := buildSchema()

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal error: %v\n", err)
		os.Exit(1)
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println(string(data))
}
