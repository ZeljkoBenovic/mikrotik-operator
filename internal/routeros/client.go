package routeros

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-routeros/routeros/v3"
)

type Client interface {
	EnsureDNS(context.Context, string, string, string, string) error
	DeleteDNS(context.Context, string) error
	EnsurePortForward(context.Context, PortForward, string) error
	DeletePortForward(context.Context, string) error
	EnsureRoute(context.Context, string, string, string) error
	EnsureRouteWithDistance(context.Context, string, string, int32, string) error
	EnsureRoutes(context.Context, string, []string, string) error
	DeleteRoute(context.Context, string) error
	DeleteRoutesByPrefix(context.Context, string) error
	EnsureFirewallRule(context.Context, FirewallRule, string) error
	DeleteFirewallRule(context.Context, string) error
	Close() error
}
type FirewallRule struct {
	Chain              string
	Action             string
	Protocol           string
	SourceAddress      string
	DestinationAddress string
	SourcePort         string
	DestinationPort    string
	InInterface        string
	OutInterface       string
	ConnectionState    []string
	ConnectionNatState []string
	LogPrefix          string
	PlaceBefore        bool
}

type PortForward struct {
	Protocol     string
	ExternalPort int32
	Target       string
	TargetPort   int32
	// PublicIP is the dst-nat dst-address (the IP that initially receives traffic).
	PublicIP string
}
type Factory func(context.Context, string, int32, bool, string, string) (Client, error)

type routerOSClient interface {
	RunArgsContext(context.Context, []string) (*routeros.Reply, error)
	Close() error
}

type apiClient struct {
	c                routerOSClient
	conn             net.Conn
	cancelSession    context.CancelFunc
	asyncDone        <-chan error
	operationMu      sync.Mutex
	operationTimeout time.Duration
	closeOnce        sync.Once
	closeErr         error
}

const (
	managedCommentPrefix     = "managed-by=mikrotik-operator"
	routerOSOperationTimeout = 15 * time.Second
)

func Dial(ctx context.Context, address string, port int32, useTLS bool, username, password string) (Client, error) {
	return dial(ctx, address, port, useTLS, username, password, routerOSOperationTimeout)
}

func dial(
	ctx context.Context,
	address string,
	port int32,
	useTLS bool,
	username string,
	password string,
	operationTimeout time.Duration,
) (Client, error) {
	if port == 0 {
		if useTLS {
			port = 8729
		} else {
			port = 8728
		}
	}
	target := routerOSAddress(address, port)
	dialCtx, cancelDial := context.WithTimeout(ctx, operationTimeout)
	defer cancelDial()

	var conn net.Conn
	var err error
	if useTLS {
		conn, err = (&tls.Dialer{}).DialContext(dialCtx, "tcp", target)
	} else {
		conn, err = new(net.Dialer).DialContext(dialCtx, "tcp", target)
	}
	if err != nil {
		if conn != nil {
			if closeErr := conn.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close partial RouterOS connection: %w", closeErr))
			}
		}
		return nil, fmt.Errorf("dial RouterOS at %q: %w", target, err)
	}
	cancelDial()

	c, err := routeros.NewClient(conn)
	if err != nil {
		return nil, fmt.Errorf("create RouterOS client: %w; close: %w", err, conn.Close())
	}

	// go-routeros only observes request contexts in asynchronous mode. Start it
	// before logging in so a stalled /login can be canceled. The session context
	// belongs to this short-lived client, not to a reconciliation invocation.
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	asyncDone := c.AsyncContext(sessionCtx)
	loginErr := runRouterOSOperation(ctx, conn, operationTimeout, func(operationCtx context.Context) error {
		return c.LoginContext(operationCtx, username, password)
	})
	if loginErr != nil {
		closeErr := c.Close()
		cancelSession()
		<-asyncDone
		return nil, fmt.Errorf("login to RouterOS at %q: %w; close: %v", target, loginErr, closeErr)
	}

	return &apiClient{
		c:                c,
		conn:             conn,
		cancelSession:    cancelSession,
		asyncDone:        asyncDone,
		operationTimeout: operationTimeout,
	}, nil
}

