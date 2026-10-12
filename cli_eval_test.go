package railway_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	railway "github.com/railwayapp/railway-go-sdk"
)

// Matches the CLI's evaluate_go wrapper: go run railway.go railway_iac_eval_main.go
func TestCLIEvalWrapper(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	moduleRoot, err := filepath.Abs(filepath.Dir(thisFile))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(
		"module railway-eval\n\ngo 1.22\n\nrequire github.com/railwayapp/railway-go-sdk v0.0.0\nreplace github.com/railwayapp/railway-go-sdk => "+moduleRoot+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "railway.go"), []byte(`package main

import railway "github.com/railwayapp/railway-go-sdk"

const Partial = "api"

func Railway() railway.Project {
	web := railway.ServiceNamed("api", railway.ServiceConfig{"start": "echo api"})
	hook := railway.Fn("hook", railway.ServiceConfig{"codeFile": "hook.ts"})
	return railway.ProjectNamed("app", []any{web, hook})
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.ts"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "railway_iac_eval_main.go"), []byte(`package main
import ("encoding/json"; "os")
func main() {
  out, err := json.Marshal(map[string]any{"partial": Partial, "project": Railway().Graph()})
  if err != nil { panic(err) }
  os.Stdout.Write(out)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "railway.go", "railway_iac_eval_main.go")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	var payload struct {
		Partial string         `json:"partial"`
		Project map[string]any `json:"project"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if payload.Partial != "api" {
		t.Fatalf("partial: %q", payload.Partial)
	}
	resources, _ := payload.Project["resources"].([]any)
	if len(resources) != 2 {
		t.Fatalf("resources: %v", payload.Project["resources"])
	}
	node := resources[0].(map[string]any)
	if node["address"] != "service.api" {
		t.Fatalf("node: %v", node)
	}
	hook := resources[1].(map[string]any)
	deploy, _ := hook["deploy"].(map[string]any)
	if hook["kind"] != "function" || deploy["startCommand"] != railway.FunctionStartCommand("export {}") {
		t.Fatalf("hook: %v", hook)
	}
}
