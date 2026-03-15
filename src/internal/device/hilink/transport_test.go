package hilink

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type stubServer struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	tokens   []string
	handler  func(w http.ResponseWriter, r *http.Request, n int)
}

func (s *stubServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 0)
	if r.Body != nil {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		buf = b[:n]
	}
	s.mu.Lock()
	s.requests = append(s.requests, r)
	s.bodies = append(s.bodies, string(buf))
	s.tokens = append(s.tokens, r.Header.Get(HeaderToken))
	n := len(s.requests)
	s.mu.Unlock()
	s.handler(w, r, n)
}

func (s *stubServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func newTestClient(t *testing.T, h func(w http.ResponseWriter, r *http.Request, n int)) (*Client, *stubServer) {
	t.Helper()
	stub := &stubServer{handler: h}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	c := New(netip.MustParseAddr("192.168.8.1"), Options{
		BaseURL: srv.URL,
		Timeout: 2 * time.Second,
		Sleep:   func(context.Context, time.Duration) {},
	})
	return c, stub
}

func writeSesTok(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", ContentTypeResponse)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(XMLProlog + "\n<response>\n<SesInfo>SessionID=abc</SesInfo>\n<TokInfo>" + token + "</TokInfo>\n</response>\n"))
}

func TestStatus200WithErrorBodyIsAnError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok1")
			return
		}
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("error_100003.xml"))
	})
	var out infoResponse
	err := c.Get(context.Background(), PathDeviceInformation, &out)
	if !errors.Is(err, domain.ErrNeedLogin) {
		t.Fatalf("HTTP 200 with <error> body must be an error, got %v", err)
	}
}

func TestContentTypeTextHTMLIsAccepted(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok1")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("device_information.xml"))
	})
	info, err := c.Information(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceName != "E3372" {
		t.Fatalf("info = %+v", info)
	}
}

func TestSingleRetryOnTokenError(t *testing.T) {
	var posts int
	c, stub := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok"+itoa(n))
			return
		}
		posts++
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		if posts == 1 {
			w.Write(Fixture("error_125002.xml"))
			return
		}
		w.Write(Fixture("response_ok.xml"))
	})
	if err := c.Reboot(context.Background()); err != nil {
		t.Fatalf("retry after 125002 should succeed: %v", err)
	}
	if posts != 2 {
		t.Fatalf("expected exactly one retry, saw %d attempts", posts)
	}
	if stub.count() != 4 {
		t.Fatalf("expected handshake+post+handshake+post, saw %d requests", stub.count())
	}
}

func TestNoInfiniteRetryOnPersistentTokenError(t *testing.T) {
	var posts int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok"+itoa(n))
			return
		}
		posts++
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("error_125003.xml"))
	})
	err := c.Reboot(context.Background())
	if !errors.Is(err, domain.ErrTokenInvalid) {
		t.Fatalf("err = %v", err)
	}
	if posts != MaxAttempts {
		t.Fatalf("attempts = %d, want %d", posts, MaxAttempts)
	}
}

func TestNonTokenErrorIsNotRetried(t *testing.T) {
	var posts int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok1")
			return
		}
		posts++
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("error_112001.xml"))
	})
	err := c.Reboot(context.Background())
	if !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if posts != 1 {
		t.Fatalf("112001 must not be retried, attempts = %d", posts)
	}
}

func TestBackoffSchedule(t *testing.T) {
	if MaxAttempts != 2 {
		t.Fatalf("MaxAttempts = %d", MaxAttempts)
	}
	if Backoffs[0] != 200*time.Millisecond || Backoffs[1] != 600*time.Millisecond {
		t.Fatalf("backoffs = %v", Backoffs)
	}
	var slept []time.Duration
	stub := &stubServer{handler: func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok"+itoa(n))
			return
		}
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("error_125002.xml"))
	}}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	c := New(netip.MustParseAddr("192.168.8.1"), Options{
		BaseURL: srv.URL,
		Sleep:   func(_ context.Context, d time.Duration) { slept = append(slept, d) },
	})
	_ = c.Reboot(context.Background())
	if len(slept) != 1 || slept[0] != Backoffs[0] {
		t.Fatalf("slept = %v", slept)
	}
}

func TestTokenIsSentAndRotatedOnPost(t *testing.T) {
	c, stub := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			w.Header().Set("Content-Type", ContentTypeResponse)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(XMLProlog + "\n<response>\n<SesInfo>SessionID=abc</SesInfo>\n<TokInfo>a#b#c</TokInfo>\n</response>\n"))
			return
		}
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("response_ok.xml"))
	})
	ctx := context.Background()
	if err := c.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.tokens) != 3 {
		t.Fatalf("requests = %d", len(stub.tokens))
	}
	if stub.tokens[1] != "a" || stub.tokens[2] != "b" {
		t.Fatalf("tokens sent = %v, want a then b", stub.tokens[1:])
	}
}

func TestResponseHeaderRefillsTokenQueue(t *testing.T) {
	c, stub := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "seed")
			return
		}
		h := w.Header()
		h.Set("Content-Type", ContentTypeResponse)
		h[HeaderTokenOne] = []string{"fresh1"}
		h[HeaderTokenTwo] = []string{"fresh2"}
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("response_ok.xml"))
	})
	ctx := context.Background()
	if err := c.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.tokens[2] != "fresh1" {
		t.Fatalf("second post used %q, want the refilled token", stub.tokens[2])
	}
}

func TestUnreachableWhenConnectionFails(t *testing.T) {
	c := New(netip.MustParseAddr("192.0.2.1"), Options{
		BaseURL: "http://127.0.0.1:1",
		Timeout: 200 * time.Millisecond,
		Sleep:   func(context.Context, time.Duration) {},
	})
	var out infoResponse
	err := c.Get(context.Background(), PathDeviceInformation, &out)
	if !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
	if c.Reachable(context.Background()) {
		t.Fatal("Reachable must be false when the socket refuses")
	}
}

func TestReachableTrueWhenDeviceAnswersWithError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok1")
			return
		}
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("error_100003.xml"))
	})
	if !c.Reachable(context.Background()) {
		t.Fatal("a device answering 100003 is reachable")
	}
}

func TestPostSendsXMLProlog(t *testing.T) {
	c, stub := newTestClient(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasSuffix(r.URL.Path, PathSesTokInfo) {
			writeSesTok(w, "tok1")
			return
		}
		w.Header().Set("Content-Type", ContentTypeResponse)
		w.WriteHeader(http.StatusOK)
		w.Write(Fixture("response_ok.xml"))
	})
	if err := c.Reboot(context.Background()); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	body := stub.bodies[1]
	if !strings.HasPrefix(body, XMLProlog) {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(body, "<Control>1</Control>") {
		t.Fatalf("reboot body = %q", body)
	}
}