func routerOSAddress(address string, port int32) string {
	return net.JoinHostPort(address, fmt.Sprintf("%d", port))
}

func (a *apiClient) Close() error {
	a.closeOnce.Do(func() {
		a.operationMu.Lock()
		defer a.operationMu.Unlock()

		// Close the go-routeros reader before canceling its context waiter.
		// In v3.0.1, Cancel sends to the same channel that Close closes, so
		// reversing this order can race and panic with send on closed channel.
		a.closeErr = a.c.Close()
		a.cancelSession()
		<-a.asyncDone
	})
	return a.closeErr
}

func (a *apiClient) runContext(ctx context.Context, sentences ...string) (*routeros.Reply, error) {
	return a.runArgsContext(ctx, sentences)
}

func (a *apiClient) runArgsContext(ctx context.Context, sentences []string) (*routeros.Reply, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()

	timeout := a.operationTimeout
	if timeout <= 0 {
		timeout = routerOSOperationTimeout
	}
	var reply *routeros.Reply
	err := runRouterOSOperation(ctx, a.conn, timeout, func(operationCtx context.Context) error {
		var err error
		reply, err = a.c.RunArgsContext(operationCtx, sentences)
		return err
	})
	return reply, err
}

func runRouterOSOperation(
	ctx context.Context,
	conn net.Conn,
	timeout time.Duration,
	operation func(context.Context) error,
) error {
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	interruptResult := make(chan error, 1)
	stopInterrupt := context.AfterFunc(operationCtx, func() {
		interruptErr := conn.SetDeadline(time.Now())
		if interruptErr != nil {
			interruptErr = errors.Join(interruptErr, conn.Close())
		}
		interruptResult <- interruptErr
	})

	operationErr := operation(operationCtx)
	var interruptErr error
	if !stopInterrupt() {
		interruptErr = <-interruptResult
	}
	resetErr := conn.SetDeadline(time.Time{})

	if contextErr := operationCtx.Err(); contextErr != nil {
		result := []error{fmt.Errorf("routeros operation: %w", contextErr)}
		if interruptErr != nil {
			result = append(result, fmt.Errorf("interrupt routeros I/O: %w", interruptErr))
		}
		if resetErr != nil {
			result = append(result, fmt.Errorf("reset routeros I/O deadline: %w", resetErr))
		}
		return errors.Join(result...)
	}
	if operationErr != nil {
		if resetErr != nil {
			return errors.Join(operationErr, fmt.Errorf("reset routeros I/O deadline: %w", resetErr))
		}
		return operationErr
	}
	if interruptErr != nil {
		return fmt.Errorf("interrupt routeros I/O: %w", interruptErr)
	}
	if resetErr != nil {
		return fmt.Errorf("reset routeros I/O deadline: %w", resetErr)
	}
	return nil
}
func (a *apiClient) EnsureDNS(
	ctx context.Context,
	name, address, ttl, comment string,
) error {
	entries, err := a.managedEntries(
		ctx,
		"/ip/dns/static/print",
		func(value string) bool { return value == comment },
		"name", "address", "ttl", "comment",
	)
	if err != nil {
		return err
	}
	if len(entries) == 1 && entries[0]["name"] == name && entries[0]["address"] == address && (ttl == "" || entries[0]["ttl"] == ttl) {
		return nil
	}
	if err := a.DeleteDNS(ctx, comment); err != nil {
		return err
	}
	args := []string{"/ip/dns/static/add", "=name=" + name, "=address=" + address, "=comment=" + comment}
	if ttl != "" {
		args = append(args, "=ttl="+ttl)
	}
	_, err = a.runArgsContext(ctx, args)
	return err
}
func (a *apiClient) DeleteDNS(ctx context.Context, comment string) error {
	return a.deleteByComment(ctx, "/ip/dns/static/print", "/ip/dns/static/remove", comment)
}
func (a *apiClient) EnsurePortForward(ctx context.Context, forward PortForward, comment string) error {
	entries, err := a.managedEntries(
		ctx,
		"/ip/firewall/nat/print",
		func(value string) bool { return managedCommentPrefixMatches(value, comment) },
		"chain", "protocol", "dst-port", "action", "to-addresses", "to-ports", "dst-address", "comment",
	)
	if err != nil {
		return err
	}
	dstComment := comment + "/dstnat"
	srcComment := comment + "/srcnat"
	dstOK := false
	srcOK := false
	for _, entry := range entries {
		switch entry["comment"] {
		case dstComment:
			dstOK = entry["chain"] == "dstnat" &&
				entry["protocol"] == forward.Protocol &&
				entry["dst-port"] == fmt.Sprintf("%d", forward.ExternalPort) &&
				entry["action"] == "dst-nat" &&
				entry["to-addresses"] == forward.Target &&
				entry["to-ports"] == fmt.Sprintf("%d", forward.TargetPort) &&
				entry["dst-address"] == forward.PublicIP
		case srcComment:
			srcOK = entry["chain"] == "srcnat" && entry["dst-address"] == forward.Target && entry["action"] == "masquerade"
		}
	}
	if dstOK && srcOK && len(entries) == 2 {
		return nil
	}
	if err := a.DeletePortForward(ctx, comment); err != nil {
		return err
	}
	firstIDs, err := a.firstIDsByChain(ctx, "/ip/firewall/nat/print")
	if err != nil {
		return err
	}
	args := []string{
		"/ip/firewall/nat/add",
		"=chain=dstnat",
		"=protocol=" + forward.Protocol,
		fmt.Sprintf("=dst-port=%d", forward.ExternalPort),
		"=action=dst-nat",
		"=to-addresses=" + forward.Target,
		fmt.Sprintf("=to-ports=%d", forward.TargetPort),
		"=comment=" + comment + "/dstnat",
	}
	if forward.PublicIP != "" {
		args = append(args, "=dst-address="+forward.PublicIP)
	}
	if placeBefore := placeBeforeArg("dstnat", firstIDs); placeBefore != "" {
		args = append(args, placeBefore)
	}
	if _, err := a.runArgsContext(ctx, args); err != nil {
		return err
	}
	srcArgs := []string{
		"/ip/firewall/nat/add",
		"=chain=srcnat",
		"=dst-address=" + forward.Target,
		"=action=masquerade",
		"=comment=" + comment + "/srcnat",
	}
	if placeBefore := placeBeforeArg("srcnat", firstIDs); placeBefore != "" {
		srcArgs = append(srcArgs, placeBefore)
	}
	_, err = a.runArgsContext(ctx, srcArgs)
	return err
}

