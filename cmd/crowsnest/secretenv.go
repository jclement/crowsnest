// Keeping secrets out of serve's own process environment (R1b H1 residual).
//
// config.UnsetSecretEnv removes the password and friends from the environment crowsnest's
// children inherit, but not from the process's own initial environment: the kernel keeps the
// environment a process was exec'd with, readable by any process of the same user through
// /proc/<pid>/environ on Linux or `ps eww` on macOS, and every agent runs as that user. So when
// serve finds a secret in its environment it re-executes itself once, with the secrets removed
// from the new environment and handed over through an inherited pipe instead. The new process
// reads the pipe, puts the values in its in-memory environment just long enough for the config to
// load them, and UnsetSecretEnv removes them again.
//
// What this cannot reach: the process that started crowsnest (a shell, tini as PID 1 in the
// devbox) keeps whatever environment it had. To keep the password out of that too, pass it as a
// file (--password-file / CROWSNEST_PASSWORD_FILE) rather than a variable.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/jclement/crowsnest/internal/config"
)

const (
	// secretsFDEnv names the inherited file descriptor the secrets arrive on after a re-exec.
	secretsFDEnv = "CROWSNEST_SECRETS_FD"
	// maxSecretsPayload bounds the handover: far more than any password, and well inside the
	// smallest pipe buffer, so the write before exec can never block.
	maxSecretsPayload = 8 << 10
)

// secretHandover splits environ into the environment to re-exec with (no secrets) and the
// secrets themselves. ok is false when there is nothing to hand over, or when this process is
// already the re-executed one.
func secretHandover(environ, names []string) (clean []string, secrets map[string]string, ok bool) {
	secrets = map[string]string{}
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		switch {
		case name == secretsFDEnv:
			return nil, nil, false
		case slices.Contains(names, name):
			secrets[name] = value
		default:
			clean = append(clean, entry)
		}
	}
	return clean, secrets, len(secrets) > 0
}

// reexecWithoutSecrets re-executes this binary with the same arguments and the secrets moved
// from the environment to a pipe. It returns only when it did not re-exec: nothing to hand over,
// a test binary, or a failure (then serve carries on with the secrets where they were, which is
// how it behaved before, and logs why).
func reexecWithoutSecrets() error {
	clean, secrets, ok := secretHandover(os.Environ(), config.SecretEnvNames())
	if !ok {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary to re-execute it: %w", err)
	}
	if strings.HasSuffix(executable, ".test") {
		return nil // `go test` running serve in-process; re-executing would rerun the tests
	}
	payload, err := json.Marshal(secrets)
	if err != nil || len(payload) > maxSecretsPayload {
		return errors.New("the secrets in the environment are too large to hand over")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating the secret handover pipe: %w", err)
	}
	defer func() { _ = reader.Close() }()
	_, writeErr := writer.Write(payload)
	_ = writer.Close() // also closed by exec (close-on-exec), so the reader sees EOF
	if writeErr != nil {
		return fmt.Errorf("writing the secret handover pipe: %w", writeErr)
	}
	fd := int(reader.Fd())
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, 0); errno != 0 {
		return fmt.Errorf("letting the secret handover pipe survive exec: %w", errno)
	}
	clean = append(clean, secretsFDEnv+"="+strconv.Itoa(fd))
	return syscall.Exec(executable, os.Args, clean) //nolint:gosec // this binary, its own arguments
}

// restoreHandedOverSecrets reads the secrets a re-exec handed over, if this process is the
// re-executed one, and puts them in the in-memory environment for the config to load. restored
// says this process is the re-executed one, which must not re-exec again: the secrets it just
// restored would otherwise send it round forever.
func restoreHandedOverSecrets() (restored bool, err error) {
	text, ok := os.LookupEnv(secretsFDEnv)
	if !ok {
		return false, nil
	}
	_ = os.Unsetenv(secretsFDEnv)
	fd, err := strconv.Atoi(text)
	if err != nil || fd < 3 {
		return true, fmt.Errorf("%s=%q is not a file descriptor crowsnest handed itself", secretsFDEnv, text)
	}
	file := os.NewFile(uintptr(fd), "crowsnest-secrets")
	defer func() { _ = file.Close() }()
	payload, err := io.ReadAll(io.LimitReader(file, maxSecretsPayload+1))
	if err != nil {
		return true, fmt.Errorf("reading the secrets crowsnest handed itself: %w", err)
	}
	var secrets map[string]string
	if err := json.Unmarshal(payload, &secrets); err != nil {
		return true, fmt.Errorf("decoding the secrets crowsnest handed itself: %w", err)
	}
	names := config.SecretEnvNames()
	for name, value := range secrets {
		if slices.Contains(names, name) {
			_ = os.Setenv(name, value)
		}
	}
	return true, nil
}
