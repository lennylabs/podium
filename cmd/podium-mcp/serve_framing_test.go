package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// spec: §6.8 — the host drives the MCP lifecycle and sends JSON-RPC
// notifications (notifications/initialized after initialize, and possibly
// notifications/cancelled or notifications/roots/list_changed). A
// notification carries no id and must not receive a response. The serve loop
// must not answer it with a -32601 error frame.
func TestServe_NotificationProducesNoResponse(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	initParams, _ := json.Marshal(map[string]string{"protocolVersion": protocolVersion})
	initReq, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: initParams})
	// notifications/initialized carries no id field.
	notif := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	in := bytes.NewReader([]byte(string(initReq) + "\n" + notif + "\n"))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d response lines, want exactly 1 (initialize only):\n%s", len(lines), out.String())
	}
	if strings.Contains(out.String(), "-32601") || strings.Contains(out.String(), "method not found") {
		t.Errorf("notification drew an error frame, must be silent:\n%s", out.String())
	}
}

// spec: §6.8 / JSON-RPC 2.0 — a bare notification (no surrounding request)
// produces no output at all.
func TestServe_LoneNotificationsAreSilent(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	in := bytes.NewReader([]byte(
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}` + "\n"))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("notifications must produce no output, got:\n%s", out.String())
	}
}

// spec: §6.8 — a request with an id is still answered even when its method is
// unknown, distinguishing it from an id-less notification.
func TestDispatchLine_RequestWithIDStillAnswered(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	in := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":9,"method":"definitely/not/real"}` + "\n"))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(out.String(), "-32601") {
		t.Errorf("a request with an id and unknown method must get a -32601 error, got:\n%s", out.String())
	}
}

// spec: §6.8 — the long-lived subprocess must survive a single oversized
// inbound frame: that request fails with a structured error and subsequent
// frames on the same stdio session are still served.
func TestServe_OversizedFrameDoesNotTerminate(t *testing.T) {
	srv := &mcpServer{cfg: &config{}}
	oversized := append(bytes.Repeat([]byte("x"), maxFrameBytes+10), '\n')
	toolsReq, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"})
	in := bytes.NewReader(append(oversized, append(toolsReq, '\n')...))
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve returned an error; an oversized frame must not tear down the process: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d response lines, want 2 (oversized error + tools/list):\n%s", len(lines), out.String())
	}
	var errResp rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &errResp); err != nil {
		t.Fatalf("first line not JSON: %v", err)
	}
	if errResp.Error == nil || errResp.Error.Code != -32600 {
		t.Errorf("oversized frame must yield a -32600 error, got %+v", errResp.Error)
	}
	if !strings.Contains(out.String(), "load_artifact") {
		t.Errorf("tools/list after the oversized frame was not served:\n%s", out.String())
	}
}

// spec: §6.8 — the host owns the lifecycle; the serve loop ends cleanly when
// stdin reaches EOF (the host closing the pipe), returning no error.
func TestServe_StopsCleanlyOnStdinEOF(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	// A final frame with no trailing newline still serves, then EOF ends the
	// loop with a nil error.
	toolsReq, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"})
	in := bytes.NewReader(toolsReq) // no trailing '\n'
	var out bytes.Buffer
	if err := srv.serve(in, &out); err != nil {
		t.Fatalf("serve must return nil on stdin EOF, got: %v", err)
	}
	if !strings.Contains(out.String(), "load_artifact") {
		t.Errorf("trailing newline-less frame was not served:\n%s", out.String())
	}
}

// spec: §6.8 — the host owns signal handling and shutdown. The bridge installs
// no lifecycle (SIGINT/SIGTERM) handlers; the only signal it consults is
// SIGHUP for the §6.3.2.1 token re-read, which is out of §6.8 scope.
func TestHangupSignals_OnlyTokenReReadSignal(t *testing.T) {
	t.Parallel()
	for _, sig := range hangupSignals() {
		name := sig.String()
		if strings.Contains(name, "interrupt") || strings.Contains(name, "terminated") {
			t.Errorf("bridge must not handle lifecycle signal %q; the host owns shutdown", name)
		}
	}
}

// spec: §6.8 — readFrame bounds buffered memory and reports an oversized frame
// instead of accumulating it, so the serve loop can fail one request and keep
// going.
func TestReadFrame(t *testing.T) {
	t.Parallel()
	t.Run("normal frames", func(t *testing.T) {
		r := bufio.NewReaderSize(strings.NewReader("alpha\nbeta\n"), 16)
		line, tooLong, err := readFrame(r, 1024)
		if err != nil || tooLong || string(line) != "alpha\n" {
			t.Fatalf("first: line=%q tooLong=%v err=%v", line, tooLong, err)
		}
		line, tooLong, err = readFrame(r, 1024)
		if err != nil || tooLong || string(line) != "beta\n" {
			t.Fatalf("second: line=%q tooLong=%v err=%v", line, tooLong, err)
		}
		if _, _, err = readFrame(r, 1024); err != io.EOF {
			t.Fatalf("third: want io.EOF, got %v", err)
		}
	})
	t.Run("oversized frame is drained and the next frame survives", func(t *testing.T) {
		// A line longer than max, then a normal line. The reader buffer is
		// smaller than the line to force the ErrBufferFull drain path.
		big := strings.Repeat("z", 100)
		r := bufio.NewReaderSize(strings.NewReader(big+"\n"+"ok\n"), 8)
		line, tooLong, err := readFrame(r, 16)
		if !tooLong {
			t.Fatalf("want tooLong for a %d-byte frame over a 16-byte cap", len(big))
		}
		if len(line) != 0 {
			t.Fatalf("oversized frame must return an empty line, got %q", line)
		}
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		line, tooLong, err = readFrame(r, 16)
		if err != nil || tooLong || string(line) != "ok\n" {
			t.Fatalf("next frame: line=%q tooLong=%v err=%v", line, tooLong, err)
		}
	})
	t.Run("trailing partial frame returns with EOF", func(t *testing.T) {
		r := bufio.NewReaderSize(strings.NewReader("tail"), 16)
		line, tooLong, err := readFrame(r, 1024)
		if err != io.EOF || tooLong || string(line) != "tail" {
			t.Fatalf("line=%q tooLong=%v err=%v", line, tooLong, err)
		}
	})
}