func (a *apiClient) DeletePortForward(ctx context.Context, comment string) error {
	return a.deleteByCommentPrefix(ctx, "/ip/firewall/nat/print", "/ip/firewall/nat/remove", comment)
}

func (a *apiClient) EnsureFirewallRule(ctx context.Context, rule FirewallRule, comment string) error {
	entries, err := a.managedEntries(
		ctx,
		"/ip/firewall/filter/print",
		func(value string) bool { return value == comment },
		"chain", "action", "protocol", "src-address", "dst-address",
		"src-port", "dst-port", "in-interface", "out-interface",
		"connection-state", "connection-nat-state", "log-prefix", "comment", ".id",
	)
	if err != nil {
		return err
	}
	if len(entries) == 1 && firewallRuleMatches(entries[0], rule) {
		if !rule.PlaceBefore {
			return nil
		}
		precedesUnmanaged, err := a.firewallRulePrecedesUnmanaged(ctx, entries[0][".id"])
		if err != nil {
			return err
		}
		if precedesUnmanaged {
			return nil
		}
	}
	if err := a.DeleteFirewallRule(ctx, comment); err != nil {
		return err
	}
	args := []string{"/ip/firewall/filter/add", "=chain=" + rule.Chain, "=action=" + rule.Action, "=comment=" + comment}
	if rule.Protocol != "" {
		args = append(args, "=protocol="+rule.Protocol)
	}
	if rule.SourceAddress != "" {
		args = append(args, "=src-address="+rule.SourceAddress)
	}
	if rule.DestinationAddress != "" {
		args = append(args, "=dst-address="+rule.DestinationAddress)
	}
	if rule.SourcePort != "" {
		args = append(args, "=src-port="+rule.SourcePort)
	}
	if rule.DestinationPort != "" {
		args = append(args, "=dst-port="+rule.DestinationPort)
	}
	if rule.InInterface != "" {
		args = append(args, "=in-interface="+rule.InInterface)
	}
	if rule.OutInterface != "" {
		args = append(args, "=out-interface="+rule.OutInterface)
	}
	if len(rule.ConnectionState) > 0 {
		args = append(args, "=connection-state="+strings.Join(rule.ConnectionState, ","))
	}
	if len(rule.ConnectionNatState) > 0 {
		args = append(args, "=connection-nat-state="+strings.Join(rule.ConnectionNatState, ","))
	}
	if rule.LogPrefix != "" {
		args = append(args, "=log-prefix="+rule.LogPrefix)
	}
	if rule.PlaceBefore {
		firstIDs, err := a.firstIDsByChain(ctx, "/ip/firewall/filter/print")
		if err != nil {
			return err
		}
		if placeBefore := placeBeforeArg(rule.Chain, firstIDs); placeBefore != "" {
			args = append(args, placeBefore)
		}
	}
	_, err = a.runArgsContext(ctx, args)
	return err
}

