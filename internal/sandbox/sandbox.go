package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// SandboxWorkspace is the workspace directory inside the sandbox.
	SandboxWorkspace = "/tmp/workspace" //nolint:gosec // not a credential
	// SandboxClaudeConfig is the Claude config directory inside the sandbox.
	SandboxClaudeConfig = "/tmp/claude-config" //nolint:gosec // not a credential

	createTimeout   = 3 * time.Minute
	readyTimeout    = 2 * time.Minute
	readyPoll       = 2 * time.Second
	transferTimeout = 5 * time.Minute
)

// EnsureProvider creates or updates a provider on the gateway. Credential
// values may contain ${VAR} references which are expanded from the host
// environment before being passed to openshell.
//
// Credentials use the bare-key form (--credential KEY) so that secret values
// never appear on the process command line. The expanded values are injected
// into the child process environment, where openshell reads them directly.
// See https://docs.nvidia.com/openshell/latest/sandboxes/manage-providers#bare-key-form
func EnsureProvider(name, providerType string, credentials, config map[string]string) error {
	args, extraEnv, secrets := buildProviderArgs(name, providerType, credentials, config)

	cmd := exec.Command("openshell", args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Redact known credential values from error output.
		outStr := string(out)
		for _, s := range secrets {
			outStr = strings.ReplaceAll(outStr, s, "***")
		}
		return fmt.Errorf("provider create %q failed: %s", name, outStr)
	}
	return nil
}

// buildProviderArgs constructs the CLI args and child environment entries for
// openshell provider create. Credentials use the bare-key form (--credential KEY)
// so secret values never appear on the process command line. The expanded values
// are returned as extra env vars to be set on the child process.
// See https://docs.nvidia.com/openshell/latest/sandboxes/manage-providers#bare-key-form
func buildProviderArgs(name, providerType string, credentials, config map[string]string) (args, extraEnv, secrets []string) {
	args = []string{"provider", "create",
		"--name", name,
		"--type", providerType,
	}

	for k, v := range credentials {
		expanded := os.ExpandEnv(v)
		if expanded != "" {
			secrets = append(secrets, expanded)
		}
		extraEnv = append(extraEnv, fmt.Sprintf("%s=%s", k, expanded))
		args = append(args, "--credential", k)
	}
	for k, v := range config {
		expanded := os.ExpandEnv(v)
		args = append(args, "--config", k+"="+expanded)
	}

	return args, extraEnv, secrets
}

// EnsureAvailable checks that the openshell binary is in PATH.
func EnsureAvailable() error {
	_, err := exec.LookPath("openshell")
	if err != nil {
		return fmt.Errorf("openshell not found in PATH: %w", err)
	}
	return nil
}

// EnsureGateway starts a local gateway if none is active. It is idempotent —
// if a gateway is already running the command is a no-op.
func EnsureGateway() error {
	// Check if a gateway is already active.
	check := exec.Command("openshell", "gateway", "info")
	if err := check.Run(); err == nil {
		return nil
	}

	cmd := exec.Command("openshell", "gateway", "start")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gateway start failed: %s", string(out))
	}
	return nil
}

// Create creates a persistent OpenShell sandbox and waits for it to be ready.
// If providers are given, they are passed as --provider flags. If image is
// non-empty, it is passed as --from to start the sandbox from a container image.
// If policy is non-empty, it is applied at creation time via --policy.
func Create(name string, providers []string, image, policy string) error {
	ctx, cancel := context.WithTimeout(context.Background(), createTimeout)
	defer cancel()

	args := []string{
		"sandbox", "create",
		"--name", name,
		"--keep",
		"--no-auto-providers",
		"--no-tty",
	}
	if image != "" {
		args = append(args, "--from", image)
	}
	if policy != "" {
		args = append(args, "--policy", policy)
	}
	for _, p := range providers {
		args = append(args, "--provider", p)
	}
	// Without a command, sandbox create starts an interactive shell and
	// blocks until it exits. Pass `true` so it returns immediately.
	args = append(args, "--", "true")

	cmd := exec.CommandContext(ctx, "openshell", args...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()

	if err != nil {
		check := exec.Command("openshell", "sandbox", "get", name)
		if checkErr := check.Run(); checkErr != nil {
			return fmt.Errorf("sandbox create failed: %s", string(out))
		}
	}

	// Wait for sandbox to be fully ready (image pull can take a while).
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		check := exec.Command("openshell", "sandbox", "get", name)
		output, checkErr := check.Output()
		if checkErr == nil && strings.Contains(string(output), "Ready") {
			return nil
		}
		time.Sleep(readyPoll)
	}

	return fmt.Errorf("sandbox %q not ready after %s", name, readyTimeout)
}

