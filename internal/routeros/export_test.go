package routeros

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-routeros/routeros/v3"
	"github.com/go-routeros/routeros/v3/proto"
)

func TestExportText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply *routeros.Reply
		want  string
	}{
		{name: "nil", reply: nil, want: ""},
		{
			name:  "done ret",
			reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/ip address\n"}}},
			want:  "/ip address\n",
		},
		{
			name: "script sentences",
			reply: &routeros.Reply{Re: []*proto.Sentence{
				{Map: map[string]string{"script": "/ip dns"}},
				{Map: map[string]string{"script": "/ip route"}},
			}},
			want: "/ip dns\n/ip route\n",
		},
		{
			name: "sorted fallback keys",
			reply: &routeros.Reply{Re: []*proto.Sentence{
				{Map: map[string]string{"zeta": "/ip route\n", "alpha": "/ip dns\n"}},
			}},
			want: "/ip dns\n/ip route\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := exportText(tt.reply); got != tt.want {
				t.Fatalf("exportText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func versionReply(version string) scriptedRouterOSResponse {
	return scriptedRouterOSResponse{
		reply: &routeros.Reply{Re: []*proto.Sentence{{Map: map[string]string{"version": version}}}},
	}
}

func TestExport_UsesCompactThenStoresScript(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "# compact export"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	got, err := api.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "# compact export" {
		t.Fatalf("export = %q", got)
	}
	if len(scripted.calls) != 2 {
		t.Fatalf("calls = %#v", scripted.calls)
	}
	if scripted.calls[0][0] != "/system/resource/print" {
		t.Fatalf("version probe missing: %#v", scripted.calls)
	}
	wantExport := []string{"/export", "=compact="}
	if !sameArgs(scripted.calls[1], wantExport) {
		t.Fatalf("export args = %#v, want %#v", scripted.calls[1], wantExport)
	}
}

func TestExport_FallsBackWhenCompactEmpty(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{Re: []*proto.Sentence{{Map: map[string]string{"script": "/ip dns set servers=1.1.1.1"}}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	got, err := api.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/ip dns") {
		t.Fatalf("export = %q", got)
	}
	if len(scripted.calls) != 3 {
		t.Fatalf("calls = %#v", scripted.calls)
	}
	if !sameArgs(scripted.calls[1], []string{"/export", "=compact="}) {
		t.Fatalf("compact args = %#v", scripted.calls[1])
	}
	if !sameArgs(scripted.calls[2], []string{"/export"}) {
		t.Fatalf("fallback args = %#v", scripted.calls[2])
	}
}

func TestExport_V7AddsShowSensitive(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("7.16.2 (stable)"),
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/user\n"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if _, err := api.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/export", "=compact=", "=show-sensitive="}
	if !sameArgs(scripted.calls[1], want) {
		t.Fatalf("v7 export args = %#v, want %#v", scripted.calls[1], want)
	}
}

func TestExport_V6OmitsShowSensitive(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/user\n"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if _, err := api.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range scripted.calls {
		for _, arg := range call {
			if arg == "=show-sensitive=" {
				t.Fatalf("v6 export used show-sensitive: %#v", scripted.calls)
			}
		}
	}
}

func TestImport_RejectsEmptyScript(t *testing.T) {
	api := newScriptedAPIClient(t, &scriptedRouterOSClient{})
	err := api.Import(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "empty script") {
		t.Fatalf("error = %v", err)
	}
}

func TestImport_WritesFileThenImports(t *testing.T) {
	empty := &routeros.Reply{}
	created := &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*9", "name": restoreFileName},
	}}}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},   // remove restore.rsc
			{reply: empty},   // remove restore.rsc.txt
			{reply: created}, // /file/print file=
			{reply: empty},   // /file/set contents
			{reply: empty},   // /import
			{reply: created}, // print for remove
			{reply: empty},   // remove
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n"); err != nil {
		t.Fatal(err)
	}
	var sawPrintFile, sawSet, sawImport, sawScriptRun bool
	for _, call := range scripted.calls {
		if len(call) == 0 {
			continue
		}
		switch call[0] {
		case "/file/print":
			for _, arg := range call {
				if arg == "=file="+restoreFileName {
					sawPrintFile = true
				}
			}
		case "/file/set":
			sawSet = true
			if !containsArg(call, "=.id=*9") || !containsArgPrefix(call, "=contents=") {
				t.Fatalf("file set args = %#v", call)
			}
		case "/import":
			sawImport = true
			if !containsArg(call, "=file-name="+restoreFileName) {
				t.Fatalf("import args = %#v", call)
			}
		case "/system/script/run", "/system/script/add":
			sawScriptRun = true
		}
	}
	if !sawPrintFile || !sawSet || !sawImport {
		t.Fatalf("missing file import steps, calls=%#v", scripted.calls)
	}
	if sawScriptRun {
		t.Fatalf("system script path was used, calls=%#v", scripted.calls)
	}
}

func TestImport_LooksUpFileIDWhenCreateReplyHasNone(t *testing.T) {
	empty := &routeros.Reply{}
	lookup := &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*4", "name": restoreFileName},
	}}}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: empty},  // create print without identity
			{reply: lookup}, // ?name= lookup
			{reply: empty},  // set
			{reply: empty},  // import
			{reply: lookup},
			{reply: empty},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if err := api.Import(context.Background(), "/ip dns\n"); err != nil {
		t.Fatal(err)
	}
	var setID string
	for _, call := range scripted.calls {
		if len(call) > 0 && call[0] == "/file/set" {
			for _, arg := range call {
				if strings.HasPrefix(arg, "=.id=") {
					setID = strings.TrimPrefix(arg, "=.id=")
				}
			}
		}
	}
	if setID != "*4" {
		t.Fatalf("file set used id %q, want *4; calls=%#v", setID, scripted.calls)
	}
}

func TestImport_DoesNotFallBackToSystemScript(t *testing.T) {
	empty := &routeros.Reply{}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{err: errors.New("no file print")},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n")
	if err == nil {
		t.Fatal("expected import error")
	}
	for _, call := range scripted.calls {
		if len(call) > 0 && strings.HasPrefix(call[0], "/system/script") {
			t.Fatalf("system script used after file failure: %#v", scripted.calls)
		}
	}
}

func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsArg(call []string, want string) bool {
	for _, arg := range call {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgPrefix(call []string, prefix string) bool {
	for _, arg := range call {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}
