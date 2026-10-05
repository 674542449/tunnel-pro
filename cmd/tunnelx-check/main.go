package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"tunnelx/internal/check"
	"tunnelx/internal/config"
)

func main() {
	path := flag.String("config", "client.json", "normal-mode client configuration")
	strictPath := flag.String("strict-config", "", "optional strict-mode configuration")
	fixtures := flag.Bool("fixtures", false, "test the private acceptance fixtures (server must explicitly allow them)")
	filter := flag.String("case", "", "run only case names containing this text")
	videoHash := flag.String("video-sha256", "", "run controlled video checks with this expected SHA256")
	videoSize := flag.Int64("video-size", 0, "expected controlled video size")
	benchURL := flag.String("bench-url", "", "benchmark HTTP/2 with an HTTPS download URL")
	benchRounds := flag.Int("bench-rounds", 2, "benchmark rounds")
	benchDirect := flag.Bool("bench-direct", false, "include one direct HTTPS sample")
	benchHash := flag.String("bench-sha256", "", "optional expected benchmark payload SHA256")
	out := flag.String("out", "acceptance.json", "report path")
	flag.Parse()
	var c config.Client
	if e := config.Read(*path, &c); e != nil {
		fatal(e)
	}
	c.CAFile = config.Resolve(*path, c.CAFile)
	var strict *config.Client
	if *strictPath != "" {
		strict = new(config.Client)
		if e := config.Read(*strictPath, strict); e != nil {
			fatal(e)
		}
		strict.CAFile = config.Resolve(*strictPath, strict.CAFile)
	}
	var r check.Report
	if *benchURL != "" {
		r = check.Benchmark(c, *benchURL, *benchRounds, *benchDirect, *benchHash, func(s string) { fmt.Println(s) })
	} else if *videoHash != "" {
		r = check.RunVideo(c, strict, *videoHash, *videoSize, func(s string) { fmt.Println(s) })
	} else {
		r = check.Run(c, strict, *fixtures, func(s string) { fmt.Println(s) }, *filter)
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(*out), 0755); e != nil {
		fatal(e)
	}
	if e = os.WriteFile(*out, append(b, '\n'), 0644); e != nil {
		fatal(e)
	}
	fmt.Printf("Acceptance passed=%v, cases=%d, report=%s\n", r.Passed, len(r.Cases), *out)
	if !r.Passed {
		os.Exit(1)
	}
}
func fatal(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
