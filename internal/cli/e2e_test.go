package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestMain(m *testing.M) {
	if os.Getenv("SOAP_TEST_LISTENER") == "1" {
		listenForever()
	}
	os.Exit(m.Run())
}

// listenForever is the end-to-end test's helper process: it opens a TCP port, prints the port
// number and waits to be killed.
func listenForever() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(3)
	}
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(0)
		}
		c.Close()
	}
}

func TestEndToEndKillFreesPort(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real process and runs real commands")
	}
	child := exec.Command(os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), "SOAP_TEST_LISTENER=1")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill() })
	var port int
	if _, err := fmt.Fscan(stdout, &port); err != nil {
		t.Fatalf("reading the helper's port: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		child.Wait() // reaps the helper, so soap sees it disappear
		close(exited)
	}()

	d, err := HomeDeps(t.TempDir(), false) // RealDeps would write a config file in the real home folder
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d.Stdin, d.Stdout, d.Stderr = strings.NewReader(""), &out, &out

	if code := Run(d, []string{"ports", "--json"}); code != 0 {
		t.Fatalf("ports exit %d: %s", code, out.String())
	}
	var ls []model.Listener
	if err := json.Unmarshal(out.Bytes(), &ls); err != nil {
		t.Fatalf("ports --json: %v\n%s", err, out.String())
	}
	if !slices.ContainsFunc(ls, func(l model.Listener) bool { return int(l.Port) == port && l.PID == child.Process.Pid }) {
		t.Fatalf("port %d (pid %d) isn't listed", port, child.Process.Pid)
	}

	out.Reset()
	if code := Run(d, []string{"kill", fmt.Sprintf(":%d", port), "-y"}); code != 0 {
		t.Fatalf("kill exit %d: %s", code, out.String())
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the helper is still running")
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
		c.Close()
		t.Fatal("the port is still open")
	}
}