// Delete deletes a sandbox, returning any error for the caller to log.
func Delete(name string) error {
	out, err := exec.Command("openshell", "sandbox", "delete", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox delete %q failed: %s", name, string(out))
	}
	return nil
}

// GetSSHConfig retrieves the SSH config for a sandbox.
func GetSSHConfig(name string) (string, error) {
	out, err := exec.Command("openshell", "sandbox", "ssh-config", name).Output()
	if err != nil {
		return "", fmt.Errorf("getting SSH config for sandbox %q: %w", name, err)
	}
	return string(out), nil
}

// SCP copies a local file or directory into a sandbox.
func SCP(sshConfigPath, sandboxName, localPath, remotePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "scp",
		"-F", sshConfigPath,
		"-r",
		localPath,
		fmt.Sprintf("openshell-%s:%s", sandboxName, remotePath),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("scp to sandbox %q timed out after %s", sandboxName, transferTimeout)
		}
		return fmt.Errorf("scp to sandbox %q failed: %s: %w", sandboxName, string(out), err)
	}
	return nil
}

// SSH runs a command inside a sandbox and returns stdout, stderr, and exit code.
func SSH(sshConfigPath, sandboxName, command string, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh",
		"-F", sshConfigPath,
		fmt.Sprintf("openshell-%s", sandboxName),
		command,
	)

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if runErr != nil && ctx.Err() != nil {
		return stdoutBuf.String(), stderrBuf.String(), exitCode,
			fmt.Errorf("ssh command timed out after %s", timeout)
	}

	if runErr != nil && cmd.ProcessState == nil {
		return "", "", exitCode, fmt.Errorf("ssh failed to start: %w", runErr)
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, nil
}

// SSHStream runs a command inside a sandbox, streaming output to the given writers.
func SSHStream(sshConfigPath, sandboxName, command string, timeout time.Duration, stdoutW, stderrW *os.File) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh",
		"-F", sshConfigPath,
		fmt.Sprintf("openshell-%s", sandboxName),
		command,
	)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	err := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil && ctx.Err() != nil {
		return exitCode, fmt.Errorf("ssh command timed out after %s", timeout)
	}

	if err != nil && cmd.ProcessState == nil {
		return exitCode, fmt.Errorf("ssh failed to start: %w", err)
	}

	return exitCode, nil
}

// SSHStreamReader runs a command inside a sandbox, returning an io.ReadCloser for
// stdout so the caller can parse structured output. Stderr is forwarded to the
// given writer. The caller must read stdout to completion, then call cmd.Wait().
func SSHStreamReader(sshConfigPath, sandboxName, command string, timeout time.Duration, stderrW io.Writer) (io.ReadCloser, *exec.Cmd, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	cmd := exec.CommandContext(ctx, "ssh",
		"-F", sshConfigPath,
		fmt.Sprintf("openshell-%s", sandboxName),
		command,
	)
	cmd.Stderr = stderrW

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("starting ssh command: %w", err)
	}

	return stdout, cmd, cancel, nil
}

// RsyncFrom copies a directory from a sandbox to the local machine using rsync
// with safety flags: symlinks are skipped (--no-links) and .git/hooks/ is
// excluded to prevent a compromised sandbox from injecting executable content
// into the host repo. Requires rsync on both host and sandbox.
func RsyncFrom(sshConfigPath, sandboxName, remoteDir, localDir string) error {
	// Trailing slashes ensure rsync copies contents, not the directory itself.
	if !strings.HasSuffix(remoteDir, "/") {
		remoteDir += "/"
	}
	if !strings.HasSuffix(localDir, "/") {
		localDir += "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout)
	defer cancel()

	remote := fmt.Sprintf("openshell-%s:%s", sandboxName, remoteDir)
	cmd := exec.CommandContext(ctx, "rsync",
		"-a",
		"--no-links",
		"--exclude", ".git/hooks/",
		"-e", fmt.Sprintf("ssh -F %s", sshConfigPath),
		remote,
		localDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("rsync from sandbox %q timed out after %s", sandboxName, transferTimeout)
		}
		return fmt.Errorf("rsync from sandbox %q failed: %s: %w", sandboxName, string(out), err)
	}
	return nil
}

