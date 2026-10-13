package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
	"supermetry/internal/updaterdaemon"
)

// releaseTrustedPublicKeys is injected as comma-separated standard-base64
// Ed25519 public keys at release build time. Development builds fail closed.
var releaseTrustedPublicKeys string

const (
	updaterEnvironmentPath = "/etc/supermetry/supermetry.env"
	updaterLockPath        = "/var/lib/supermetry-updater/helper.lock"
)

func updaterHelperCommand(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("the updater helper is supported on Linux only")
	}
	if !runningAsRoot() {
		return errors.New("the updater helper must run as root")
	}
	descriptor, err := updaterdaemon.LoadDescriptor(updatehelper.DeploymentDescriptorPath)
	if err != nil {
		return err
	}
	keyring, err := updaterReleaseKeyring(releaseTrustedPublicKeys)
	if err != nil {
		return err
	}
	environment, err := readUpdaterEnvironment(updaterEnvironmentPath)
	if err != nil && descriptor.Mode == updatehelper.DeploymentBinary {
		return err
	}
	factory, err := updaterdaemon.NewBackendFactory(ctx, updaterdaemon.ProductionConfig{
		Descriptor: descriptor, Keyring: keyring,
		Environment: environment, JobsDir: updatehelper.DefaultJobStoreDir,
	})
	if err != nil {
		return fmt.Errorf("configure updater backend: %w", err)
	}
	lock, err := updaterdaemon.AcquireProcessLock(updaterLockPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	listener, err := updaterdaemon.SystemdListener()
	if err != nil {
		return err
	}
	defer listener.Close()
	controller := updaterdaemon.NewController(updaterdaemon.Config{JobsDir: updatehelper.DefaultJobStoreDir, BackendFactory: factory})
	return (&updaterdaemon.Server{Listener: listener, Controller: controller}).Serve(ctx)
}

func updaterReleaseKeyring(encoded string) (release.Keyring, error) {
	if strings.TrimSpace(encoded) == "" {
		return release.Keyring{}, errors.New("release signing public key is not configured")
	}
	keys := make(map[string]ed25519.PublicKey)
	for _, item := range strings.Split(encoded, ",") {
		publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(item))
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return release.Keyring{}, errors.New("release signing public key is invalid")
		}
		digest := sha256.Sum256(publicKey)
		keyID := "ed25519-" + hex.EncodeToString(digest[:8])
		if _, duplicate := keys[keyID]; duplicate {
			return release.Keyring{}, errors.New("release signing public key is duplicated")
		}
		keys[keyID] = ed25519.PublicKey(publicKey)
	}
	return release.NewKeyring(keys)
}

func runningAsRoot() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			return len(fields) >= 2 && fields[1] == "0"
		}
	}
	return false
}

func readUpdaterEnvironment(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect updater environment: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("updater environment must be a non-writable regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validEnvironmentName(key) {
			return nil, errors.New("updater environment contains an invalid assignment")
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' && value[len(value)-1] == '\'' || value[0] == '"' && value[len(value)-1] == '"') {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read updater environment: %w", err)
	}
	result := make([]string, 0, len(values)+1)
	result = append(result, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || value[0] >= '0' && value[0] <= '9' {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
