package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func newRunCmd() *cobra.Command {
	var fullsendDir string
	var outputBase string
	var targetRepo string

	cmd := &cobra.Command{
		Use:   "run <agent-name>",
		Short: "Run an agent",
		Long:  "Execute an agent by name: read its harness YAML, set up the sandbox, and run the agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]
			printer := ui.New(os.Stdout)
			return runAgent(agentName, fullsendDir, outputBase, targetRepo, printer)
		},
	}

	cmd.Flags().StringVar(&fullsendDir, "fullsend-dir", "", "base directory containing the .fullsend layout")
	cmd.Flags().StringVar(&outputBase, "output-dir", "", "base directory for run output (default: /tmp/fullsend)")
	cmd.Flags().StringVar(&targetRepo, "target-repo", "", "path to the target repository")
	_ = cmd.MarkFlagRequired("fullsend-dir")
	_ = cmd.MarkFlagRequired("target-repo")

	return cmd
}

func runAgent(agentName, fullsendDir, outputBase, targetRepo string, printer *ui.Printer) error {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		printer.SetCIWriter(os.Stderr)
	}
	printer.Banner()
	printer.Blank()
	printer.Header("Running agent: " + agentName)
	printer.Blank()

	// 1. Resolve and load harness.
	harnessPath := filepath.Join(fullsendDir, "harness", agentName+".yaml")
	printer.StepStart("Loading harness: " + harnessPath)

	h, err := harness.Load(harnessPath)
	if err != nil {
		printer.StepFail("Failed to load harness")
		return fmt.Errorf("loading harness: %w", err)
	}

	absFullsendDir, err := filepath.Abs(fullsendDir)
	if err != nil {
		return fmt.Errorf("resolving fullsend dir: %w", err)
	}
	if err := h.ResolveRelativeTo(absFullsendDir); err != nil {
		printer.StepFail("Path validation failed")
		return fmt.Errorf("resolving paths: %w", err)
	}

	if err := h.ValidateRunnerEnv(); err != nil {
		printer.StepFail("Environment validation failed")
		return fmt.Errorf("validating env: %w", err)
	}
	for k, v := range h.RunnerEnv {
		h.RunnerEnv[k] = os.ExpandEnv(v)
	}
	if err := h.ValidateFilesExist(); err != nil {
		printer.StepFail("File validation failed")
		return fmt.Errorf("validating files: %w", err)
	}
	printer.StepDone("Harness loaded")

	// Print plan.
	printer.KeyValue("Agent", h.Agent)
	if h.Policy != "" {
		printer.KeyValue("Policy", h.Policy)
	}
	if h.Model != "" {
		printer.KeyValue("Model", h.Model)
	}
	if h.Image != "" {
		printer.KeyValue("Image", h.Image)
	}
	if len(h.Providers) > 0 {
		printer.KeyValue("Providers", strings.Join(h.Providers, ", "))
	}
	if len(h.Skills) > 0 {
		printer.KeyValue("Skills", strings.Join(h.Skills, ", "))
	}
	if h.AgentInput != "" {
		printer.KeyValue("Agent input", h.AgentInput)
	}
	if h.PreScript != "" {
		printer.KeyValue("Pre-script", h.PreScript)
	}
	if h.PostScript != "" {
		printer.KeyValue("Post-script", h.PostScript)
	}
	if h.TimeoutMinutes > 0 {
		printer.KeyValue("Timeout", fmt.Sprintf("%d minutes", h.TimeoutMinutes))
	}
	printer.Blank()

	// 2. Check openshell availability.
	printer.StepStart("Checking openshell availability")
	if err := sandbox.EnsureAvailable(); err != nil {
		printer.StepFail("openshell not available")
		return fmt.Errorf("openshell is required: %w", err)
	}
	printer.StepDone("openshell available")

	// 2a. Ensure a gateway is running.
	printer.StepStart("Ensuring gateway")
	if err := sandbox.EnsureGateway(); err != nil {
		printer.StepFail("Failed to start gateway")
		return fmt.Errorf("starting gateway: %w", err)
	}
	printer.StepDone("Gateway ready")

	// 2b. Ensure providers exist on the gateway (if any declared).
	if len(h.Providers) > 0 {
		providersDir := filepath.Join(absFullsendDir, "providers")
		providerDefs, err := harness.LoadProviderDefs(providersDir)
		if err != nil {
			printer.StepFail("Failed to load provider definitions")
			return fmt.Errorf("loading provider definitions: %w", err)
		}
		for _, pd := range providerDefs {
			printer.StepStart("Ensuring provider: " + pd.Name)
			if err := sandbox.EnsureProvider(pd.Name, pd.Type, pd.Credentials, pd.Config); err != nil {
				printer.StepFail("Failed to create provider " + pd.Name)
				return fmt.Errorf("ensuring provider %q: %w", pd.Name, err)
			}
			printer.StepDone("Provider ready: " + pd.Name)
		}
	}

	// 2c. Run pre-script on the host (if configured).
	if h.PreScript != "" {
		printer.StepStart("Running pre-script: " + h.PreScript)
		preCmd := exec.Command(h.PreScript)
		preCmd.Env = append(os.Environ(), envToList(h.RunnerEnv)...)
		preCmd.Stdout = os.Stdout
		preCmd.Stderr = os.Stderr
		if err := preCmd.Run(); err != nil {
			printer.StepFail("Pre-script failed")
			return fmt.Errorf("running pre-script: %w", err)
		}
		printer.StepDone("Pre-script completed")
	}

	// 3. Create sandbox.
	sandboxName := fmt.Sprintf("agent-%s-%d-%d", agentName, os.Getpid(), time.Now().Unix())
	printer.StepStart("Creating sandbox: " + sandboxName)

	if err := sandbox.Create(sandboxName, h.Providers, h.Image, h.Policy); err != nil {
		printer.StepFail("Failed to create sandbox")
		return fmt.Errorf("creating sandbox: %w", err)
	}
	if outputBase == "" {
		outputBase = filepath.Join(os.TempDir(), "fullsend")
	}
	runDir := filepath.Join(outputBase, sandboxName)

	// Post-script runs after sandbox cleanup (defers are LIFO).
	if h.PostScript != "" {
		defer func() {
			printer.StepStart("Running post-script: " + h.PostScript)
			postCmd := exec.Command(h.PostScript)
			postCmd.Dir = runDir
			postCmd.Env = append(os.Environ(), envToList(h.RunnerEnv)...)
			postCmd.Stdout = os.Stdout
			postCmd.Stderr = os.Stderr
			if err := postCmd.Run(); err != nil {
				printer.StepWarn("Post-script failed: " + err.Error())
			} else {
				printer.StepDone("Post-script completed")
			}
		}()
	}
	defer func() {
		printer.StepStart("Cleaning up sandbox")
		if err := sandbox.Delete(sandboxName); err != nil {
			printer.StepWarn("Sandbox cleanup failed: " + err.Error())
		} else {
			printer.StepDone("Sandbox deleted")
		}
	}()
	printer.StepDone("Sandbox created")

	// 4. Get SSH config.
	sshConfig, err := sandbox.GetSSHConfig(sandboxName)
	if err != nil {
		printer.StepFail("Failed to get SSH config")
		return err
	}

	sshConfigFile, err := os.CreateTemp("", "openshell-ssh-*.config")
	if err != nil {
		return fmt.Errorf("creating SSH config temp file: %w", err)
	}
	sshConfigPath := sshConfigFile.Name()
	if _, err := sshConfigFile.WriteString(sshConfig); err != nil {
		sshConfigFile.Close()
		os.Remove(sshConfigPath)
		return fmt.Errorf("writing SSH config: %w", err)
	}
	sshConfigFile.Close()
	defer os.Remove(sshConfigPath)

	// 6. Resolve target repo path (needed by bootstrap for env vars).
	repoSrc, err := filepath.Abs(targetRepo)
	if err != nil {
		return fmt.Errorf("resolving target repo path: %w", err)
	}
	repoName := filepath.Base(repoSrc)
	repoDir := fmt.Sprintf("%s/%s", sandbox.SandboxWorkspace, repoName)

	// 7. Bootstrap sandbox.
	printer.StepStart("Bootstrapping sandbox")
	if err := bootstrapSandbox(sshConfigPath, sandboxName, repoDir, h); err != nil {
		printer.StepFail("Failed to bootstrap sandbox")
		return err
	}
	printer.StepDone("Sandbox bootstrapped")

	// 8. Make project code available (copy repo root into a named subdirectory).
	printer.StepStart("Copying project code into sandbox")
	mkRepoCmd := fmt.Sprintf("mkdir -p %s", repoDir)
	if _, _, _, err := sandbox.SSH(sshConfigPath, sandboxName, mkRepoCmd, 10*time.Second); err != nil {
		return fmt.Errorf("creating repo dir in sandbox: %w", err)
	}
	if err := sandbox.SCP(sshConfigPath, sandboxName, repoSrc+"/.", repoDir+"/"); err != nil {
		printer.StepFail("Failed to copy project code")
		return fmt.Errorf("copying project code: %w", err)
	}
	printer.StepDone("Project code copied to " + repoName + "/")

	// 8b. Copy agent-input files (if configured).
	if h.AgentInput != "" {
		printer.StepStart("Copying agent-input files into sandbox")
		remoteInput := fmt.Sprintf("%s/agent-input", sandbox.SandboxWorkspace)
		mkInputCmd := fmt.Sprintf("mkdir -p %s", remoteInput)
		if _, _, _, err := sandbox.SSH(sshConfigPath, sandboxName, mkInputCmd, 10*time.Second); err != nil {
			return fmt.Errorf("creating agent-input dir in sandbox: %w", err)
		}
		if err := sandbox.SCP(sshConfigPath, sandboxName, h.AgentInput+"/.", remoteInput+"/"); err != nil {
			printer.StepFail("Failed to copy agent-input files")
			return fmt.Errorf("copying agent-input files: %w", err)
		}
		printer.StepDone("Agent-input files copied")
	}

	// 9. Run agent with validation loop.
	agentBaseName := strings.TrimSuffix(filepath.Base(h.Agent), ".md")
	claudeCmd := buildClaudeCommand(agentBaseName, h.Model, repoDir)

	timeout := time.Duration(h.TimeoutMinutes) * time.Minute
	if timeout == 0 {
		timeout = 30 * time.Minute
	}

	maxIterations := 1
	if h.ValidationLoop != nil && h.ValidationLoop.MaxIterations > 0 {
		maxIterations = h.ValidationLoop.MaxIterations
	}

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("creating run directory: %w", err)
	}

	var lastExitCode int
	var validationPassed bool
	var runCount int

	for iteration := 1; iteration <= maxIterations; iteration++ {
		runCount = iteration

		// Each iteration gets its own subdirectory for output and transcripts.
		iterDir := filepath.Join(runDir, fmt.Sprintf("iteration-%d", iteration))
		iterOutputDir := filepath.Join(iterDir, "output")
		iterTranscriptDir := filepath.Join(iterDir, "transcripts")
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return fmt.Errorf("creating iteration directory: %w", err)
		}

		if maxIterations > 1 {
			printer.Blank()
			printer.Header(fmt.Sprintf("Iteration %d of %d", iteration, maxIterations))
		}

		// Clear sandbox-side output and transcripts so the next iteration starts fresh.
		if iteration > 1 {
			clearCmd := fmt.Sprintf("rm -rf %s/output/* %s/*.jsonl",
				sandbox.SandboxWorkspace, sandbox.SandboxClaudeConfig)
			if _, _, _, clearErr := sandbox.SSH(sshConfigPath, sandboxName, clearCmd, 10*time.Second); clearErr != nil {
				printer.StepWarn("Failed to clear sandbox output: " + clearErr.Error())
			}
		}

		// 9a. Run agent.
		printer.StepStart("Running agent")
		printer.Blank()

		agentStart := time.Now()
		heartbeatDone := make(chan struct{})
		go runHeartbeat(printer, agentStart, timeout, heartbeatDone)

		var metrics RunMetrics
		exitCode, runErr := runAgentWithProgress(sshConfigPath, sandboxName, claudeCmd, timeout, printer, agentStart, &metrics)
		close(heartbeatDone)

		if runErr != nil {
			printer.StepFail("Agent execution failed")
			return fmt.Errorf("running agent (iteration %d): %w", iteration, runErr)
		}
		lastExitCode = exitCode

		printer.Blank()
		// Non-zero exit is a warning, not a failure — the validation loop is the success gate.
		if exitCode == 0 {
			printer.StepDone(fmt.Sprintf("Agent exited with code %d", exitCode))
		} else {
			printer.StepWarn(fmt.Sprintf("Agent exited with code %d", exitCode))
		}

		// 9b. Extract output files.
		printer.StepStart("Extracting output files")
		remoteSrc := fmt.Sprintf("%s/output", sandbox.SandboxWorkspace)
		extracted, extractErr := sandbox.ExtractOutputFiles(sshConfigPath, sandboxName, remoteSrc, iterOutputDir)
		if extractErr != nil {
			printer.StepWarn("Failed to extract output files: " + extractErr.Error())
		} else if len(extracted) == 0 {
			printer.StepInfo("No output files found")
		} else {
			for _, f := range extracted {
				printer.StepInfo(f)
			}
			printer.StepDone(fmt.Sprintf("Extracted %d output file(s)", len(extracted)))
		}

		// 9c. Extract transcripts for this iteration.
		printer.StepStart("Extracting transcripts")
		if err := sandbox.ExtractTranscripts(sshConfigPath, sandboxName, agentName, iterTranscriptDir); err != nil {
			printer.StepWarn("Failed to extract transcripts: " + err.Error())
		} else {
			printer.StepDone("Transcripts extracted")
		}

		// 9d. Extract target repo back to host. Uses rsync with --no-links
		// and --exclude .git/hooks/ to prevent sandbox escape via symlinks
		// or injected git hooks.
		printer.StepStart("Extracting target repo")
		if err := sandbox.RsyncFrom(sshConfigPath, sandboxName, repoDir, repoSrc); err != nil {
			printer.StepWarn("Failed to extract target repo: " + err.Error())
		} else {
			printer.StepDone("Target repo extracted to " + repoSrc)
		}

		// 9e. Run validation.
		if h.ValidationLoop == nil {
			break
		}

		printer.StepStart("Running validation: " + h.ValidationLoop.Script)
		valCmd := exec.Command(h.ValidationLoop.Script)
		valCmd.Dir = iterDir
		valCmd.Env = append(os.Environ(),
			append(envToList(h.RunnerEnv),
				fmt.Sprintf("TARGET_REPO_DIR=%s", repoSrc),
				fmt.Sprintf("FULLSEND_RUN_DIR=%s", runDir),
			)...,
		)
		valOut, valErr := valCmd.CombinedOutput()

		if valErr == nil {
			printer.StepDone("Validation passed: " + strings.TrimSpace(string(valOut)))
			validationPassed = true
			break
		}

		printer.StepFail("Validation failed: " + strings.TrimSpace(string(valOut)))
		if iteration < maxIterations {
			printer.StepInfo(fmt.Sprintf("Will retry (%d iterations remaining)", maxIterations-iteration))
		}
	}

	// 10. Print results.
	printer.Blank()
	printer.Header("Results")
	printer.KeyValue("Run directory", runDir)
	printer.KeyValue("Agent exit code", fmt.Sprintf("%d", lastExitCode))
	printer.KeyValue("Agent runs", fmt.Sprintf("%d", runCount))
	if h.ValidationLoop != nil {
		if validationPassed {
			printer.KeyValue("Validation", "passed")
		} else {
			printer.KeyValue("Validation", "failed")
		}
	}
	printer.Blank()

	if h.ValidationLoop != nil && !validationPassed {
		return fmt.Errorf("validation failed after %d iteration(s)", runCount)
	}

	return nil
}

