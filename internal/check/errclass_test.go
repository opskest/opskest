package check

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"opskest/internal/model"
)

func TestClassifyErrDNS(t *testing.T) {
	// The ".invalid" TLD is reserved by RFC 2606 to never resolve, so this
	// is a reliable, network-independent way to get a real *net.DNSError.
	_, err := net.LookupHost("nonexistent.invalid")
	if err == nil {
		t.Skip("expected nonexistent.invalid to fail to resolve; got no error")
	}

	class, reason := ClassifyErr(err)
	if class != model.ErrDNS {
		t.Errorf("class = %q, want %q (reason=%q, err=%v)", class, model.ErrDNS, reason, err)
	}
}

func TestClassifyErrRefused(t *testing.T) {
	// Bind a listener to get a free loopback port, then close it so
	// nothing is listening there: dialing it should be refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	_, dialErr := net.DialTimeout("tcp", addr, 2*time.Second)
	if dialErr == nil {
		t.Fatalf("expected dialing a closed port to fail")
	}

	class, reason := ClassifyErr(dialErr)
	if class != model.ErrRefused {
		t.Errorf("class = %q, want %q (reason=%q, err=%v)", class, model.ErrRefused, reason, dialErr)
	}
}

func TestClassifyErrTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()

	class, reason := ClassifyErr(ctx.Err())
	if class != model.ErrTimeout {
		t.Errorf("class = %q, want %q (reason=%q)", class, model.ErrTimeout, reason)
	}
}

func TestClassifyErrGenericNetwork(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}

	class, reason := ClassifyErr(opErr)
	if class != model.ErrNetwork {
		t.Errorf("class = %q, want %q (reason=%q)", class, model.ErrNetwork, reason)
	}
}

func TestClassifyErrNil(t *testing.T) {
	class, reason := ClassifyErr(nil)
	if class != model.ErrNone {
		t.Errorf("class = %q, want %q", class, model.ErrNone)
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
}
