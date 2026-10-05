package control

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCommercialSMTPRejectsPlaintextAndAlertDeduplicates(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	commands := make(chan string, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(c, "220 fixture ESMTP\r\n")
		line, _ := bufio.NewReader(c).ReadString('\n')
		commands <- line
		fmt.Fprint(c, "250 fixture\r\n")
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	var number int
	fmt.Sscan(port, &number)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e = sendMail(ctx, MailConfig{Host: "127.0.0.1", Port: number, From: "ops@example.test", Username: "private-user", Password: "private-password"}, MailMessage{ID: ID(), To: "user@example.test", Subject: "test"}, "body")
	if e == nil {
		t.Fatal("plaintext SMTP accepted")
	}
	if command := <-commands; strings.Contains(command, "private") {
		t.Fatal("SMTP credential sent before TLS")
	}
	a, _, _ := commercialSetup(t)
	a.Config.Mail.AlertsTo = "ops@example.test"
	now := time.Now().Unix()
	a.Store.Update(func(d *State) error {
		d.Nodes = append(d.Nodes, Node{ID: ID(), Enabled: true, LastSeen: now - 100})
		return nil
	})
	a.maintain(now)
	a.maintain(now + 1)
	d := a.Store.Snapshot()
	if len(d.Outbox) != 1 {
		t.Fatal("duplicate outage alerts")
	}
	a.Store.Update(func(d *State) error { d.Nodes[0].LastSeen = now + 2; return nil })
	a.maintain(now + 2)
	a.maintain(now + 3)
	if len(a.Store.Snapshot().Outbox) != 2 {
		t.Fatal("recovery notification not deduplicated")
	}
}
