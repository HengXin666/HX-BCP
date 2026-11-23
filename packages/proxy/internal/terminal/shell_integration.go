package terminal

import (
	"os"
	"path/filepath"
	"strings"
)

// Shell integration lets the web UI follow the shell's working directory
// without ever typing a probe command into the user's session.
//
// An earlier implementation sent pwd to the PTY on connect and after every
// command whose effect it could not predict. That polluted the user's shell
// history with lines they never typed — exactly the behaviour a terminal must
// not have. Every mature web terminal (VS Code, WezTerm, Kitty, iTerm2, Tabby,
// ttyd) solves this the same way: the shell reports its directory over an
// escape sequence (OSC 7), and a fallback reads the kernel's own view of the
// process. This file implements both, in order of preference:
//
//  1. OSC 7 - the shell emits ESC ] 7 ; file://<host><path> ST after every
//     prompt. It is enabled with a generated startup fragment that is sourced
//     *in addition to* the user's own rc, so the user's configuration is never
//     modified or replaced.
//  2. /proc/<pid>/cwd - the server owns the PTY and reads the shell process's
//     real working directory. This covers shells this package cannot inject
//     into (fish, a custom rc that rewrites PROMPT_COMMAND, ...).
//  3. The last known directory. The UI keeps it; it never runs a command to
//     find out.
//
// Every layer is silent: no command line is ever written to the PTY on the
// user's behalf, so neither the visible screen nor the history file contains a
// probe.

// shellIntegrationEnvVar disables the generated startup fragment entirely.
const shellIntegrationEnvVar = "HX_PXY_SHELL_INTEGRATION"

// ShellIntegrationDisabled reports whether the administrator turned the startup
// fragment off. Only an explicit falsy value disables it.
func ShellIntegrationDisabled(environment []string) bool {
	switch strings.ToLower(strings.TrimSpace(environmentValue(environment, shellIntegrationEnvVar))) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

// shellIntegrationDir returns the private directory holding the generated
// startup files. It lives next to the runtime files so packaging only has to
// move one path.
func shellIntegrationDir(runtimeDirectory string) string {
	if strings.TrimSpace(runtimeDirectory) == "" {
		return ""
	}
	return filepath.Join(runtimeDirectory, "shell-integration")
}

// shellFamily classifies the configured shell so the correct startup mechanism
// is used. Unknown shells fall back to POSIX sh, which is not injected into.
func shellFamily(shell string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(shell)))
	switch {
	case strings.Contains(base, "zsh"):
		return "zsh"
	case strings.Contains(base, "bash"):
		return "bash"
	default:
		return "sh"
	}
}

// osc7Function is the POSIX fragment that prints the OSC 7 sequence.
const osc7Function = "hx_osc7() { printf '\\033]7;file://%s%s\\033\\\\' \"${HOSTNAME:-localhost}\" \"${PWD}\"; }"

// writeShellIntegration materializes the startup files for one shell family and
// returns the path that must be handed to the shell, plus the extra environment.
// The files are regenerated on every start so an upgrade always ships the
// current sequence.
func writeShellIntegration(directory, shell string) (scriptPath string, extraEnv []string) {
	if directory == "" {
		return "", nil
	}
	family := shellFamily(shell)
	if family != "bash" && family != "zsh" {
		return "", nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil
	}
	if family == "bash" {
		path := filepath.Join(directory, "bashrc")
		if err := os.WriteFile(path, []byte(bashIntegrationScript()), 0o600); err != nil {
			return "", nil
		}
		return path, []string{"HISTCONTROL=ignorespace:ignoredups"}
	}
	// zsh cannot be redirected with an --rcfile flag; ZDOTDIR plus a shim
	// .zshrc preserves the user's own configuration.
	if err := os.WriteFile(filepath.Join(directory, ".zshrc"), []byte(zshIntegrationScript()), 0o600); err != nil {
		return "", nil
	}
	return "", []string{"ZDOTDIR=" + directory, "HIST_IGNORE_SPACE=1"}
}

// bashIntegrationScript sources the user's own bashrc first, then installs the
// OSC 7 hook in front of any PROMPT_COMMAND they already configured.
func bashIntegrationScript() string {
	return strings.Join([]string{
		"# HX-Pxy shell integration (generated): report the working directory",
		"# over OSC 7 so the web UI follows cd without running a probe command.",
		"# This file is rewritten on every shell start and never replaces your own rc.",
		"if [ -n \"${HX_ORIGINAL_BASHRC:-}\" ] && [ -r \"${HX_ORIGINAL_BASHRC}\" ]; then",
		"  . \"${HX_ORIGINAL_BASHRC}\"",
		"fi",
		"if [ -z \"${HX_OSC7_ACTIVE:-}\" ]; then",
		"  HX_OSC7_ACTIVE=1",
		"  " + osc7Function,
		"  case \";${PROMPT_COMMAND:-};\" in",
		"    *\";hx_osc7;\"*) ;;",
		"    *) PROMPT_COMMAND=\"hx_osc7${PROMPT_COMMAND:+;${PROMPT_COMMAND}}\" ;;",
		"  esac",
		"fi",
		"",
	}, "\n")
}

// zshIntegrationScript sources the user's own zshrc first, then registers the
// OSC 7 hook with add-zsh-hook so any existing precmd hook keeps running.
func zshIntegrationScript() string {
	return strings.Join([]string{
		"# HX-Pxy shell integration (generated): report the working directory",
		"# over OSC 7 so the web UI follows cd without running a probe command.",
		"# This file is rewritten on every shell start and never replaces your own rc.",
		"if [ -n \"${HX_ORIGINAL_ZDOTDIR:-}\" ] && [ -r \"${HX_ORIGINAL_ZDOTDIR}/.zshrc\" ]; then",
		"  ZDOTDIR=\"${HX_ORIGINAL_ZDOTDIR}\" . \"${HX_ORIGINAL_ZDOTDIR}/.zshrc\"",
		"fi",
		"if [ -z \"${HX_OSC7_ACTIVE:-}\" ]; then",
		"  HX_OSC7_ACTIVE=1",
		"  " + osc7Function,
		"  autoload -Uz add-zsh-hook 2>/dev/null && add-zsh-hook precmd hx_osc7",
		"fi",
		"",
	}, "\n")
}
