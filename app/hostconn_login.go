package app

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/sshclient"
)

// Texts a terminal shows while the login it waits for is answered elsewhere.
const (
	waitForwardsLogin = "Идёт вход для пробросов: ответьте в разделе «Пробросы»\r\n"
	waitSessionLogin  = "Идёт вход в консоли «%s»\r\n"
)

// connAsker is who answers the login questions of one acquire: a session's terminal or a host's forwards group.
type connAsker struct {
	out      *outStream
	in       *inputQueue
	trust    trustOwner
	label    string              // "" for the forwards group; the host name for a session
	prompter *sshclient.Prompter // optional: a session passes its own to read Pending after login; nil — built from out/in
	// newInput (optional) gives a fresh input queue for each login this asker answers; it replaces
	// in. The forwards group uses it, so a late answer never reaches the next login.
	newInput func() *inputQueue
}

// loginAttempt is one login of a host connection: a pause first when it is a reconnect, then the
// dial with its questions. Every acquire waiting on it gets its result.
type loginAttempt struct {
	ask    *connAsker
	loop   bool // a reconnect for the forwards: no acquire owns it
	delay  time.Duration
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed once client and err are set

	// Guarded by hostConn.mu.
	waiting  []*connAsker // the asks of the acquires waiting on it
	keep     bool         // the secrets it logs in with may be remembered
	handover bool         // cancelled because its asker left while others still wait

	client *ssh.Client
	err    error
}

// waitText is what ask's terminal shows while the login of answering runs elsewhere; "" for its own.
func waitText(answering, ask *connAsker) string {
	switch {
	case answering.out == ask.out:
		return ""
	case answering.label == "":
		return waitForwardsLogin
	default:
		return fmt.Sprintf(waitSessionLogin, answering.label)
	}
}

// login dials the host with the remembered secrets, asking through att.ask. When the host's login
// parameters in the store are no longer the connection's, it does not dial: the connection goes
// stale (wiping the secrets) and the login fails with errConnStale.
func (c *hostConn) login(att *loginAttempt) (*ssh.Client, error) {
	c.mu.Lock()
	stale := c.stale
	c.mu.Unlock()
	if stale {
		return nil, errConnStale
	}
	ask := att.ask
	in := ask.in
	if ask.newInput != nil {
		in = ask.newInput()
		defer func() { clear(in.Close()) }() // a late answer does not reach the next login
	}
	if in != nil {
		stop := context.AfterFunc(att.ctx, func() { clear(in.Close()) }) // a pending prompt returns at once
		defer stop()
	}
	a := c.a
	if !a.storeUnlocked() {
		return nil, errVaultLocked
	}
	h, t, err := a.hostTarget(c.hostID)
	if err != nil {
		return nil, err
	}
	if paramsOf(h) != c.params {
		if t.Key != nil {
			clear(t.Key.Private)
		}
		c.markStale()
		return nil, errConnStale
	}
	kh, err := knownHostsFor(t, a.knownHosts)
	if err != nil {
		return nil, err
	}
	t.Trust = a.trustAsker(ask.trust, t.User, ask.out)
	c.mu.Lock()
	if t.Key != nil && c.passKeyID == t.Key.ID {
		t.Passphrase = bytes.Clone(c.passphrase) // Dial clears these copies
	}
	t.Password = bytes.Clone(c.password)
	c.mu.Unlock()
	if t.Key != nil {
		keyID := t.Key.ID
		t.OnPassphrase = func(p []byte) { c.remember(att, &c.passphrase, p, keyID) }
	}
	t.OnPassword = func(p []byte) { c.remember(att, &c.password, p, "") }
	p := ask.prompter
	if p == nil {
		p = sshclient.NewPrompter(ask.out, in)
	}
	return dialTarget(att.ctx, t, p, kh)
}

// knownHostsFor opens known_hosts for a login of t; on failure it wipes t's copy of the private key,
// which no dial will use.
func knownHostsFor(t sshclient.Target, open func() (*sshclient.KnownHosts, error)) (*sshclient.KnownHosts, error) {
	kh, err := open()
	if err != nil && t.Key != nil {
		clear(t.Key.Private)
	}
	return kh, err
}

// remember keeps a copy of p, a secret the server just accepted, in *dst unless att may no longer
// keep secrets (the connection closed or went stale meanwhile). keyID is the key a passphrase opens.
func (c *hostConn) remember(att *loginAttempt, dst *[]byte, p []byte, keyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !att.keep {
		return
	}
	clear(*dst)
	*dst = bytes.Clone(p)
	if dst == &c.passphrase {
		c.passKeyID = keyID
	}
}
