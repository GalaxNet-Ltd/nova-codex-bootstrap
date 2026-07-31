package hookconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	maxHooksFileSize = 1024 * 1024
	statusMessage    = "Notifying NovaScale"
)

var notificationEvents = []string{"PermissionRequest", "Stop"}

type rawObject map[string]json.RawMessage

func Install(path, command string) (bool, error) {
	if command == "" {
		return false, errors.New("hook command is required")
	}
	document, original, err := load(path)
	if err != nil {
		return false, err
	}
	hooks, err := hooksObject(document)
	if err != nil {
		return false, err
	}

	changed := false
	for _, event := range notificationEvents {
		groups, err := groupsForEvent(hooks, event)
		if err != nil {
			return false, err
		}
		if containsCommand(groups, command) {
			continue
		}
		groups = append(groups, newGroup(command))
		encoded, err := json.Marshal(groups)
		if err != nil {
			return false, err
		}
		hooks[event] = encoded
		changed = true
	}
	if !changed {
		return false, nil
	}
	document["hooks"] = mustJSON(hooks)
	return true, save(path, document, original)
}

func Uninstall(path, command string) (bool, error) {
	if command == "" {
		return false, errors.New("hook command is required")
	}
	document, original, err := load(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hooks, err := hooksObject(document)
	if err != nil {
		return false, err
	}

	changed := false
	for _, event := range notificationEvents {
		groups, err := groupsForEvent(hooks, event)
		if err != nil {
			return false, err
		}
		filtered, removed, err := removeCommand(groups, command)
		if err != nil {
			return false, err
		}
		if !removed {
			continue
		}
		changed = true
		if len(filtered) == 0 {
			delete(hooks, event)
			continue
		}
		encoded, err := json.Marshal(filtered)
		if err != nil {
			return false, err
		}
		hooks[event] = encoded
	}
	if !changed {
		return false, nil
	}
	document["hooks"] = mustJSON(hooks)
	return true, save(path, document, original)
}

func Installed(path, command string) (bool, error) {
	document, _, err := load(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hooks, err := hooksObject(document)
	if err != nil {
		return false, err
	}
	for _, event := range notificationEvents {
		groups, err := groupsForEvent(hooks, event)
		if err != nil || !containsCommand(groups, command) {
			return false, err
		}
	}
	return true, nil
}

func load(path string) (rawObject, []byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return rawObject{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxHooksFileSize {
		return nil, nil, errors.New("Codex hooks file is too large")
	}
	var document rawObject
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, nil, fmt.Errorf("parse Codex hooks file: %w", err)
	}
	return document, data, nil
}

func hooksObject(document rawObject) (rawObject, error) {
	if raw, ok := document["hooks"]; ok {
		var hooks rawObject
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, errors.New("Codex hooks field must be an object")
		}
		if hooks == nil {
			hooks = rawObject{}
		}
		return hooks, nil
	}
	return rawObject{}, nil
}

func groupsForEvent(hooks rawObject, event string) ([]rawObject, error) {
	raw, ok := hooks[event]
	if !ok {
		return nil, nil
	}
	var groups []rawObject
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("Codex %s hooks must be an array", event)
	}
	return groups, nil
}

func containsCommand(groups []rawObject, command string) bool {
	for _, group := range groups {
		handlers, err := handlersForGroup(group)
		if err != nil {
			continue
		}
		for _, handler := range handlers {
			if handlerCommand(handler) == command {
				return true
			}
		}
	}
	return false
}

func removeCommand(groups []rawObject, command string) ([]rawObject, bool, error) {
	result := make([]rawObject, 0, len(groups))
	removed := false
	for _, group := range groups {
		handlers, err := handlersForGroup(group)
		if err != nil {
			return nil, false, err
		}
		filtered := handlers[:0]
		for _, handler := range handlers {
			if handlerCommand(handler) == command {
				removed = true
				continue
			}
			filtered = append(filtered, handler)
		}
		if len(filtered) == 0 {
			continue
		}
		group["hooks"] = mustJSON(filtered)
		result = append(result, group)
	}
	return result, removed, nil
}

func handlersForGroup(group rawObject) ([]rawObject, error) {
	raw, ok := group["hooks"]
	if !ok {
		return nil, errors.New("Codex hook group is missing hooks")
	}
	var handlers []rawObject
	if err := json.Unmarshal(raw, &handlers); err != nil {
		return nil, errors.New("Codex hook handlers must be an array")
	}
	return handlers, nil
}

func handlerCommand(handler rawObject) string {
	var kind, command string
	_ = json.Unmarshal(handler["type"], &kind)
	_ = json.Unmarshal(handler["command"], &command)
	if kind != "command" {
		return ""
	}
	return command
}

func newGroup(command string) rawObject {
	handler := rawObject{
		"type":          mustJSON("command"),
		"command":       mustJSON(command),
		"timeout":       mustJSON(3),
		"statusMessage": mustJSON(statusMessage),
	}
	return rawObject{"hooks": mustJSON([]rawObject{handler})}
}

func save(path string, document rawObject, original []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if len(original) > 0 {
		backup := path + ".novascale-backup"
		file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, writeErr := file.Write(original); writeErr != nil {
				file.Close()
				return writeErr
			}
			if err := file.Close(); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
