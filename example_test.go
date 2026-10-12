package railway_test

import (
	"encoding/json"
	"fmt"

	railway "github.com/railwayapp/railway-go-sdk"
)

func ExampleProjectNamed() {
	web := railway.ServiceNamed("web", railway.ServiceConfig{
		"start": "./app",
	})
	graph := railway.ProjectNamed("demo", []any{web}).Graph()
	fmt.Println(graph["name"])
	// Output: demo
}

func ExampleAutoUpdates() {
	source := railway.Image("nginx:latest", map[string]any{
		"autoUpdates": railway.AutoUpdates{
			Type:     railway.AutoUpdatesPatch,
			Schedule: []railway.AutoUpdateWindow{{Day: 6, StartHour: 2, EndHour: 6}},
		},
	})
	out, _ := json.Marshal(source["autoUpdates"])
	fmt.Println(string(out))
	// Output: {"schedule":[{"day":6,"endHour":6,"startHour":2}],"type":"patch"}
}

func ExampleFn() {
	// Relative to this file: testdata/hook.ts next to example_test.go.
	hook := railway.Fn("hook", railway.ServiceConfig{
		"codeFile": "testdata/hook.ts",
	})
	graph := hook.Graph()
	fmt.Println(graph["kind"], graph["source"].(map[string]any)["image"])
	// Output: function ghcr.io/railwayapp/function-bun:1.4.0
}
