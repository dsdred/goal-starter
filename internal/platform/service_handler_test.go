//go:build windows

package platform

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// TestServiceHandlerLifecycleSequence drives the handler without a real SCM
// and verifies the D6 state contract: StartPending → Running (only after the
// HTTP bind) → StopPending (dwWaitHint 30 000 ms) → Stopped, with the
// application shutdown path completing before Stopped is reported.
func TestServiceHandlerLifecycleSequence(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	appReturned := make(chan struct{})
	app := func(ctx context.Context) error {
		<-ctx.Done()
		ln.Close()
		close(appReturned)
		return nil
	}

	r := make(chan svc.ChangeRequest, 2)
	s := make(chan svc.Status, 8)
	h := &serviceHandler{
		opts: ServiceRunOptions{
			Name:       "GoAlTest",
			RunApp:     app,
			ServeAddr:  addr,
			StatusText: func() string { return "instances: running=1" },
		},
		log: &eventLogger{},
	}

	type result struct {
		ec   bool
		code uint32
	}
	resCh := make(chan result, 1)
	go func() {
		ec, code := h.Execute(nil, r, s)
		resCh <- result{ec, code}
	}()

	st := <-s
	if st.State != svc.StartPending {
		t.Fatalf("first status = %v, want StartPending", st.State)
	}
	st = <-s
	if st.State != svc.Running {
		t.Fatalf("second status = %v, want Running (after bind)", st.State)
	}

	// Interrogate returns real state (D6.4).
	r <- svc.ChangeRequest{Cmd: svc.Interrogate}
	st = <-s
	if st.State != svc.Running {
		t.Fatalf("interrogate status = %v, want Running", st.State)
	}

	// Stop request (D6.2).
	r <- svc.ChangeRequest{Cmd: svc.Stop}
	st = <-s
	if st.State != svc.StopPending {
		t.Fatalf("stop status = %v, want StopPending", st.State)
	}
	if st.WaitHint != 30000 {
		t.Fatalf("wait hint = %d, want 30000", st.WaitHint)
	}
	st = <-s
	if st.State != svc.Stopped {
		t.Fatalf("final status = %v, want Stopped", st.State)
	}

	select {
	case <-appReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("application shutdown path did not complete")
	}
	res := <-resCh
	if res.ec || res.code != 0 {
		t.Fatalf("exit = (%v, %d), want (false, 0)", res.ec, res.code)
	}
}

// TestServiceHandlerPreBindFailure verifies D6.1: a failure before the HTTP
// bind reports Stopped (never Running) and a non-zero exit code.
func TestServiceHandlerPreBindFailure(t *testing.T) {
	app := func(ctx context.Context) error {
		return errors.New("config validation failed (test)")
	}
	r := make(chan svc.ChangeRequest, 1)
	s := make(chan svc.Status, 8)
	h := &serviceHandler{
		opts: ServiceRunOptions{
			Name:      "GoAlTest",
			RunApp:    app,
			ServeAddr: "127.0.0.1:1", // never bound
		},
		log: &eventLogger{},
	}

	resCh := make(chan struct{}, 1)
	var code uint32
	go func() {
		_, c := h.Execute(nil, r, s)
		code = c
		resCh <- struct{}{}
	}()

	st := <-s
	if st.State != svc.StartPending {
		t.Fatalf("first status = %v, want StartPending", st.State)
	}
	select {
	case st = <-s:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not terminate after pre-bind failure")
	}
	if st.State != svc.Stopped {
		t.Fatalf("status = %v, want Stopped (never Running)", st.State)
	}
	<-resCh
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
}

// TestServiceHandlerStopBeforeBind verifies that an SCM stop request arriving
// before the bind completes terminates cleanly (Stopped, exit 0).
func TestServiceHandlerStopBeforeBind(t *testing.T) {
	app := func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}
	r := make(chan svc.ChangeRequest, 2)
	s := make(chan svc.Status, 8)
	h := &serviceHandler{
		opts: ServiceRunOptions{
			Name:      "GoAlTest",
			RunApp:    app,
			ServeAddr: "127.0.0.1:1", // never bound
		},
		log: &eventLogger{},
	}

	resCh := make(chan uint32, 1)
	go func() {
		_, c := h.Execute(nil, r, s)
		resCh <- c
	}()

	st := <-s
	if st.State != svc.StartPending {
		t.Fatalf("first status = %v, want StartPending", st.State)
	}
	// The app never binds and never exits on its own: the stop request must
	// cancel its context and terminate the handler.
	r <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case st = <-s:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not terminate on pre-bind stop")
	}
	if st.State != svc.Stopped {
		t.Fatalf("status = %v, want Stopped", st.State)
	}
	if code := <-resCh; code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
