package routeros

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-routeros/routeros/v3"
	"github.com/go-routeros/routeros/v3/proto"
)

func TestRouterOSAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		port    int32
		want    string
	}{
		{
			name:    "IPv4",
			address: "192.0.2.1",
			port:    8728,
			want:    "192.0.2.1:8728",
		},
		{
			name:    "IPv6",
			address: "2001:db8::1",
			port:    8729,
			want:    "[2001:db8::1]:8729",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := routerOSAddress(test.address, test.port); got != test.want {
				t.Fatalf("routerOSAddress(%q, %d) = %q, want %q", test.address, test.port, got, test.want)
			}
		})
	}
}

func TestDial_TimesOutStalledLoginWithoutCallerDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})

	accepted := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		close(accepted)
		_, copyErr := io.Copy(io.Discard, conn)
		serverDone <- errors.Join(copyErr, conn.Close())
	}()

	ctx := context.Background()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("test context unexpectedly has a deadline")
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address has type %T, want *net.TCPAddr", listener.Addr())
	}
	client, err := dial(
		ctx,
		"127.0.0.1",
		int32(tcpAddress.Port),
		false,
		"user",
		"password",
		50*time.Millisecond,
	)
	if client != nil {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("close unexpected client: %v", closeErr)
		}
		t.Fatal("Dial() returned a client after login cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial() error = %v, want context deadline exceeded", err)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("server did not accept the RouterOS connection")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("fake RouterOS server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RouterOS connection was not closed after login cancellation")
	}
}

func TestDial_TimesOutStalledTLSHandshakeWithoutCallerDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})

	accepted := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		close(accepted)
		_, copyErr := io.Copy(io.Discard, conn)
		serverDone <- errors.Join(copyErr, conn.Close())
	}()

	ctx := context.Background()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("test context unexpectedly has a deadline")
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address has type %T, want *net.TCPAddr", listener.Addr())
	}
	client, err := dial(
		ctx,
		"127.0.0.1",
		int32(tcpAddress.Port),
		true,
		"user",
		"password",
		50*time.Millisecond,
	)
	if client != nil {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("close unexpected client: %v", closeErr)
		}
		t.Fatal("dial() returned a client after TLS handshake timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial() error = %v, want context deadline exceeded", err)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("server did not accept the TLS connection")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("fake TLS server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TLS connection was not closed after handshake timeout")
	}
}

func TestAPIClient_CommandTimeoutInterruptsStalledWrite(t *testing.T) {
	api, _ := newPipeAPIClient(t, 50*time.Millisecond)
	ctx := context.Background()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("test context unexpectedly has a deadline")
	}

	_, err := api.runContext(ctx, "/system/identity/print")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runContext() error = %v, want context deadline exceeded", err)
	}
}

func TestAPIClient_CommandCancellationInterruptsStalledRead(t *testing.T) {
	api, serverConn := newPipeAPIClient(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("test context unexpectedly has a deadline")
	}

	result := make(chan error, 1)
	go func() {
		_, err := api.runContext(ctx, "/system/identity/print")
		result <- err
	}()

	request := make([]byte, 1024)
	if _, err := serverConn.Read(request); err != nil {
		t.Fatalf("read RouterOS request: %v", err)
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runContext() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled RouterOS read did not stop after context cancellation")
	}
}

func newPipeAPIClient(t *testing.T, operationTimeout time.Duration) (*apiClient, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	client, err := routeros.NewClient(clientConn)
	if err != nil {
		t.Fatalf("create RouterOS client: %v", err)
	}
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	api := &apiClient{
		c:                client,
		conn:             clientConn,
		cancelSession:    cancelSession,
		asyncDone:        client.AsyncContext(sessionCtx),
		operationTimeout: operationTimeout,
	}
	t.Cleanup(func() {
		if err := api.Close(); err != nil {
			t.Errorf("close RouterOS client: %v", err)
		}
		if err := serverConn.Close(); err != nil {
			t.Errorf("close fake RouterOS server: %v", err)
		}
	})
	return api, serverConn
}

