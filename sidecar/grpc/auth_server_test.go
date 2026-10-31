package grpcserver_test

//lint:file-ignore SA1019 the test serves gRPC over h2c, as internal/app does until #448.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/amorey/gochan/watch"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	grpcserver "github.com/kstackhq/kstack/sidecar/grpc"
	"github.com/kstackhq/kstack/sidecar/grpc/authpb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/auth"
)

// fakeAuthSvc is a hand-written auth.Service for the grpc server tests:
// signed-out by default; StartLogin signs in and publishes, Logout signs out and
// publishes, Subscribe returns a latest-value channel (current-on-subscribe then
// changes).
type fakeAuthSvc struct {
	mu       sync.Mutex
	signedIn bool
	identity auth.Identity
	loginAs  auth.Identity

	loginErr  error // when set, StartLogin returns this error without signing in
	logoutErr error // when set, Logout returns this error without signing out

	// When set, Logout closes logoutEntered and blocks until logoutGate closes.
	logoutEntered chan struct{}
	logoutGate    chan struct{}

	hub *watch.Hub[auth.State]
	tx  *watch.Sender[auth.State]
}

func newFakeAuthSvc(loginAs auth.Identity) *fakeAuthSvc {
	return newFakeAuthSvcState(false, auth.Identity{}, loginAs)
}

func signedInFakeAuthSvc(id auth.Identity) *fakeAuthSvc {
	return newFakeAuthSvcState(true, id, id)
}

func newFakeAuthSvcState(signedIn bool, identity, loginAs auth.Identity) *fakeAuthSvc {
	f := &fakeAuthSvc{signedIn: signedIn, identity: identity, loginAs: loginAs}
	f.hub = watch.New(f.stateLocked())
	f.tx = f.hub.Sender()
	return f
}

func (f *fakeAuthSvc) Current(context.Context) (auth.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stateLocked(), nil
}

func (f *fakeAuthSvc) StartLogin(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginErr != nil {
		return f.loginErr
	}
	f.signedIn = true
	f.identity = f.loginAs
	f.publishLocked()
	return nil
}

func (f *fakeAuthSvc) Logout(context.Context) error {
	if f.logoutGate != nil {
		close(f.logoutEntered)
		<-f.logoutGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.logoutErr != nil {
		return f.logoutErr
	}
	f.signedIn = false
	f.identity = auth.Identity{}
	f.publishLocked()
	return nil
}

func (f *fakeAuthSvc) TokenSource(context.Context) oauth2.TokenSource { return nil }

// Subscribe streams session State (current-on-subscribe, then changes) plus a
// cancel func — the same watch hub the real service publishes through.
func (f *fakeAuthSvc) Subscribe() (<-chan auth.State, func()) {
	rx := f.hub.Receiver()
	return rx.Chan(), rx.Close
}

func (f *fakeAuthSvc) stateLocked() auth.State {
	st := auth.State{Authenticated: f.signedIn}
	if f.signedIn {
		id := f.identity
		st.Identity = &id
	}
	return st
}

// publishLocked publishes the current State to subscribers. Caller holds f.mu.
func (f *fakeAuthSvc) publishLocked() {
	f.tx.Send(f.stateLocked()) //nolint:errcheck // Send never blocks; a closed hub is a no-op
}

// newGRPCTestConn stands up an h2c HTTP server over the gRPC server and returns
// a connected client connection. Cleaned up on t.Cleanup.
func newGRPCTestConn(t *testing.T, grpcSrv *grpc.Server) *grpc.ClientConn {
	t.Helper()
	_, conn := serveGRPC(t, grpcSrv)
	return conn
}

// serveGRPC is newGRPCTestConn, also returning the HTTP server for a test that
// drives its shutdown.
func serveGRPC(t *testing.T, grpcSrv *grpc.Server) (*http.Server, *grpc.ClientConn) {
	t.Helper()
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: grpcSrv, Protocols: &protocols}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn
}

// TestAuthStateWatchSnapshotThenDelta verifies the server-streaming behavior:
// the first message is the current state (signed-out on subscribe), and after
// StartLogin the next message delivers the authenticated identity.
func TestAuthStateWatchSnapshotThenDelta(t *testing.T) {
	svc := newFakeAuthSvc(auth.Identity{Email: "ada@example.com", Name: "Ada"})
	grpcSrv := grpcserver.NewServer(context.Background(), svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)

	// First frame: current-on-subscribe = signed out.
	first, err := stream.Recv()
	require.NoError(t, err)
	require.False(t, first.GetAuthenticated())
	require.Nil(t, first.GetIdentity())

	// StartLogin synchronously signs in on the fake; the watch must deliver a delta.
	_, err = client.StartLogin(context.Background(), &authpb.StartLoginRequest{})
	require.NoError(t, err)

	// Second frame: signed in with identity.
	second, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, second.GetAuthenticated())
	require.NotNil(t, second.GetIdentity())
	require.Equal(t, "ada@example.com", second.GetIdentity().GetEmail())
	require.Equal(t, "Ada", second.GetIdentity().GetName())
}

// TestStartLoginAndLogoutRPC verifies the unary RPCs succeed on a functional
// fake and that Logout causes the next watch to start signed-out.
func TestStartLoginAndLogoutRPC(t *testing.T) {
	svc := signedInFakeAuthSvc(auth.Identity{Email: "ada@example.com"})
	grpcSrv := grpcserver.NewServer(context.Background(), svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)

	// StartLogin should succeed (fake does not error by default).
	_, err := client.StartLogin(context.Background(), &authpb.StartLoginRequest{})
	require.NoError(t, err)

	// Logout should succeed.
	_, err = client.Logout(context.Background(), &authpb.LogoutRequest{})
	require.NoError(t, err)

	// A fresh watch must now start signed-out.
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	require.False(t, first.GetAuthenticated())
}