func (a *apiClient) firstIDsByChain(ctx context.Context, printCmd string) (map[string]string, error) {
	entries, err := a.managedEntries(ctx, printCmd, func(string) bool { return true }, ".id", "chain")
	if err != nil {
		return nil, err
	}
	firstIDs := make(map[string]string)
	for _, entry := range entries {
		chain := entry["chain"]
		id := entry[".id"]
		if chain == "" || id == "" {
			continue
		}
		if _, exists := firstIDs[chain]; exists {
			continue
		}
		firstIDs[chain] = id
	}
	return firstIDs, nil
}

func placeBeforeArg(chain string, firstIDs map[string]string) string {
	id := firstIDs[chain]
	if id == "" {
		return ""
	}
	return "=place-before=" + id
}

func (a *apiClient) firewallRulePrecedesUnmanaged(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	entries, err := a.managedEntries(ctx, "/ip/firewall/filter/print", func(string) bool { return true }, ".id", "chain", "comment")
	if err != nil {
		return false, err
	}
	chain := ""
	for _, entry := range entries {
		if entry[".id"] == id {
			chain = entry["chain"]
			break
		}
	}
	if chain == "" {
		return false, nil
	}
	for _, entry := range entries {
		if entry["chain"] != chain {
			continue
		}
		if entry[".id"] == id {
			return true, nil
		}
		if !isManagedComment(entry["comment"]) {
			return false, nil
		}
	}
	return false, nil
}
func (a *apiClient) DeleteFirewallRule(ctx context.Context, comment string) error {
	return a.deleteByCommentPrefix(ctx, "/ip/firewall/filter/print", "/ip/firewall/filter/remove", comment)
}