func TestAPIClient_CloseIsConcurrentAndIdempotent(t *testing.T) {
	const (
		iterations = 100
		callers    = 16
	)

	for range iterations {
		clientConn, serverConn := net.Pipe()
		client, err := routeros.NewClient(clientConn)
		if err != nil {
			t.Fatalf("create RouterOS client: %v", err)
		}
		sessionCtx, cancelSession := context.WithCancel(context.Background())
		api := &apiClient{
			c:             client,
			cancelSession: cancelSession,
			asyncDone:     client.AsyncContext(sessionCtx),
		}

		start := make(chan struct{})
		errors := make(chan error, callers)
		var waitGroup sync.WaitGroup
		waitGroup.Add(callers)
		for range callers {
			go func() {
				defer waitGroup.Done()
				<-start
				errors <- api.Close()
			}()
		}
		close(start)
		waitGroup.Wait()
		close(errors)

		for err := range errors {
			if err != nil {
				t.Fatalf("Close() error = %v, want nil", err)
			}
		}
		if err := api.Close(); err != nil {
			t.Fatalf("repeated Close() error = %v, want nil", err)
		}
		if err := serverConn.Close(); err != nil {
			t.Fatalf("close server connection: %v", err)
		}
	}
}

func TestManagedComment(t *testing.T) {
	got := ManagedComment("dns", "web", "apps")
	want := "managed-by=mikrotik-operator/dns/apps/web"
	if got != want {
		t.Fatalf("ManagedComment() = %q, want %q", got, want)
	}
}

func TestManagedCommentPrefixMatchesResourceBoundaries(t *testing.T) {
	prefix := ManagedComment("route", "web", "apps")
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "exact resource", value: prefix, want: true},
		{name: "child entry", value: prefix + "/gateway", want: true},
		{name: "similar resource name", value: ManagedComment("route", "web2", "apps"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := managedCommentPrefixMatches(test.value, prefix); got != test.want {
				t.Fatalf("managedCommentPrefixMatches(%q, %q) = %t, want %t", test.value, prefix, got, test.want)
			}
		})
	}
}

func TestIsManagedComment(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "operator root", value: managedCommentPrefix, want: true},
		{name: "operator resource", value: ManagedComment("route", "web", "apps"), want: true},
		{name: "user comment", value: "managed-by=mikrotik-operator-copy", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isManagedComment(test.value); got != test.want {
				t.Fatalf("isManagedComment(%q) = %t, want %t", test.value, got, test.want)
			}
		})
	}
}

func TestEnsureFirewallRule_PropagatesOrderingScanErrorWithoutMutation(t *testing.T) {
	orderingErr := errors.New("ordering scan unavailable")
	comment := ManagedComment("firewall", "web", "apps")
	client := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{
				reply: &routeros.Reply{
					Re: []*proto.Sentence{
						{
							Map: map[string]string{
								".id":     "*1",
								"chain":   "forward",
								"action":  "accept",
								"comment": comment,
							},
						},
					},
				},
			},
			{err: orderingErr},
		},
	}
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		if err := clientConn.Close(); err != nil {
			t.Errorf("close client connection: %v", err)
		}
		if err := serverConn.Close(); err != nil {
			t.Errorf("close server connection: %v", err)
		}
	})
	done := make(chan error)
	close(done)
	api := &apiClient{
		c:                client,
		conn:             clientConn,
		cancelSession:    func() {},
		asyncDone:        done,
		operationTimeout: time.Second,
	}

	err := api.EnsureFirewallRule(
		context.Background(),
		FirewallRule{Chain: "forward", Action: "accept", PlaceBefore: true},
		comment,
	)
	if !errors.Is(err, orderingErr) {
		t.Fatalf("EnsureFirewallRule() error = %v, want %v", err, orderingErr)
	}
	if len(client.calls) != 2 {
		t.Fatalf("RouterOS command count = %d, want 2 print commands", len(client.calls))
	}
	for _, call := range client.calls {
		if len(call) == 0 || call[0] != "/ip/firewall/filter/print" {
			t.Fatalf("unexpected mutating RouterOS command after ordering error: %v", call)
		}
	}
}

type scriptedRouterOSResponse struct {
	reply *routeros.Reply
	err   error
}

type scriptedRouterOSClient struct {
	calls     [][]string
	responses []scriptedRouterOSResponse
}

