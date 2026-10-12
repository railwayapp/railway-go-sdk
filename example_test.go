package railway_test

import (
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

func ExampleFn() {
	// Relative to this file: testdata/hook.ts next to example_test.go.
	hook := railway.Fn("hook", railway.ServiceConfig{
		"codeFile": "testdata/hook.ts",
	})
	graph := hook.Graph()
	fmt.Println(graph["kind"], graph["source"].(map[string]any)["image"])
	// Output: function ghcr.io/railwayapp/function-bun:1.4.0
}
