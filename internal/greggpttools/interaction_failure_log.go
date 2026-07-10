package greggpttools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const interactionFailureLogSchemaVersion = 1

type interactionFailureLogEntry struct {
	SchemaVersion  int      `json:"schema_version"`
	TimestampUTC   string   `json:"timestamp_utc"`
	Reason         string   `json:"reason"`
	RequestSummary string   `json:"request_summary"`
	FailureSummary string   `json:"failure_summary"`
	FailedTools    []string `json:"failed_tools,omitempty"`
	NextStep       string   `json:"next_step,omitempty"`
}

func interactionFailureLogTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("interaction_failure_log", GroupDiagnostics, "Append a concise structured JSONL diagnostic when GregGPT cannot complete an interaction satisfactorily. Write-only; no read/list capability.", timeout, object(
		required("reason", stringSpec("Plain-language failure cause, for example broken tool, missing local data, missing capability, ambiguous request, stale index, or timeout risk.")),
		required("request_summary", stringSpec("Concise summary of what the user wanted.")),
		required("failure_summary", stringSpec("Concise explanation of why GregGPT could not complete the request.")),
		optional("failed_tools", stringArraySpec("Tool names or commands involved in the failure.")),
		optional("next_step", stringSpec("Smallest useful fix, clarification, or operator action.")),
	), func(_ context.Context, a Arguments) (Result, error) {
		path := cfg.resolvedFailedInteractionsPath()
		entry := interactionFailureLogEntry{
			SchemaVersion:  interactionFailureLogSchemaVersion,
			TimestampUTC:   time.Now().UTC().Format(time.RFC3339Nano),
			Reason:         stringArg(a, "reason"),
			RequestSummary: stringArg(a, "request_summary"),
			FailureSummary: stringArg(a, "failure_summary"),
			FailedTools:    nonEmptyStrings(stringSliceArg(a, "failed_tools")),
			NextStep:       stringArg(a, "next_step"),
		}
		if err := appendInteractionFailureLog(path, entry); err != nil {
			return Result{}, err
		}
		return Result{
			Name:   "interaction_failure_log",
			OK:     true,
			Stdout: fmt.Sprintf(`{"logged":true,"path":%q}`, path),
		}, nil
	})
}

func appendInteractionFailureLog(path string, entry interactionFailureLogEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create failed interaction log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open failed interaction log: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return fmt.Errorf("append failed interaction log entry: %w", err)
	}
	return nil
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