func (s *scriptedRouterOSClient) RunArgsContext(_ context.Context, sentences []string) (*routeros.Reply, error) {
	s.calls = append(s.calls, append([]string{}, sentences...))
	if len(s.responses) == 0 {
		return nil, fmt.Errorf("unexpected RouterOS command: %v", sentences)
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response.reply, response.err
}

func (s *scriptedRouterOSClient) Close() error {
	return nil
}

func TestEnsurePortForward_OmitsPlaceBeforeOnEmptyTable(t *testing.T) {
	comment := ManagedComment("portforward", "web", "apps")
	empty := &routeros.Reply{}
	client := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: empty},
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{}},
		},
	}
	api := newScriptedAPIClient(t, client)

	err := api.EnsurePortForward(context.Background(), PortForward{
		Protocol:     "tcp",
		ExternalPort: 80,
		Target:       "10.0.0.10",
		TargetPort:   80,
		PublicIP:     "198.51.100.10",
	}, comment)
	if err != nil {
		t.Fatalf("EnsurePortForward() error = %v", err)
	}
	if len(client.calls) != 5 {
		t.Fatalf("RouterOS command count = %d, want 5", len(client.calls))
	}
	for _, call := range client.calls {
		if len(call) > 0 && strings.HasSuffix(call[0], "/add") {
			for _, arg := range call {
				if strings.HasPrefix(arg, "=place-before=") {
					t.Fatalf("empty NAT table used %s", arg)
				}
			}
		}
	}
}

func TestEnsurePortForward_PlacesBeforeExistingChainID(t *testing.T) {
	comment := ManagedComment("portforward", "web", "apps")
	empty := &routeros.Reply{}
	existing := &routeros.Reply{Re: []*proto.Sentence{
		{Map: map[string]string{".id": "*3", "chain": "dstnat"}},
		{Map: map[string]string{".id": "*5", "chain": "srcnat"}},
	}}
	client := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: existing},
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{}},
		},
	}
	api := newScriptedAPIClient(t, client)

	err := api.EnsurePortForward(context.Background(), PortForward{
		Protocol:     "tcp",
		ExternalPort: 80,
		Target:       "10.0.0.10",
		TargetPort:   80,
		PublicIP:     "198.51.100.10",
	}, comment)
	if err != nil {
		t.Fatalf("EnsurePortForward() error = %v", err)
	}
	wantPlaceBefore := map[string]string{
		"dstnat": "=place-before=*3",
		"srcnat": "=place-before=*5",
	}
	found := 0
	for _, call := range client.calls {
		if len(call) == 0 || call[0] != "/ip/firewall/nat/add" {
			continue
		}
		chain := ""
		got := ""
		for _, arg := range call {
			switch {
			case strings.HasPrefix(arg, "=chain="):
				chain = strings.TrimPrefix(arg, "=chain=")
			case strings.HasPrefix(arg, "=place-before="):
				got = arg
			}
		}
		if want := wantPlaceBefore[chain]; got != want {
			t.Fatalf("NAT %s add place-before = %q, want %q: %v", chain, got, want, call)
		}
		found++
	}
	if found != 2 {
		t.Fatalf("placed NAT adds = %d, want 2", found)
	}
}

func TestEnsurePortForward_SetsDstAddressOnDstNat(t *testing.T) {
	comment := ManagedComment("portforward", "web", "apps")
	empty := &routeros.Reply{}
	client := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: empty},
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{}},
		},
	}
	api := newScriptedAPIClient(t, client)

	err := api.EnsurePortForward(context.Background(), PortForward{
		Protocol:     "tcp",
		ExternalPort: 443,
		Target:       "10.0.0.10",
		TargetPort:   8443,
		PublicIP:     "203.0.113.10",
	}, comment)
	if err != nil {
		t.Fatalf("EnsurePortForward() error = %v", err)
	}
	found := false
	for _, call := range client.calls {
		if len(call) == 0 || call[0] != "/ip/firewall/nat/add" {
			continue
		}
		chain := ""
		dstAddress := ""
		for _, arg := range call {
			switch {
			case strings.HasPrefix(arg, "=chain="):
				chain = strings.TrimPrefix(arg, "=chain=")
			case strings.HasPrefix(arg, "=dst-address="):
				dstAddress = strings.TrimPrefix(arg, "=dst-address=")
			}
		}
		if chain != "dstnat" {
			continue
		}
		found = true
		if dstAddress != "203.0.113.10" {
			t.Fatalf("dst-nat dst-address = %q, want 203.0.113.10: %v", dstAddress, call)
		}
	}
	if !found {
		t.Fatal("dst-nat add was not issued")
	}
}

