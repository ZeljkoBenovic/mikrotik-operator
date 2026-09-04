package routeros

import (
	"context"
	"errors"
	"strconv"
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

func TestExport_VersionProbeFailureOmitsShowSensitive(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{err: errors.New("version print failed")},
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/user\n"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if _, err := api.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sameArgs(scripted.calls[1], []string{"/export", "=compact="}) {
		t.Fatalf("export args after version failure = %#v", scripted.calls[1])
	}
}

func TestExport_ReadsVersionFromDoneSentence(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"version": "7.16.2"}}}},
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/user\n"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	if _, err := api.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/export", "=compact=", "=show-sensitive="}
	if !sameArgs(scripted.calls[1], want) {
		t.Fatalf("v7 export args from Done version = %#v, want %#v", scripted.calls[1], want)
	}
}

func TestExport_FallsBackWhenCompactFails(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{err: errors.New("compact unsupported")},
			{reply: &routeros.Reply{Done: &proto.Sentence{Map: map[string]string{"ret": "/ip dns\n"}}}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	got, err := api.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "/ip dns" {
		t.Fatalf("export = %q", got)
	}
	if !sameArgs(scripted.calls[2], []string{"/export"}) {
		t.Fatalf("fallback args = %#v", scripted.calls[2])
	}
}

func TestExport_ReturnsErrorWhenEmpty(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{reply: &routeros.Reply{}},
			{reply: &routeros.Reply{}},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	_, err := api.Export(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty export") {
		t.Fatalf("error = %v", err)
	}
}

func TestExport_ReturnsLastErrorWhenBothAttemptsFail(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			versionReply("6.49.18"),
			{err: errors.New("compact failed")},
			{err: errors.New("verbose failed")},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	_, err := api.Export(context.Background())
	if err == nil || !strings.Contains(err.Error(), "verbose failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestSplitRestoreScript_KeepsChunksUnderV6ContentsLimit(t *testing.T) {
	t.Parallel()
	script := largeRestoreScript(200)
	if len(script) <= maxRestoreFileContentsBytes {
		t.Fatalf("fixture too small: %d bytes", len(script))
	}
	chunks, err := splitRestoreScript(script, maxRestoreFileContentsBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2 for %d-byte script", len(chunks), len(script))
	}
	for i, chunk := range chunks {
		if len(chunk) > maxRestoreFileContentsBytes {
			t.Fatalf("chunk %d is %d bytes, want <= %d", i, len(chunk), maxRestoreFileContentsBytes)
		}
		if strings.TrimSpace(chunk) == "" {
			t.Fatalf("chunk %d is empty", i)
		}
	}
	joined := strings.Join(chunks, "\n")
	for i := 0; i < 200; i++ {
		want := "comment=op-restore-" + strconv.Itoa(i)
		if !strings.Contains(joined, want) {
			t.Fatalf("joined chunks missing %s", want)
		}
	}
}

func TestSplitRestoreScript_RepeatsPathWhenChunkStartsWithCommand(t *testing.T) {
	t.Parallel()
	script := "/ip address\n" + strings.Repeat("add address=10.0.0.1/32 interface=lo\n", 120)
	chunks, err := splitRestoreScript(script, maxRestoreFileContentsBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.Contains(chunk, "/ip address") {
			t.Fatalf("chunk %d missing path context: %q", i, chunk[:min(len(chunk), 80)])
		}
	}
}

func TestRestoreStatements_JoinsBackslashContinuation(t *testing.T) {
	t.Parallel()
	script := "/ip firewall filter\nadd action=accept chain=input \\\n    comment=\"allow admin\"\nadd action=drop chain=input\n"
	got, err := restoreStatements(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("statements = %#v", got)
	}
	if !strings.Contains(got[1], "comment=\"allow admin\"") || strings.HasSuffix(strings.TrimSpace(got[1]), "\\") {
		t.Fatalf("continuation not joined: %#v", got[1])
	}
}

func TestRestoreStatements_JoinsBraceBlocks(t *testing.T) {
	t.Parallel()
	script := "/system script\nadd name=op source={\n:put \"hi\"\n}\n/ip dns set servers=1.1.1.1\n"
	got, err := restoreStatements(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("statements = %#v", got)
	}
	if !strings.Contains(got[1], `:put "hi"`) || !strings.Contains(got[1], "}") {
		t.Fatalf("brace block not joined: %#v", got[1])
	}
}

func TestRestoreStatements_RejectsUnterminatedQuoteAndBrace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
	}{
		{name: "quote", script: "/ip firewall filter\nadd comment=\"unterminated\n"},
		{name: "brace", script: "/system script\nadd source={\n:put hi\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := restoreStatements(tt.script)
			if err == nil || !strings.Contains(err.Error(), "unterminated") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSplitRestoreScript_RejectsStatementOverLimit(t *testing.T) {
	t.Parallel()
	huge := "/ip firewall filter\nadd comment=\"" + strings.Repeat("x", maxRestoreFileContentsBytes) + "\"\n"
	_, err := splitRestoreScript(huge, maxRestoreFileContentsBytes)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestImport_RejectsEmptyScript(t *testing.T) {
	api := newScriptedAPIClient(t, &scriptedRouterOSClient{})
	err := api.Import(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "empty script") {
		t.Fatalf("error = %v", err)
	}
}

func TestImport_ReturnsLeftoverFileRemoveError(t *testing.T) {
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{err: errors.New("print failed")},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n")
	if err == nil || !strings.Contains(err.Error(), "print failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestImport_UploadsScriptLargerThanFileContentsLimitInChunks(t *testing.T) {
	script := largeRestoreScript(2000)
	if len(script) <= maxRestoreFileContentsBytes {
		t.Fatalf("fixture too small: %d bytes", len(script))
	}
	if len(script) <= 60*1024 {
		t.Fatalf("fixture %d bytes does not exceed the v7 ~60KiB contents cap", len(script))
	}
	chunks, err := splitRestoreScript(script, maxRestoreFileContentsBytes)
	if err != nil {
		t.Fatal(err)
	}
	scripted := &scriptedRouterOSClient{responses: restoreImportResponses(len(chunks))}
	api := newScriptedAPIClient(t, scripted)
	if err := api.Import(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	var sets, imports int
	var contents []string
	for _, call := range scripted.calls {
		if len(call) == 0 {
			continue
		}
		switch call[0] {
		case "/file/set":
			sets++
			got := contentsArg(call)
			if got == "" {
				t.Fatalf("file set missing contents: %#v", call)
			}
			if len(got) > maxRestoreFileContentsBytes {
				t.Fatalf("contents length %d exceeds v6 file cap %d", len(got), maxRestoreFileContentsBytes)
			}
			contents = append(contents, got)
		case "/import":
			imports++
		}
	}
	if sets < 2 || sets != imports {
		t.Fatalf("sets=%d imports=%d calls=%#v", sets, imports, scripted.calls)
	}
	joined := strings.Join(contents, "\n")
	if !strings.Contains(joined, "comment=op-restore-0") || !strings.Contains(joined, "comment=op-restore-1999") {
		t.Fatalf("imported chunks missing script body")
	}
}

func largeRestoreScript(n int) string {
	var builder strings.Builder
	builder.WriteString("/ip firewall filter\n")
	for i := 0; i < n; i++ {
		builder.WriteString("add action=accept chain=forward comment=op-restore-")
		builder.WriteString(strconv.Itoa(i))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func restoreImportResponses(chunks int) []scriptedRouterOSResponse {
	empty := scriptedRouterOSResponse{reply: &routeros.Reply{}}
	created := scriptedRouterOSResponse{reply: &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*9", "name": restoreFileName},
	}}}}
	responses := []scriptedRouterOSResponse{empty, empty, created}
	for i := 0; i < chunks; i++ {
		responses = append(responses, empty, empty)
	}
	return append(responses, created, empty)
}

func contentsArg(call []string) string {
	for _, arg := range call {
		if strings.HasPrefix(arg, "=contents=") {
			return strings.TrimPrefix(arg, "=contents=")
		}
	}
	return ""
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

func TestImport_WriteErrorStillRemovesRestoreFile(t *testing.T) {
	empty := &routeros.Reply{}
	created := &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*9", "name": restoreFileName},
	}}}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: created},
			{err: errors.New("contents too large")},
			{reply: created},
			{reply: empty},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n")
	if err == nil || !strings.Contains(err.Error(), "contents too large") {
		t.Fatalf("error = %v", err)
	}
	if !sawCommand(scripted.calls, "/file/remove") {
		t.Fatalf("write failure left restore file: %#v", scripted.calls)
	}
}

func TestImport_ImportErrorStillRemovesRestoreFile(t *testing.T) {
	empty := &routeros.Reply{}
	created := &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*9", "name": restoreFileName},
	}}}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: created},
			{reply: empty},
			{err: errors.New("syntax error")},
			{reply: created},
			{reply: empty},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n")
	if err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("error = %v", err)
	}
	if !sawCommand(scripted.calls, "/file/remove") {
		t.Fatalf("import failure left restore file: %#v", scripted.calls)
	}
}

func TestImport_JoinsRemoveErrorAfterImportFailure(t *testing.T) {
	empty := &routeros.Reply{}
	created := &routeros.Reply{Re: []*proto.Sentence{{
		Map: map[string]string{".id": "*9", "name": restoreFileName},
	}}}
	scripted := &scriptedRouterOSClient{
		responses: []scriptedRouterOSResponse{
			{reply: empty},
			{reply: empty},
			{reply: created},
			{reply: empty},
			{err: errors.New("syntax error")},
			{err: errors.New("remove print failed")},
		},
	}
	api := newScriptedAPIClient(t, scripted)
	err := api.Import(context.Background(), "/ip dns set servers=1.1.1.1\n")
	if err == nil || !strings.Contains(err.Error(), "syntax error") || !strings.Contains(err.Error(), "remove print failed") {
		t.Fatalf("error = %v", err)
	}
}

func sawCommand(calls [][]string, command string) bool {
	for _, call := range calls {
		if len(call) > 0 && call[0] == command {
			return true
		}
	}
	return false
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
