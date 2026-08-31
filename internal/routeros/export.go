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
		_ = a.removeFileByName(ctx, name)
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
	if _, err := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, []string{
		"/file/set",
		"=.id=" + fileID,
		"=contents=" + script,
	}); err != nil {
		return fmt.Errorf("write restore file: %w", err)
	}
	_, importErr := a.runArgsContextTimeout(ctx, routerOSBackupTimeout, []string{
		"/import",
		"=file-name=" + fileName,
	})
	removeErr := a.removeFileByName(ctx, fileName)
	if importErr != nil {
		if removeErr != nil {
			return fmt.Errorf("run /import: %w; remove restore file: %v", importErr, removeErr)
		}
		return fmt.Errorf("run /import: %w", importErr)
	}
	if removeErr != nil {
		return fmt.Errorf("remove restore file: %w", removeErr)
	}
	return nil
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