func TestEnsurePortForward_OmitsDstAddressWhenEmpty(t *testing.T) {
	comment := ManagedComment("portforward", "web", "apps")
	empty := &routeros.Reply{}
	client := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: empty},
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{}},
		},
	}
	api := newScriptedAPIClient(t, client)

	err := api.EnsurePortForward(context.Background(), PortForward{
		Protocol:     "tcp",
		ExternalPort: 443,
		Target:       "10.0.0.10",
		TargetPort:   8443,
	}, comment)
	if err != nil {
		t.Fatalf("EnsurePortForward() error = %v", err)
	}
	for _, call := range client.calls {
		if len(call) == 0 || call[0] != "/ip/firewall/nat/add" {
			continue
		}
		chain := ""
		for _, arg := range call {
			if strings.HasPrefix(arg, "=chain=") {
				chain = strings.TrimPrefix(arg, "=chain=")
			}
			if chain == "dstnat" && strings.HasPrefix(arg, "=dst-address=") {
				t.Fatalf("empty destination still set %s", arg)
			}
		}
	}
}

func newScriptedAPIClient(t *testing.T, scripted *scriptedRouterOSClient) *apiClient {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		if err := clientConn.Close(); err != nil {
			t.Errorf("close client connection: %v", err)
		}
		if err := serverConn.Close(); err != nil {
			t.Errorf("close server connection: %v", err)
		}
	})
	done := make(chan error)
	close(done)
	return &apiClient{
		c:                scripted,
		conn:             clientConn,
		cancelSession:    func() {},
		asyncDone:        done,
		operationTimeout: time.Second,
	}
}

func TestFirewallRulePrecedesUnmanaged(t *testing.T) {
	comment := ManagedComment("firewall", "web", "apps")
	tests := []struct {
		name    string
		id      string
		entries []*proto.Sentence
		want    bool
	}{
		{
			name: "ignores unmanaged rules in other chains",
			id:   "*2",
			entries: []*proto.Sentence{
				{Map: map[string]string{".id": "*1", "chain": "input", "comment": "defconf"}},
				{Map: map[string]string{".id": "*2", "chain": "forward", "comment": comment}},
			},
			want: true,
		},
		{
			name: "detects unmanaged rules earlier in the same chain",
			id:   "*2",
			entries: []*proto.Sentence{
				{Map: map[string]string{".id": "*1", "chain": "forward", "comment": "drop"}},
				{Map: map[string]string{".id": "*2", "chain": "forward", "comment": comment}},
			},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedRouterOSClient{
				responses: []scriptedRouterOSResponse{
					{reply: &routeros.Reply{Re: test.entries}},
				},
			}
			ok, err := newScriptedAPIClient(t, client).firewallRulePrecedesUnmanaged(context.Background(), test.id)
			if err != nil {
				t.Fatalf("firewallRulePrecedesUnmanaged() error = %v", err)
			}
			if ok != test.want {
				t.Fatalf("firewallRulePrecedesUnmanaged() = %t, want %t", ok, test.want)
			}
		})
	}
}

func TestFirewallRuleMatches(t *testing.T) {
	rule := FirewallRule{
		Chain:              "forward",
		Action:             "accept",
		Protocol:           "tcp",
		DestinationAddress: "10.0.0.10",
		DestinationPort:    "80",
		ConnectionState:    []string{"established", "related"},
	}
	entry := map[string]string{
		"chain":            "forward",
		"action":           "accept",
		"protocol":         "tcp",
		"dst-address":      "10.0.0.10",
		"dst-port":         "80",
		"connection-state": "established,related",
	}
	if !firewallRuleMatches(entry, rule) {
		t.Fatal("firewallRuleMatches() returned false for an equivalent rule")
	}
	entry["dst-address"] = "10.0.0.11"
	if firewallRuleMatches(entry, rule) {
		t.Fatal("firewallRuleMatches() returned true for a different rule")
	}
}
