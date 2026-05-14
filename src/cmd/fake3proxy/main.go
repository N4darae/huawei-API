package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type listenSpec struct {
	proto string
	addr  string
}

type listenList []listenSpec

func (l *listenList) String() string {
	out := make([]string, 0, len(*l))
	for _, s := range *l {
		out = append(out, s.proto+"://"+s.addr)
	}
	return strings.Join(out, ",")
}

func (l *listenList) Set(v string) error {
	proto, addr, ok := strings.Cut(v, "://")
	if !ok {
		return fmt.Errorf("want <proto>://<host>:<port>, got %q", v)
	}
	switch proto {
	case "socks5", "http":
	default:
		return fmt.Errorf("unknown proto %q, want socks5 or http", proto)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return err
	}
	*l = append(*l, listenSpec{proto: proto, addr: addr})
	return nil
}

type credList map[string]string

func (c credList) String() string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}

func (c credList) Set(v string) error {
	user, pass, ok := strings.Cut(v, ":")
	if !ok || user == "" {
		return fmt.Errorf("want <user>:<password>, got %q", v)
	}
	c[user] = pass
	return nil
}

type server struct {
	creds credList

	mu        sync.Mutex
	listeners []net.Listener
	dropped   bool
}

func main() {
	var listens listenList
	creds := credList{}
	fs := flag.NewFlagSet("fake3proxy", flag.ContinueOnError)
	fs.Var(&listens, "listen", "repeatable, socks5://host:port or http://host:port")
	fs.Var(creds, "cred", "repeatable, <user>:<password> the fake accepts")
	failListen := fs.Bool("fail-listen", false, "bind nothing and exit 0, mimicking a rejected acl keyword")
	dropOnReload := fs.Bool("drop-on-reload", false, "close every listener on SIGUSR1 and stay alive")
	exitAfter := fs.Duration("exit-after", 0, "exit after this delay")
	exitCode := fs.Int("exit-code", 0, "exit status used by -exit-after")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *failListen {
		fmt.Fprintln(os.Stderr, "Unknown operation type: simulated")
		os.Exit(0)
	}
	if len(listens) == 0 {
		fmt.Fprintln(os.Stderr, "fake3proxy: no -listen given")
		os.Exit(2)
	}

	s := &server{creds: creds}
	if err := s.start(listens); err != nil {
		fmt.Fprintln(os.Stderr, "fake3proxy:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	usr := make(chan os.Signal, 4)
	signal.Notify(usr, syscall.SIGUSR1)

	var timeout <-chan time.Time
	if *exitAfter > 0 {
		t := time.NewTimer(*exitAfter)
		defer t.Stop()
		timeout = t.C
	}

	for {
		select {
		case <-ctx.Done():
			s.closeAll()
			return
		case <-timeout:
			s.closeAll()
			os.Exit(*exitCode)
		case <-usr:
			if *dropOnReload {
				s.closeAll()
			}
		}
	}
}

func (s *server) start(specs []listenSpec) error {
	for _, sp := range specs {
		ln, err := net.Listen("tcp", sp.addr)
		if err != nil {
			s.closeAll()
			return err
		}
		s.mu.Lock()
		s.listeners = append(s.listeners, ln)
		s.mu.Unlock()
		go s.accept(ln, sp.proto)
	}
	return nil
}

func (s *server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropped = true
	for _, ln := range s.listeners {
		ln.Close()
	}
	s.listeners = nil
}

func (s *server) accept(ln net.Listener, proto string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.serve(c, proto)
	}
}

func (s *server) serve(c net.Conn, proto string) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if proto == "socks5" {
		s.serveSocks(c)
		return
	}
	s.serveHTTP(c)
}

func (s *server) serveSocks(c net.Conn) {
	head := make([]byte, 2)
	if _, err := readFull(c, head); err != nil || head[0] != 0x05 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := readFull(c, methods); err != nil {
		return
	}
	chosen := byte(0x00)
	authed := len(s.creds) == 0
	for _, m := range methods {
		if m == 0x02 && len(s.creds) > 0 {
			chosen = 0x02
		}
	}
	if _, err := c.Write([]byte{0x05, chosen}); err != nil {
		return
	}
	if chosen == 0x02 {
		u, p, err := readSocksAuth(c)
		if err != nil {
			return
		}
		known, ok := s.creds[u]
		authed = ok && known == p
		if _, err := c.Write([]byte{0x01, 0x00}); err != nil {
			return
		}
	}

	req := make([]byte, 4)
	if _, err := readFull(c, req); err != nil {
		return
	}
	skip := 0
	switch req[3] {
	case 0x01:
		skip = 4
	case 0x03:
		n := make([]byte, 1)
		if _, err := readFull(c, n); err != nil {
			return
		}
		skip = int(n[0])
	case 0x04:
		skip = 16
	}
	if _, err := readFull(c, make([]byte, skip+2)); err != nil {
		return
	}

	rep := byte(0x02)
	if authed {
		rep = 0x05
	}
	c.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func readSocksAuth(c net.Conn) (string, string, error) {
	head := make([]byte, 2)
	if _, err := readFull(c, head); err != nil {
		return "", "", err
	}
	u := make([]byte, int(head[1]))
	if _, err := readFull(c, u); err != nil {
		return "", "", err
	}
	n := make([]byte, 1)
	if _, err := readFull(c, n); err != nil {
		return "", "", err
	}
	p := make([]byte, int(n[0]))
	if _, err := readFull(c, p); err != nil {
		return "", "", err
	}
	return string(u), string(p), nil
}

func (s *server) serveHTTP(c net.Conn) {
	r := bufio.NewReader(c)
	if _, err := r.ReadString('\n'); err != nil {
		return
	}
	cred := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "Proxy-Authorization") {
			cred = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "Basic "))
		}
	}

	if len(s.creds) > 0 && !s.credOK(cred) {
		c.Write([]byte("HTTP/1.0 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"proxy\"\r\nConnection: close\r\n\r\n"))
		return
	}
	c.Write([]byte("HTTP/1.0 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
}

func (s *server) credOK(cred string) bool {
	raw, err := base64.StdEncoding.DecodeString(cred)
	if err != nil {
		return false
	}
	u, p, ok := strings.Cut(string(raw), ":")
	if !ok {
		return false
	}
	known, found := s.creds[u]
	return found && known == p
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		if k > 0 {
			n += k
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
