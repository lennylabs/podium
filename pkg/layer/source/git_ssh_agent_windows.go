//go:build windows

package source

import (
	"errors"
	"io"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio"
	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sys/windows"
)

// nativeAgentPipe is the named pipe of the Windows OpenSSH agent, the one
// xanzy/ssh-agent dials when Pageant is not running.
const nativeAgentPipe = `\\.\pipe\openssh-ssh-agent`

// errNoWindowsAgent is the message xanzy/ssh-agent returns when it finds
// neither agent, kept so that the clone error matches go-git's default.
var errNoWindowsAgent = errors.New("SSH agent requested, but could not detect Pageant or Windows native SSH agent")

var procFindWindow = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW")

// dialAgent connects to the SSH agent the way xanzy/ssh-agent's New does on
// Windows, Pageant first and then the native OpenSSH agent pipe, and returns
// the agent client with the connection the caller must close.
//
// New is not called for the native agent because it returns a nil connection
// with a client over the pipe, and that client's reader goroutine and the
// pipe handle then stay open for the life of the process. A Pageant client
// holds neither: New's Pageant transport has no Close method, so the agent
// client serializes its calls without a reader goroutine, and each call is a
// window message. New detects Pageant again itself, so a Pageant that exits
// between the two checks makes New dial the pipe and keep it, which is the
// behaviour before this function existed.
func dialAgent() (agent.Agent, io.Closer, error) {
	if pageantRunning() {
		ag, _, err := sshagent.New()
		return ag, nil, err
	}
	conn, err := winio.DialPipe(nativeAgentPipe, nil)
	if err != nil {
		return nil, nil, errNoWindowsAgent
	}
	return agent.NewClient(conn), conn, nil
}

// pageantRunning reports whether a Pageant window exists, by the FindWindowW
// lookup xanzy/ssh-agent makes.
func pageantRunning() bool {
	name, err := syscall.UTF16PtrFromString("Pageant")
	if err != nil {
		return false
	}
	h, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)))
	return h != 0
}
