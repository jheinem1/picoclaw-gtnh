package workspace_test

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestGTNHTasksConcurrentAddsHaveUniqueIDs(t *testing.T) {
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	stateDir := t.TempDir()
	tasksPath := filepath.Join(stateDir, "gtnh_tasks.tsv")
	env := append(os.Environ(),
		"GTNH_TASKS_FILE="+tasksPath,
		"GTNH_TASKS_UPDATED_FILE="+filepath.Join(stateDir, "gtnh_tasks.updated"),
		"GTNH_TASKS_STATUS_FILE="+filepath.Join(stateDir, "gtnh_task_status_updates.json"),
		"GTNH_TASKS_LOCK_FILE="+filepath.Join(stateDir, "gtnh_tasks.lock"),
	)

	const adds = 24
	errs := make(chan error, adds)
	var wg sync.WaitGroup
	for i := 0; i < adds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command("sh", "gtnh_tasks", "add", fmt.Sprintf("concurrent task %02d", i))
			cmd.Dir = workspace
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("add %d: %w: %s", i, err, out)
				return
			}
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	file, err := os.Open(tasksPath)
	if err != nil {
		t.Fatalf("Open(tasks) error = %v", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll(tasks) error = %v", err)
	}
	if len(rows) != adds+1 {
		t.Fatalf("task rows = %d, want %d", len(rows), adds+1)
	}
	ids := map[string]struct{}{}
	for _, row := range rows[1:] {
		if len(row) == 0 {
			t.Fatal("empty task row")
		}
		if _, exists := ids[row[0]]; exists {
			t.Fatalf("duplicate task id %q", row[0])
		}
		ids[row[0]] = struct{}{}
	}
}
