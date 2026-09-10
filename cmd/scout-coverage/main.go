package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"openmaps/internal/routing/qualification"
	"os"
)

func main() {
	boundaries := flag.String("boundaries", "", "pinned Census ZIP")
	audit := flag.String("audit", "", "source audit JSON")
	routes := flag.String("routes", "", "optional frozen offline JSONL")
	flag.Parse()
	result, e := qualification.Coverage(*boundaries, *audit, *routes)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e = enc.Encode(result); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
