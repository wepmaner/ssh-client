package sshtest

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestRegisteredHandlersServeOtherPackages(t *testing.T) {
	RegisterChannelHandler("test-hello", func(nc ssh.NewChannel) {
		ch, reqs, err := nc.Accept()
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		ignoreWrite(ch.Write([]byte("hello")))
		ignoreErr(ch.Close())
	})
	RegisterGlobalRequestHandler("test-ping", func(_ *ssh.ServerConn, req *ssh.Request) {
		ignoreErr(req.Reply(true, []byte("pong")))
	})

	addr, hostKey := Start(t, Opts{Password: "pw"})
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("pw")},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(c.Close()) })

	ok, reply, err := c.SendRequest("test-ping", true, nil)
	if err != nil || !ok || string(reply) != "pong" {
		t.Fatalf("global request: %v %q %v", ok, reply, err)
	}
	ch, reqs, err := c.OpenChannel("test-hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	var got bytes.Buffer
	if _, err := got.ReadFrom(ch); err != nil {
		t.Fatal(err)
	}
	if got.String() != "hello" {
		t.Fatalf("channel data %q", got.String())
	}
	if _, _, err := c.OpenChannel("unknown", nil); err == nil {
		t.Fatal("unknown channel type accepted")
	}
}

func ignoreWrite(int, error) {}
