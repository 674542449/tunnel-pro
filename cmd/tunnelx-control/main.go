package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"tunnelx/internal/config"
	"tunnelx/internal/control"
)

func main() {
	path := flag.String("config", "control.json", "private control configuration")
	init := flag.Bool("init", false, "generate private bootstrap configuration without starting")
	public := flag.String("public-url", "https://test.xiaguamail.com/control", "public HTTPS base URL")
	check := flag.Bool("check", false, "validate and initialize store, then exit")
	backup := flag.String("backup", "", "export a private consistent snapshot to an absolute path and exit")
	backupKey := flag.String("backup-key", "", "32-byte private encryption key file; encrypt backup if provided")
	restore := flag.String("restore-new", "", "restore an encrypted backup into a NEW empty store and exit")
	flag.Parse()
	if *init {
		if _, e := os.Stat(*path); !os.IsNotExist(e) {
			fatal(fmt.Errorf("refusing to overwrite bootstrap config"))
		}
		c := control.Config{Listen: "127.0.0.1:18081", PublicURL: *public, DataFile: "state/control.json", AdminEmail: "admin@tunnelx.local", AdminPassword: control.Token(), Registration: true, TrialHours: 24}
		b, _ := json.MarshalIndent(c, "", "  ")
		if e := control.WriteFile(*path, b); e != nil {
			fatal(e)
		}
		fmt.Println("Private bootstrap credentials written to " + *path)
		return
	}
	var c control.Config
	if e := config.Read(*path, &c); e != nil {
		fatal(e)
	}
	c.DataFile = config.Resolve(*path, c.DataFile)
	if *restore != "" {
		if e := c.Validate(); e != nil {
			fatal(e)
		}
		if e := control.RestoreNew(c, *restore, *backupKey); e != nil {
			fatal(e)
		}
		fmt.Println("Encrypted backup restored into new store")
		return
	}
	a, e := control.NewAPI(c)
	if e != nil {
		fatal(e)
	}
	if *check {
		fmt.Println("Control configuration and persistent store valid")
		return
	}
	if *backup != "" {
		if *backupKey != "" {
			e = a.Store.EncryptedBackup(*backup, *backupKey)
		} else {
			e = a.Store.Export(*backup)
		}
		if e != nil {
			fatal(e)
		}
		a.Store.Close()
		fmt.Println("Private snapshot exported")
		return
	}
	ln, e := net.Listen("tcp", c.Listen)
	if e != nil {
		fatal(e)
	}
	h := &http.Server{Handler: a, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stopMaintenance := a.RunMaintenance(ctx)
	defer func() { cancel(); stopMaintenance(); a.Store.Close() }()
	go func() { <-ctx.Done(); h.Close() }()
	fmt.Printf("tunnelX Control %s on %s; data %s\n", control.ConsoleVersion, c.Listen, filepath.Base(c.DataFile))
	if e = h.Serve(ln); e != nil && e != http.ErrServerClosed {
		fatal(e)
	}
}
func fatal(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
