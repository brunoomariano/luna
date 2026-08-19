package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brunoomariano/luna/src/internal/node"
)

// SocketEnv names the socket in the agent's environment.
//
// An environment variable rather than a fixed path because the agent's cwd *is*
// the worktree, so a relative path would work — until a stage runs a command in a
// subdirectory, which is ordinary. The brief names it too, so an agent that lost
// the variable can still be told where to look.
const SocketEnv = "LUNA_ARTIFACT_SOCKET"

// artifactCommand is how an agent hands something over and reads what a previous
// stage handed over.
//
// It talks to the socket, never to the store. That is the whole design: the agent
// runs contained, the store is outside its reach, and Luna is the only writer.
// Measured: a CLI that opened the database from inside `ai-jail`
// reported `created` with exit 0 and lost every write to a tmpfs.
//
// It is also the only path the lead uses. The lead runs uncontained and *could*
// open the store, which is exactly why the rule is worth stating: two paths to the
// same data is how they drift.
func artifactCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: usage: luna artifact <put|get> <artifact> [--stage <stage>]", ErrUsage)
	}

	socket := os.Getenv(SocketEnv)
	if socket == "" {
		return fmt.Errorf("%s is not set: this command runs inside a stage, where Luna is listening", SocketEnv)
	}

	switch args[0] {
	case "put":
		return putArtifact(env, socket, args[1:])
	case "get":
		return getArtifact(env, socket, args[1:])
	default:
		return fmt.Errorf("%w: unknown artifact subcommand %q; the two are put and get", ErrUsage, args[0])
	}
}

// putArtifact reads the content from stdin and hands it to Luna.
//
// Stdin rather than a path argument, because the agent has just written the thing
// and `luna artifact put contract < contract.md` is one shell idiom rather than a
// second convention about where files go.
func putArtifact(env Env, socket string, args []string) error {
	name, _, err := artifactArgs(args, "put")
	if err != nil {
		return err
	}

	// Nil means nothing is connected, which is reported rather than read as empty:
	// an agent whose redirect failed must not have that recorded as a handover.
	if env.In == nil {
		return fmt.Errorf("nothing is connected to stdin: %s is read from there, as in `luna artifact put %s < file`",
			name, name)
	}

	body, err := io.ReadAll(env.In)
	if err != nil {
		return fmt.Errorf("reading %s from stdin: %w", name, err)
	}
	if len(body) == 0 {
		return fmt.Errorf("%s is empty: an artifact with no content is not a handover", name)
	}

	resp, err := node.CallArtifact(socket, node.Request{Op: "put", Artifact: name, Body: body})
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return fmt.Errorf("handing over %s: %s", name, resp.Err)
	}

	fmt.Fprintf(env.Out, "handed over %s (%d bytes)\n", name, len(body))
	return nil
}

// getArtifact writes what a stage handed over to stdout.
func getArtifact(env Env, socket string, args []string) error {
	name, stage, err := artifactArgs(args, "get")
	if err != nil {
		return err
	}

	resp, err := node.CallArtifact(socket, node.Request{Op: "get", Artifact: name, Stage: stage})
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return fmt.Errorf("reading %s: %s", name, resp.Err)
	}

	_, err = env.Out.Write(resp.Body)
	return err
}

// artifactArgs reads the artifact name and the optional stage.
func artifactArgs(args []string, verb string) (name, stage string, err error) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--stage" && i+1 < len(args):
			stage = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--"):
			return "", "", fmt.Errorf("%w: unknown flag %q", ErrUsage, args[i])
		case name == "":
			name = args[i]
		default:
			return "", "", fmt.Errorf("%w: luna artifact %s takes one artifact, got %q as well", ErrUsage, verb, args[i])
		}
	}
	if name == "" {
		return "", "", fmt.Errorf("%w: luna artifact %s needs an artifact name", ErrUsage, verb)
	}
	return name, stage, nil
}
