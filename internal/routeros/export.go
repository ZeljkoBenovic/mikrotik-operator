package routeros

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-routeros/routeros/v3"
)

func (a *apiClient) Export(ctx context.Context) (string, error) {
	showSensitive := a.exportShowSensitive(ctx)
	var lastErr error
	for _, compact := range []bool{true, false} {
		reply, err := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, exportArgs(compact, showSensitive))
		if err != nil {
			lastErr = err
			continue
		}
		text := exportText(reply)
		if strings.TrimSpace(text) == "" {
			continue
		}
		return text, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("export RouterOS configuration: %w", lastErr)
	}
	return "", fmt.Errorf("export RouterOS configuration: empty export")
}

func exportArgs(compact, showSensitive bool) []string {
	args := []string{"/export"}
	if compact {
		args = append(args, "=compact=")
	}
	if showSensitive {
		args = append(args, "=show-sensitive=")
	}
	return args
}

func (a *apiClient) exportShowSensitive(ctx context.Context) bool {
	reply, err := a.runArgsContext(ctx, []string{"/system/resource/print", "=.proplist=version"})
	if err != nil {
		return false
	}
	return routerOSMajorVersion(resourceVersion(reply)) >= 7
}

func resourceVersion(reply *routeros.Reply) string {
	if reply == nil {
		return ""
	}
	for _, sentence := range reply.Re {
		if sentence == nil {
			continue
		}
		if version := strings.TrimSpace(sentence.Map["version"]); version != "" {
			return version
		}
	}
	if reply.Done != nil {
		return strings.TrimSpace(reply.Done.Map["version"])
	}
	return ""
}

func routerOSMajorVersion(version string) int {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0
	}
	end := 0
	for end < len(version) && version[end] >= '0' && version[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	major, err := strconv.Atoi(version[:end])
	if err != nil {
		return 0
	}
	return major
}

func (a *apiClient) Import(ctx context.Context, script string) error {
	if strings.TrimSpace(script) == "" {
		return fmt.Errorf("import RouterOS configuration: empty script")
	}
	if err := a.importViaFile(ctx, script); err != nil {
		return fmt.Errorf("import RouterOS configuration: %w", err)
	}
	return nil
}

func (a *apiClient) importViaFile(ctx context.Context, script string) error {
	for _, name := range restoreFileNames() {
		if err := a.removeFileByName(ctx, name); err != nil {
			return fmt.Errorf("remove leftover restore file %q: %w", name, err)
		}
	}
	chunks, err := splitRestoreScript(script, maxRestoreFileContentsBytes)
	if err != nil {
		return err
	}
	createReply, err := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, []string{
		"/file/print",
		"=file=" + restoreFileName,
	})
	if err != nil {
		return fmt.Errorf("create restore file: %w", err)
	}
	fileID, fileName := fileIdentity(createReply)
	if fileID == "" {
		fileID, fileName, err = a.lookupRestoreFile(ctx)
		if err != nil {
			return err
		}
	}
	var importErr error
	for i, chunk := range chunks {
		if _, err := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, []string{
			"/file/set",
			"=.id=" + fileID,
			"=contents=" + chunk,
		}); err != nil {
			importErr = fmt.Errorf("write restore file chunk %d: %w", i+1, err)
			break
		}
		if _, err := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, []string{
			"/import",
			"=file-name=" + fileName,
		}); err != nil {
			importErr = fmt.Errorf("run /import chunk %d: %w", i+1, err)
			break
		}
	}
	removeErr := a.removeFileByName(ctx, fileName)
	if importErr != nil {
		if removeErr != nil {
			return fmt.Errorf("%w; remove restore file: %v", importErr, removeErr)
		}
		return importErr
	}
	if removeErr != nil {
		return fmt.Errorf("remove restore file: %w", removeErr)
	}
	return nil
}

func splitRestoreScript(script string, maxBytes int) ([]string, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("restore chunk size must be positive")
	}
	statements, err := restoreStatements(script)
	if err != nil {
		return nil, err
	}
	chunks := make([]string, 0, 1)
	var current strings.Builder
	currentPath := ""
	chunkHasPath := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, ensureTrailingNewline(current.String()))
		current.Reset()
		chunkHasPath = false
	}
	for i := 0; i < len(statements); i++ {
		stmt := statements[i]
		piece := ensureTrailingNewline(stmt)
		isPath := restorePathLine(stmt)
		if isPath {
			currentPath = strings.TrimSpace(firstRestoreLine(stmt))
		}
		addition := piece
		if !chunkHasPath && currentPath != "" && !isPath && !restoreCommentLine(stmt) {
			addition = ensureTrailingNewline(currentPath) + piece
		}
		if len(addition) > maxBytes {
			return nil, fmt.Errorf("restore statement exceeds %d-byte RouterOS file contents limit", maxBytes)
		}
		if current.Len()+len(addition) > maxBytes {
			flush()
			i--
			continue
		}
		current.WriteString(addition)
		if isPath || (currentPath != "" && !restoreCommentLine(stmt)) {
			chunkHasPath = true
		}
	}
	flush()
	if len(chunks) == 0 {
		return nil, fmt.Errorf("empty script")
	}
	return chunks, nil
}