func bootstrapSandbox(sshConfigPath, sandboxName, repoDir string, h *harness.Harness) error {
	// Create workspace structure and Claude config dir for transcripts.
	// Agent and skill definitions go in CLAUDE_CONFIG_DIR so `claude --agent`
	// finds them regardless of the repo's own .claude/ directory. When
	// CLAUDE_CONFIG_DIR is set, Claude uses it instead of ~/.claude/.
	mkdirCmd := fmt.Sprintf("mkdir -p %s/agents %s/skills %s/bin %s/.env.d %s",
		sandbox.SandboxClaudeConfig, sandbox.SandboxClaudeConfig, sandbox.SandboxWorkspace, sandbox.SandboxWorkspace, sandbox.SandboxClaudeConfig)
	if _, _, _, err := sandbox.SSH(sshConfigPath, sandboxName, mkdirCmd, 10*time.Second); err != nil {
		return fmt.Errorf("creating workspace dirs: %w", err)
	}

	// Copy agent definition to $CLAUDE_CONFIG_DIR/agents/.
	if err := sandbox.SCP(sshConfigPath, sandboxName, h.Agent,
		fmt.Sprintf("%s/agents/", sandbox.SandboxClaudeConfig)); err != nil {
		return fmt.Errorf("copying agent definition: %w", err)
	}

	// Copy skills (SCP -r copies the entire directory tree, including any
	// scripts/, references/, and assets/ bundled with the skill per the
	// agentskills.io specification).
	for _, skillPath := range h.Skills {
		if err := sandbox.SCP(sshConfigPath, sandboxName, skillPath,
			fmt.Sprintf("%s/skills/", sandbox.SandboxClaudeConfig)); err != nil {
			return fmt.Errorf("copying skill %q: %w", skillPath, err)
		}
	}

	// Write .env file (infrastructure vars) and copy host files.
	if err := bootstrapEnv(sshConfigPath, sandboxName, repoDir, h); err != nil {
		return fmt.Errorf("bootstrapping environment: %w", err)
	}

	return nil
}

