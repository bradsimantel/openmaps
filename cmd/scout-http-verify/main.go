package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"openmaps/internal/routing/qualification"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type paths []string

func (p *paths) String() string     { return fmt.Sprint([]string(*p)) }
func (p *paths) Set(s string) error { *p = append(*p, s); return nil }
func main() {
	var o qualification.HTTPOptions
	var inputs paths
	flag.StringVar(&o.URL, "url", "http://127.0.0.1:8097", "service URL")
	flag.StringVar(&o.Out, "out", "", "new report directory")
	flag.Var(&inputs, "offline", "verified offline JSONL (repeatable)")
	flag.IntVar(&o.Workers, "workers", 1, "concurrent clients 1..4")
	seconds := flag.Int("seconds", 0, "stress duration 0..1800; zero runs each case once")
	flag.Parse()
	o.Offline = inputs
	o.Duration = time.Duration(*seconds) * time.Second
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	summary, e := qualification.VerifyHTTP(ctx, o)
	if summary != nil {
		json.NewEncoder(os.Stdout).Encode(summary)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
