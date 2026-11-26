//go:build windows

package source

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh/agent"
)

// Spec: §7.3.1 — on Windows, with neither Pageant nor the native OpenSSH
// agent running, dialAgent fails with xanzy/ssh-agent's message. With the
// native agent running, it returns the pipe connection, the agent answers
// over it, and closing it ends the agent's side of the connection, so the
// snapshot's release frees the pipe handle. The test owns the native agent's
// pipe name, so it skips on a host where an agent already runs.
func TestDialAgent_WindowsNativeAgent(t *testing.T) {
	if sshagent.Available() {
		t.Skip("an SSH agent is running on this host")
	}
	_, _, want := sshagent.New()
	if _, conn, err := dialAgent(); err == nil || want == nil || err.Error() != want.Error() || conn != nil {
		t.Fatalf("dialAgent with no agent = %v (connection %v), want %v", err, conn, want)
	}

	ln, err := winio.ListenPipe(nativeAgentPipe, nil)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", nativeAgentPipe, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(served)
			return
		}
		_ = agent.ServeAgent(keyring, c)
		_ = c.Close()
		close(served)
	}()

	ag, conn, err := dialAgent()
	if err != nil || conn == nil {
		t.Fatalf("dialAgent with the native agent = connection %v, %v; want a connection", conn, err)
	}
	signers, err := ag.Signers()
	if err != nil || len(signers) != 1 {
		t.Fatalf("Signers = %d, %v; want the one key", len(signers), err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the agent connection stayed open after Close")
	}
}