// TestAuthRPCsNilServiceTolerant verifies that passing nil auth to NewServer
// degrades safely: AuthStateWatch ends immediately (clean io.EOF), and the
// unary RPCs return Unavailable — mirroring the GraphQL resolver's nil-Auth
// degrade.
func TestAuthRPCsNilServiceTolerant(t *testing.T) {
	grpcSrv := grpcserver.NewServer(context.Background(), nil, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)

	// AuthStateWatch on a nil service must end immediately with io.EOF.
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	// Unary RPCs must return Unavailable.
	_, err = client.StartLogin(context.Background(), &authpb.StartLoginRequest{})
	st, _ := status.FromError(err)
	require.Equal(t, codes.Unavailable, st.Code())

	_, err = client.Logout(context.Background(), &authpb.LogoutRequest{})
	st, _ = status.FromError(err)
	require.Equal(t, codes.Unavailable, st.Code())
}

// TestAuthStateWatchEndsOnShutdown verifies that ending the streams context ends
// the watch: the handler returns nil (grpc flushes OK trailers), and the client
// sees io.EOF.
func TestAuthStateWatchEndsOnShutdown(t *testing.T) {
	svc := newFakeAuthSvc(auth.Identity{})
	streams, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	grpcSrv := grpcserver.NewServer(streams, svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)

	client := authpb.NewAuthServiceClient(conn)
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	// Consume the initial snapshot so the handler is blocked in its select loop.
	_, err = stream.Recv()
	require.NoError(t, err)

	recvErr := make(chan error, 1)
	go func() {
		_, e := stream.Recv()
		recvErr <- e
	}()

	endStreams()
	require.ErrorIs(t, testutil.Recv(t, recvErr, "the AuthStateWatch stream to end after the streams ended"), io.EOF)

	grpcSrv.Stop()
}

// A subscription the auth service closes ends the watch cleanly: the client sees
// io.EOF.
func TestAuthStateWatchEndsWhenTheSubscriptionCloses(t *testing.T) {
	svc := newFakeAuthSvc(auth.Identity{})
	grpcSrv := grpcserver.NewServer(context.Background(), svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)

	svc.hub.Close()
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

// A frame that cannot be sent ends the watch with the send's error. proto3 refuses to
// marshal a string that is not UTF-8, so such an email fails the first send.
func TestAuthStateWatchEndsOnAFailedSend(t *testing.T) {
	svc := signedInFakeAuthSvc(auth.Identity{Email: "\xff"})
	grpcSrv := grpcserver.NewServer(context.Background(), svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)
	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorContains(t, err, "UTF-8")
}

// HTTP shutdown ends a watch at once but lets an in-flight call finish: the
// AuthStateWatch stream ends with OK trailers while Logout is still running, and
// Shutdown returns only after Logout has answered.
func TestShutdownEndsWatchesAndFinishesCalls(t *testing.T) {
	svc := signedInFakeAuthSvc(auth.Identity{Email: "ada@example.com"})
	svc.logoutEntered = make(chan struct{})
	svc.logoutGate = make(chan struct{})
	streams, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	grpcSrv := grpcserver.NewServer(streams, svc, nil)
	t.Cleanup(grpcSrv.Stop)
	srv, conn := serveGRPC(t, grpcSrv)
	srv.RegisterOnShutdown(endStreams)
	client := authpb.NewAuthServiceClient(conn)

	stream, err := client.AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)

	logoutErr := make(chan error, 1)
	go func() {
		_, err := client.Logout(context.Background(), &authpb.LogoutRequest{})
		logoutErr <- err
	}()
	testutil.Wait(t, svc.logoutEntered, "Logout to start")

	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- srv.Shutdown(context.Background()) }()

	watchErr := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		watchErr <- err
	}()
	require.ErrorIs(t, testutil.Recv(t, watchErr, "the watch to end"), io.EOF)
	select {
	case err := <-shutdownErr:
		t.Fatalf("Shutdown returned %v with Logout still running", err)
	default:
	}

	close(svc.logoutGate)
	require.NoError(t, testutil.Recv(t, logoutErr, "Logout to answer"))
	require.NoError(t, testutil.Recv(t, shutdownErr, "Shutdown to return"))
}

// An auth service that fails reports Internal, not a silent success: the tray
// menu drives these RPCs, and a swallowed error would leave it showing the
// wrong session.
func TestAuthUnaryRPCsSurfaceServiceFailures(t *testing.T) {
	svc := newFakeAuthSvc(auth.Identity{})
	svc.loginErr = errors.New("loopback bind failed")
	svc.logoutErr = errors.New("keyring locked")
	grpcSrv := grpcserver.NewServer(context.Background(), svc, nil)
	conn := newGRPCTestConn(t, grpcSrv)
	t.Cleanup(grpcSrv.Stop)

	client := authpb.NewAuthServiceClient(conn)

	_, err := client.StartLogin(context.Background(), &authpb.StartLoginRequest{})
	st, _ := status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "loopback bind failed")

	_, err = client.Logout(context.Background(), &authpb.LogoutRequest{})
	st, _ = status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "keyring locked")
}
