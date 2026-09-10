package main

import (
	"context"
	"flag"
	"fmt"
	"openmaps/internal/supervisor"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var o supervisor.Options
	flag.StringVar(&o.Root, "root", "", "filesystem to monitor")
	flag.StringVar(&o.Report, "report", "", "new resource report filename")
	flag.Int64Var(&o.RSSMiB, "rss-mib", 4096, "sampled RSS threshold 1..8192 MiB")
	flag.Int64Var(&o.ReserveGiB, "reserve-gib", 32, "minimum free disk 32..1024 GiB")
	flag.Parse()
	o.Command = flag.Args()
	o.Stdout = os.Stdout
	o.Stderr = os.Stderr
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	r, e := supervisor.Run(ctx, o)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		code := r.ReturnCode
		if code <= 0 {
			code = 2
		}
		os.Exit(code)
	}
}
