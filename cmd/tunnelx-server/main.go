package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"tunnelx/internal/config"
	"tunnelx/internal/nodeagent"
	"tunnelx/internal/server"
)

func main() {
	path := flag.String("config", "/etc/tunnelx/server.json", "configuration path")
	check := flag.Bool("check", false, "validate configuration and TLS material then exit")
	agentPath := flag.String("agent", "", "optional managed node agent configuration")
	flag.Parse()
	var c config.Server
	if e := config.Read(*path, &c); e != nil {
		fatal(e)
	}
	c.CertFile = config.Resolve(*path, c.CertFile)
	c.KeyFile = config.Resolve(*path, c.KeyFile)
	c.ECHKeyFile = config.Resolve(*path, c.ECHKeyFile)
	c.InnerCertFile = config.Resolve(*path, c.InnerCertFile)
	c.InnerKeyFile = config.Resolve(*path, c.InnerKeyFile)
	c.PublicDir = config.Resolve(*path, c.PublicDir)
	s, e := server.New(c)
	if e != nil {
		fatal(e)
	}
	var agentConfig nodeagent.Config
	if c.ManagedOnly && *agentPath == "" {
		fatal(fmt.Errorf("managed-only nodes require an agent"))
	}
	if *agentPath != "" {
		if e = config.Read(*agentPath, &agentConfig); e != nil {
			fatal(e)
		}
		agentConfig.StateFile = config.Resolve(*agentPath, agentConfig.StateFile)
		if e = agentConfig.Validate(); e != nil {
			fatal(e)
		}
	}
	if *check {
		fmt.Println("Configuration and TLS material valid")
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *agentPath != "" {
		agent, e := nodeagent.New(agentConfig)
		if e != nil {
			fatal(e)
		}
		s.Access = agent
		agent.ManagedOnly = c.ManagedOnly
		agent.Connections = s.Active.Load
		stop := agent.Run(ctx)
		defer func() { cancel(); stop() }()
	}
	fmt.Printf("Listening on %s (TLS 1.3 HTTP/2)\n", c.Listen)
	if e = s.Serve(ctx); e != nil && ctx.Err() == nil {
		fatal(e)
	}
}
func fatal(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