func restoreStatements(script string) ([]string, error) {
	normalized := strings.ReplaceAll(script, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	statements := make([]string, 0, len(lines))
	var current strings.Builder
	var quote byte
	braceDepth := 0
	for _, line := range lines {
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
		quote, braceDepth = scanRestoreLine(line, quote, braceDepth)
		continued := quote == 0 && strings.HasSuffix(strings.TrimRight(line, " \t"), `\`)
		if quote != 0 || braceDepth > 0 || continued {
			continue
		}
		stmt := current.String()
		current.Reset()
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		statements = append(statements, stmt)
	}
	if quote != 0 || braceDepth > 0 {
		return nil, fmt.Errorf("unterminated restore script statement")
	}
	if strings.TrimSpace(current.String()) != "" {
		statements = append(statements, current.String())
	}
	return statements, nil
}

func scanRestoreLine(line string, quote byte, braceDepth int) (byte, int) {
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		}
	}
	return quote, braceDepth
}

func restorePathLine(stmt string) bool {
	line := strings.TrimSpace(firstRestoreLine(stmt))
	if !strings.HasPrefix(line, "/") {
		return false
	}
	return restoreLineVerb(line) == ""
}

func restoreCommentLine(stmt string) bool {
	return strings.HasPrefix(strings.TrimSpace(stmt), "#")
}

func firstRestoreLine(stmt string) string {
	if i := strings.IndexByte(stmt, '\n'); i >= 0 {
		return stmt[:i]
	}
	return stmt
}

func restoreLineVerb(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	parts := strings.Split(strings.Trim(fields[0], "/"), "/")
	if tokenIsRestoreVerb(parts[len(parts)-1]) {
		return parts[len(parts)-1]
	}
	for _, field := range fields[1:] {
		if tokenIsRestoreVerb(field) {
			return field
		}
	}
	return ""
}

func tokenIsRestoreVerb(token string) bool {
	switch token {
	case "add", "set", "remove", "move", "enable", "disable", "comment", "reset", "print":
		return true
	default:
		return false
	}
}

func restoreFileNames() []string {
	return []string{restoreFileName, restoreFileName + ".txt"}
}

func fileIdentity(reply *routeros.Reply) (id, name string) {
	if reply == nil {
		return "", ""
	}
	for _, sentence := range reply.Re {
		if sentence == nil {
			continue
		}
		gotName := sentence.Map["name"]
		if !restoreFileNameMatch(gotName) {
			continue
		}
		if sentence.Map[".id"] == "" {
			continue
		}
		return sentence.Map[".id"], gotName
	}
	return "", ""
}

func restoreFileNameMatch(name string) bool {
	for _, candidate := range restoreFileNames() {
		if name == candidate {
			return true
		}
	}
	return false
}

func (a *apiClient) lookupRestoreFile(ctx context.Context) (id, name string, err error) {
	for _, candidate := range restoreFileNames() {
		reply, lookupErr := a.runArgsContext(ctx, []string{"/file/print", "=.proplist=.id,name", "?name=" + candidate})
		if lookupErr != nil {
			return "", "", fmt.Errorf("lookup restore file: %w", lookupErr)
		}
		id, name = fileIdentity(reply)
		if id != "" {
			return id, name, nil
		}
	}
	return "", "", fmt.Errorf("lookup restore file: %s not found", restoreFileName)
}

func (a *apiClient) removeFileByName(ctx context.Context, name string) error {
	reply, err := a.runArgsContext(ctx, []string{"/file/print", "=.proplist=.id,name", "?name=" + name})
	if err != nil {
		return err
	}
	if reply == nil {
		return nil
	}
	for _, sentence := range reply.Re {
		if sentence == nil {
			continue
		}
		id := sentence.Map[".id"]
		if id == "" {
			continue
		}
		if _, err := a.runArgsContext(ctx, []string{"/file/remove", "=.id=" + id}); err != nil {
			return err
		}
	}
	return nil
}

func exportText(reply *routeros.Reply) string {
	if reply == nil {
		return ""
	}
	if reply.Done != nil {
		if ret := strings.TrimSpace(reply.Done.Map["ret"]); ret != "" {
			return ensureTrailingNewline(ret)
		}
	}
	var builder strings.Builder
	for _, sentence := range reply.Re {
		if sentence == nil {
			continue
		}
		if script := sentence.Map["script"]; script != "" {
			builder.WriteString(script)
			if !strings.HasSuffix(script, "\n") {
				builder.WriteByte('\n')
			}
			continue
		}
		keys := make([]string, 0, len(sentence.Map))
		for key, value := range sentence.Map {
			if key == "" || key == ".id" || value == "" {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := sentence.Map[key]
			builder.WriteString(value)
			if !strings.HasSuffix(value, "\n") {
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}

func ensureTrailingNewline(value string) string {
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
