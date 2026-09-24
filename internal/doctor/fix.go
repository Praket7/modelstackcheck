package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func FixProviderTimeout(path, provider string, timeout time.Duration, apply bool) (string, error) {
	if strings.TrimSpace(provider) == "" || strings.ContainsAny(provider, "/\\") {
		return "", errors.New("a provider name is required")
	}
	if timeout < time.Second || timeout > 24*time.Hour {
		return "", errors.New("timeout must be from one second to 24 hours")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("could not read the OpenCode configuration")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("configuration must be a regular file")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("could not read the OpenCode configuration")
	}
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil || config == nil {
		return "", errors.New("automatic fixes support standard JSON configuration files only")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("configuration contains trailing data")
	}
	providers, _ := config["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
		config["provider"] = providers
	}
	providerConfig, _ := providers[provider].(map[string]any)
	if providerConfig == nil {
		providerConfig = map[string]any{}
		providers[provider] = providerConfig
	}
	options, _ := providerConfig["options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
		providerConfig["options"] = options
	}
	newTimeout := timeout.Milliseconds()
	if current, ok := options["timeout"].(json.Number); ok {
		if value, e := current.Int64(); e == nil && value >= newTimeout {
			return "Configured timeout is already at least " + timeout.String() + ".", nil
		}
	}
	if current, ok := options["timeout"].(float64); ok && current >= float64(newTimeout) {
		return "Configured timeout is already at least " + timeout.String() + ".", nil
	}
	options["timeout"] = newTimeout
	updated, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", errors.New("could not prepare the configuration change")
	}
	updated = append(updated, '\n')
	if !apply {
		return fmt.Sprintf("Preview only. Set provider.%s.options.timeout to %d milliseconds in %s.", provider, newTimeout, path), nil
	}
	backup := path + ".bak"
	backupFile, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return "", errors.New("backup file already exists or could not be created")
	}
	if _, err := backupFile.Write(original); err != nil {
		backupFile.Close()
		return "", errors.New("could not write the configuration backup")
	}
	if err := backupFile.Sync(); err != nil {
		backupFile.Close()
		return "", errors.New("could not save the configuration backup")
	}
	if err := backupFile.Close(); err != nil {
		return "", errors.New("could not close the configuration backup")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mdoc-fix-*")
	if err != nil {
		return "", errors.New("could not create a temporary configuration file")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return "", errors.New("could not preserve configuration permissions")
	}
	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		return "", errors.New("could not write the updated configuration")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", errors.New("could not save the updated configuration")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("could not close the updated configuration")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", errors.New("could not replace the configuration")
	}
	return fmt.Sprintf("Updated %s and saved the original as %s.", path, backup), nil
}