// SCPFrom copies a file or directory from a sandbox to the local machine.
func SCPFrom(sshConfigPath, sandboxName, remotePath, localPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "scp",
		"-F", sshConfigPath,
		"-r",
		fmt.Sprintf("openshell-%s:%s", sandboxName, remotePath),
		localPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("scp from sandbox %q timed out after %s", sandboxName, transferTimeout)
		}
		return fmt.Errorf("scp from sandbox %q failed: %s: %w", sandboxName, string(out), err)
	}
	return nil
}

// ExtractTranscripts copies Claude transcript files (.jsonl) from the sandbox
// to a local output directory.
func ExtractTranscripts(sshConfigPath, sandboxName, agentName, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Find transcript files in the sandbox.
	stdout, _, _, err := SSH(sshConfigPath, sandboxName,
		fmt.Sprintf("find %s -name '*.jsonl' 2>/dev/null || true", SandboxClaudeConfig),
		10*time.Second,
	)
	if err != nil {
		return fmt.Errorf("finding transcripts: %w", err)
	}

	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		fmt.Fprintf(os.Stderr, "  [%s] No transcripts found\n", agentName)
		return nil
	}
	files := strings.Split(trimmed, "\n")

	cleanBase := filepath.Clean(outputDir) + string(filepath.Separator)

	for _, remotePath := range files {
		remotePath = strings.TrimSpace(remotePath)
		if remotePath == "" {
			continue
		}
		localName := fmt.Sprintf("%s-%s", agentName, filepath.Base(remotePath))
		localPath := filepath.Join(outputDir, localName)

		// Prevent path traversal from sandbox-controlled filenames.
		if !strings.HasPrefix(filepath.Clean(localPath), cleanBase) {
			fmt.Fprintf(os.Stderr, "  [%s] Skipping path traversal attempt: %s\n", agentName, localName)
			continue
		}

		if scpErr := SCPFrom(sshConfigPath, sandboxName, remotePath, localPath); scpErr != nil {
			fmt.Fprintf(os.Stderr, "  [%s] Failed to copy transcript: %v\n", agentName, scpErr)
			continue
		}
		fmt.Fprintf(os.Stderr, "  [%s] Saved transcript: %s\n", agentName, localName)
	}

	return nil
}

// ExtractOutputFiles copies all files under a remote directory in the sandbox
// to a local output directory, preserving relative paths.
func ExtractOutputFiles(sshConfigPath, sandboxName, remoteDir, localDir string) ([]string, error) {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating local output dir: %w", err)
	}

	// List files in the sandbox output directory.
	stdout, _, _, err := SSH(sshConfigPath, sandboxName,
		fmt.Sprintf("find %s -type f 2>/dev/null || true", remoteDir),
		10*time.Second,
	)
	if err != nil {
		return nil, fmt.Errorf("listing output files: %w", err)
	}

	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")

	cleanBase := filepath.Clean(localDir) + string(filepath.Separator)

	var extracted []string
	for _, remotePath := range lines {
		remotePath = strings.TrimSpace(remotePath)
		if remotePath == "" {
			continue
		}
		// Preserve the relative path under remoteDir.
		relPath := strings.TrimPrefix(remotePath, remoteDir)
		relPath = strings.TrimPrefix(relPath, "/")
		localPath := filepath.Join(localDir, relPath)

		// Prevent path traversal from sandbox-controlled filenames.
		if !strings.HasPrefix(filepath.Clean(localPath), cleanBase) {
			fmt.Fprintf(os.Stderr, "  Skipping path traversal attempt: %s\n", relPath)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "  Failed to create dir for %s: %v\n", relPath, err)
			continue
		}

		if scpErr := SCPFrom(sshConfigPath, sandboxName, remotePath, localPath); scpErr != nil {
			fmt.Fprintf(os.Stderr, "  Failed to copy %s: %v\n", relPath, scpErr)
			continue
		}
		extracted = append(extracted, localPath)
	}

	return extracted, nil
}
