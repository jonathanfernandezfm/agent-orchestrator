package shellterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupTerminalArgvChainsCommandsAndReportsOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell script")
	}
	run := func(t *testing.T, commands ...string) (string, string) {
		t.Helper()
		dir := t.TempDir()
		argv := setupTerminalArgv("darwin", commands)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		// The script hands over to $SHELL at the end; `true` exits right away.
		cmd.Env = append(os.Environ(), "SHELL=true")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("setup script: %v\n%s", err, out)
		}
		return string(out), dir
	}

	out, dir := run(t, "cd sub 2>/dev/null; echo a > one.txt # trailing comment", "echo b > two.txt")
	if !strings.Contains(out, "[ao] Setup finished.") {
		t.Fatalf("output = %q, want finished line", out)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not written in workspace: %v", name, err)
		}
	}

	out, dir = run(t, "exit 3", "echo b > two.txt")
	if !strings.Contains(out, "[ao] Setup failed (exit 3).") {
		t.Fatalf("output = %q, want failure line with exit code", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "two.txt")); !os.IsNotExist(err) {
		t.Fatal("command after a failure should not run")
	}
}
