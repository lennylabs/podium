//go:build !windows

package source

import (
	"io"

	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh/agent"
)

// dialAgent connects to the SSH agent at SSH_AUTH_SOCK through the call
// go-git's default ssh auth makes, so the failures and their messages are
// go-git's, and returns the agent client with the Unix socket it reads.
func dialAgent() (agent.Agent, io.Closer, error) {
	ag, conn, err := sshagent.New()
	if err != nil {
		return nil, nil, err
	}
	return ag, conn, nil
}