// bootstrapEnv writes environment variables to a .env file in the sandbox and
// copies host files.
//
// The .env file contains infrastructure vars (PATH, CLAUDE_CONFIG_DIR) and
// sources all env files from .env.d/. Application-specific env vars (e.g.
// Vertex AI credentials) are delivered as expanded env files via host_files
// with expand: true.
//
// host_files entries copy files from the host into the sandbox at specified
// destination paths. Src values may contain ${VAR} references expanded from
// the host environment. When expand is true, file content is also expanded.
func bootstrapEnv(sshConfigPath, sandboxName, repoDir string, h *harness.Harness) error {
	remoteEnvFile := sandbox.SandboxWorkspace + "/.env"
	outputDir := sandbox.SandboxWorkspace + "/output"

	var lines []string

	// Infrastructure vars.
	lines = append(lines, fmt.Sprintf("export PATH=%s/bin:$PATH", sandbox.SandboxWorkspace))
	lines = append(lines, fmt.Sprintf("export CLAUDE_CONFIG_DIR=%s", sandbox.SandboxClaudeConfig))
	lines = append(lines, fmt.Sprintf("export FULLSEND_OUTPUT_DIR=%s", outputDir))
	lines = append(lines, fmt.Sprintf("export FULLSEND_TARGET_REPO_DIR=%s", repoDir))

	// Source all env files from .env.d/ (populated by host_files with expand: true).
	lines = append(lines, fmt.Sprintf("for f in %s/.env.d/*.env; do [ -f \"$f\" ] && . \"$f\"; done", sandbox.SandboxWorkspace))

	content := strings.Join(lines, "\n") + "\n"

	tmpFile, err := os.CreateTemp("", "fullsend-env-*.sh")
	if err != nil {
		return fmt.Errorf("creating temp env file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing temp env file: %w", err)
	}
	tmpFile.Close()

	if err := sandbox.SCP(sshConfigPath, sandboxName, tmpFile.Name(), remoteEnvFile); err != nil {
		return fmt.Errorf("copying .env file to sandbox: %w", err)
	}

	// Copy host files into the sandbox.
	for _, hf := range h.HostFiles {
		hostPath := os.ExpandEnv(hf.Src)
		if hostPath == "" {
			return fmt.Errorf("host_files: src %q expanded to empty string", hf.Src)
		}

		if hf.Expand {
			// Read file, expand ${VAR} in content, write expanded version.
			raw, err := os.ReadFile(hostPath)
			if err != nil {
				return fmt.Errorf("reading host file %s for expansion: %w", hf.Src, err)
			}
			expanded := os.ExpandEnv(string(raw))

			tmp, err := os.CreateTemp("", "fullsend-expand-*")
			if err != nil {
				return fmt.Errorf("creating temp file for expanded %s: %w", hf.Src, err)
			}
			if _, err := tmp.WriteString(expanded); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return fmt.Errorf("writing expanded %s: %w", hf.Src, err)
			}
			tmp.Close()

			if err := sandbox.SCP(sshConfigPath, sandboxName, tmp.Name(), hf.Dest); err != nil {
				os.Remove(tmp.Name())
				return fmt.Errorf("copying expanded file %s to %s: %w", hf.Src, hf.Dest, err)
			}
			os.Remove(tmp.Name())
		} else {
			if err := sandbox.SCP(sshConfigPath, sandboxName, hostPath, hf.Dest); err != nil {
				return fmt.Errorf("copying host file %s to %s: %w", hf.Src, hf.Dest, err)
			}
		}
	}

	return nil
}

