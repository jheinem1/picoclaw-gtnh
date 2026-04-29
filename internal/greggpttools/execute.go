package greggpttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

func (r *Registry) Execute(ctx context.Context, name string, raw json.RawMessage) (Result, error) {
	tool, ok := r.tools[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
	args, err := parseArguments(tool.definition.Parameters, raw)
	if err != nil {
		return Result{}, err
	}
	return r.executeParsed(ctx, tool, args)
}

func (r *Registry) ExecuteMap(ctx context.Context, name string, args map[string]any) (Result, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return Result{}, err
	}
	return r.Execute(ctx, name, raw)
}

func (r *Registry) argvForTest(name string, raw json.RawMessage) ([]string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	if tool.buildArgv == nil {
		return nil, fmt.Errorf("tool %q does not use argv", name)
	}
	args, err := parseArguments(tool.definition.Parameters, raw)
	if err != nil {
		return nil, err
	}
	return tool.buildArgv(args)
}

func (r *Registry) executeParsed(ctx context.Context, tool Tool, args Arguments) (Result, error) {
	if tool.execute != nil {
		return tool.execute(ctx, args)
	}
	argv, err := tool.buildArgv(args)
	if err != nil {
		return Result{}, err
	}
	if err := validateArgv(argv); err != nil {
		return Result{}, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, tool.timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, argv[0], argv[1:]...)
	cmd.Dir = r.cfg.Workspace
	cmd.Env = append(os.Environ(), "GTNH_WORKSPACE="+r.cfg.Workspace)

	output := newLimitedOutput(r.cfg.MaxOutputBytes)
	cmd.Stdout = output.stdoutWriter()
	cmd.Stderr = output.stderrWriter()

	runErr := cmd.Run()
	stdout, stderr, truncated := output.values()
	result := Result{
		Name:      tool.definition.Name,
		OK:        runErr == nil,
		ExitCode:  0,
		Stdout:    stdout,
		Stderr:    stderr,
		TimedOut:  errors.Is(timeoutCtx.Err(), context.DeadlineExceeded),
		Truncated: truncated,
	}
	if runErr == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if result.TimedOut {
		result.ExitCode = -1
		return result, nil
	}
	return Result{}, runErr
}

func validateArgv(argv []string) error {
	if len(argv) < 2 {
		return fmt.Errorf("tool handler returned empty argv")
	}
	if argv[0] == "sh" && argv[1] == "-c" {
		return fmt.Errorf("tool handlers must not use shell command strings")
	}
	for i, arg := range argv {
		if arg == "" {
			return fmt.Errorf("tool handler returned empty argv element at index %d", i)
		}
	}
	return nil
}

type limitedOutput struct {
	mu        sync.Mutex
	remaining int
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	truncated bool
}

func newLimitedOutput(max int) *limitedOutput {
	if max <= 0 {
		max = DefaultMaxOutputLength
	}
	return &limitedOutput{remaining: max}
}

func (o *limitedOutput) stdoutWriter() io.Writer {
	return limitedWriter{output: o, stderr: false}
}

func (o *limitedOutput) stderrWriter() io.Writer {
	return limitedWriter{output: o, stderr: true}
}

func (o *limitedOutput) values() (string, string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stdout.String(), o.stderr.String(), o.truncated
}

type limitedWriter struct {
	output *limitedOutput
	stderr bool
}

func (w limitedWriter) Write(p []byte) (int, error) {
	w.output.mu.Lock()
	defer w.output.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}
	if w.output.remaining <= 0 {
		w.output.truncated = true
		return len(p), nil
	}
	keep := len(p)
	if keep > w.output.remaining {
		keep = w.output.remaining
		w.output.truncated = true
	}
	if w.stderr {
		_, _ = w.output.stderr.Write(p[:keep])
	} else {
		_, _ = w.output.stdout.Write(p[:keep])
	}
	w.output.remaining -= keep
	return len(p), nil
}
