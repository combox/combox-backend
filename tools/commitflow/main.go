package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	branchFlag := flag.String("branch", "", "target branch name")
	messageFlag := flag.String("message", "", "commit message")
	flag.Parse()

	branch := strings.TrimSpace(*branchFlag)
	message := strings.TrimSpace(*messageFlag)

	if branch == "" || message == "" {
		fmt.Fprintln(os.Stderr, `Usage: make commit branch=feature/name message="commit message"`)
		os.Exit(1)
	}

	current := strings.TrimSpace(runCapture("git", "rev-parse", "--abbrev-ref", "HEAD"))
	if current == "HEAD" {
		fail("Detached HEAD is not supported. Switch to a branch first.")
	}

	switch current {
	case "main", "master":
		if commandSucceeds("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
			run("git", "switch", branch)
		} else {
			run("git", "switch", "-c", branch)
		}
	default:
		if current != branch {
			fail(fmt.Sprintf("Current branch is '%s'. Use branch=%s or switch manually.", current, current))
		}
	}

	worktreeClean := commandSucceeds("git", "diff", "--quiet")
	stagedClean := commandSucceeds("git", "diff", "--cached", "--quiet")
	if worktreeClean && stagedClean {
		fail("No changes to commit.")
	}

	run("git", "add", "-A")

	if commandSucceeds("git", "diff", "--cached", "--quiet") {
		fail("Nothing staged after git add.")
	}

	run("git", "commit", "-m", message)
	run("git", "push", "-u", "origin", branch)

	fmt.Println("Next: open a PR to main and wait for CI + review before merge.")
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fail(err.Error())
	}
}

func runCapture(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if len(exitErr.Stderr) > 0 {
				fmt.Fprint(os.Stderr, string(exitErr.Stderr))
			}
			os.Exit(exitErr.ExitCode())
		}
		fail(err.Error())
	}
	return string(output)
}

func commandSucceeds(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	return cmd.Run() == nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