// envToList converts a map of env vars to a sorted list of KEY=VALUE strings.
func envToList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]string, 0, len(env))
	for _, k := range keys {
		list = append(list, fmt.Sprintf("%s=%s", k, env[k]))
	}
	return list
}

func runAgentWithProgress(sshConfigPath, sandboxName, claudeCmd string, timeout time.Duration, printer *ui.Printer, start time.Time, metrics *RunMetrics) (int, error) {
	stdout, cmd, cancel, err := sandbox.SSHStreamReader(sshConfigPath, sandboxName, claudeCmd, timeout, os.Stderr)
	if err != nil {
		return -1, err
	}
	defer cancel()

	if parseErr := progressParser(stdout, printer, start, metrics); parseErr != nil {
		fmt.Fprintf(os.Stderr, "  progress parser: %v\n", parseErr)
	}

	waitErr := cmd.Wait()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if waitErr != nil && cmd.ProcessState == nil {
		return exitCode, fmt.Errorf("ssh failed: %w", waitErr)
	}

	return exitCode, nil
}

const heartbeatInterval = 30 * time.Second

func runHeartbeat(printer *ui.Printer, start time.Time, timeout time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			elapsed := time.Since(start).Truncate(time.Second)
			remaining := (timeout - elapsed).Truncate(time.Second)
			msg := fmt.Sprintf("Agent running (%s elapsed, %s remaining)", elapsed, remaining)
			printer.Notice(sanitizeGHA(msg))
			printer.Heartbeat(msg)
		}
	}
}

func buildClaudeCommand(agentName, model, repoDir string) string {
	envFile := sandbox.SandboxWorkspace + "/.env"

	// Defense-in-depth: escape single quotes even though Validate() rejects them.
	safe := strings.ReplaceAll(agentName, "'", "'\\''")

	modelFlag := ""
	if model != "" {
		modelFlag = fmt.Sprintf("--model '%s' ", strings.ReplaceAll(model, "'", "'\\''"))
	}

	return fmt.Sprintf(
		"cd %s && source %s && claude --print --output-format stream-json %s--agent '%s' --dangerously-skip-permissions 'Run the agent task'",
		repoDir, envFile, modelFlag, safe,
	)
}