func (a *apiClient) DeleteManagedConfiguration(ctx context.Context) error {
	for _, commands := range [][2]string{
		{"/ip/dns/static/print", "/ip/dns/static/remove"},
		{"/ip/firewall/nat/print", "/ip/firewall/nat/remove"},
		{"/ip/firewall/filter/print", "/ip/firewall/filter/remove"},
		{"/ip/route/print", "/ip/route/remove"},
	} {
		if err := a.deleteMatching(ctx, commands[0], commands[1], func(value string) bool {
			return value == managedCommentPrefix || strings.HasPrefix(value, managedCommentPrefix+"/")
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *apiClient) EnsureRoute(ctx context.Context, destination, gateway, comment string) error {
	return a.ensureRoute(ctx, destination, gateway, 0, comment)
}
func (a *apiClient) EnsureRouteWithDistance(ctx context.Context, destination, gateway string, distance int32, comment string) error {
	return a.ensureRoute(ctx, destination, gateway, distance, comment)
}
func (a *apiClient) ensureRoute(ctx context.Context, destination, gateway string, distance int32, comment string) error {
	entries, err := a.managedEntries(ctx, "/ip/route/print", func(value string) bool { return value == comment }, "dst-address", "gateway", "distance", "comment")
	if err != nil {
		return err
	}
	if len(entries) == 1 && entries[0]["dst-address"] == destination && entries[0]["gateway"] == gateway && (distance == 0 || entries[0]["distance"] == fmt.Sprintf("%d", distance)) {
		return nil
	}
	if err := a.DeleteRoute(ctx, comment); err != nil {
		return err
	}
	args := []string{"/ip/route/add", "=dst-address=" + destination, "=gateway=" + gateway, "=comment=" + comment}
	if distance > 0 {
		args = append(args, fmt.Sprintf("=distance=%d", distance))
	}
	_, err = a.runArgsContext(ctx, args)
	return err
}

func (a *apiClient) EnsureRoutes(ctx context.Context, destination string, gateways []string, prefix string) error {
	desired := make(map[string]struct{}, len(gateways))
	for _, gateway := range gateways {
		desired[prefix+"/"+gateway] = struct{}{}
	}
	entries, err := a.managedEntries(ctx, "/ip/route/print", func(value string) bool { return managedCommentPrefixMatches(value, prefix) }, ".id", "comment")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, ok := desired[entry["comment"]]; !ok {
			if _, err := a.runContext(ctx, "/ip/route/remove", "=.id="+entry[".id"]); err != nil {
				return err
			}
		}
	}
	for _, gateway := range gateways {
		if err := a.EnsureRoute(ctx, destination, gateway, prefix+"/"+gateway); err != nil {
			return err
		}
	}
	return nil
}

func (a *apiClient) DeleteRoute(ctx context.Context, comment string) error {
	return a.deleteByComment(ctx, "/ip/route/print", "/ip/route/remove", comment)
}
func (a *apiClient) DeleteRoutesByPrefix(ctx context.Context, prefix string) error {
	return a.deleteByCommentPrefix(ctx, "/ip/route/print", "/ip/route/remove", prefix)
}
func (a *apiClient) deleteByComment(ctx context.Context, printCmd, removeCmd, comment string) error {
	return a.deleteMatching(ctx, printCmd, removeCmd, func(value string) bool { return value == comment })
}
func (a *apiClient) deleteByCommentPrefix(ctx context.Context, printCmd, removeCmd, prefix string) error {
	return a.deleteMatching(ctx, printCmd, removeCmd, func(value string) bool { return managedCommentPrefixMatches(value, prefix) })
}
func (a *apiClient) deleteMatching(ctx context.Context, printCmd, removeCmd string, matches func(string) bool) error {
	entries, err := a.managedEntries(ctx, printCmd, matches, ".id", "comment")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err = a.runContext(ctx, removeCmd, "=.id="+entry[".id"]); err != nil {
			return err
		}
	}
	return nil
}

func (a *apiClient) managedEntries(ctx context.Context, printCmd string, matches func(string) bool, properties ...string) ([]map[string]string, error) {
	proplist := strings.Join(properties, ",")
	r, err := a.runContext(ctx, printCmd, "=.proplist="+proplist)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]string, 0)
	for _, reply := range r.Re {
		if matches(reply.Map["comment"]) {
			entries = append(entries, reply.Map)
		}
	}
	return entries, nil
}

func firewallRuleMatches(entry map[string]string, rule FirewallRule) bool {
	return entry["chain"] == rule.Chain &&
		entry["action"] == rule.Action &&
		entry["protocol"] == rule.Protocol &&
		entry["src-address"] == rule.SourceAddress &&
		entry["dst-address"] == rule.DestinationAddress &&
		entry["src-port"] == rule.SourcePort &&
		entry["dst-port"] == rule.DestinationPort &&
		entry["in-interface"] == rule.InInterface &&
		entry["out-interface"] == rule.OutInterface &&
		entry["connection-state"] == strings.Join(rule.ConnectionState, ",") &&
		entry["connection-nat-state"] == strings.Join(rule.ConnectionNatState, ",") &&
		entry["log-prefix"] == rule.LogPrefix
}

func ManagedComment(kind, name, namespace string) string {
	return strings.Join([]string{"managed-by=mikrotik-operator", kind, namespace, name}, "/")
}

func managedCommentPrefixMatches(value, prefix string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func isManagedComment(value string) bool {
	return value == managedCommentPrefix || strings.HasPrefix(value, managedCommentPrefix+"/")
}
