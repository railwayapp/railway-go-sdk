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

func ExampleServiceCdn() {
	docs := railway.ServiceNamed("docs", railway.ServiceConfig{
		"cdn": railway.ServiceCdn{
			Enabled:           true,
			DefaultTtlSeconds: 86400,
			HtmlCaching:       railway.ServiceCdnHtmlCachingForce,
		},
	})
	out, _ := json.Marshal(docs.Graph()["cdn"])
	fmt.Println(string(out))
	// Output: {"defaultTtlSeconds":86400,"enabled":true,"htmlCaching":"force"}
}

func ExampleEdgeRules() {
	web := railway.ServiceNamed("web", railway.ServiceConfig{
		"edgeRules": railway.EdgeRules{{
			Priority: 0,
			If:       railway.EdgeRuleExpr{Attr: "http.host", Op: "eq", Value: "old.example.com"},
			Then: railway.EdgeRuleThen{
				Action: railway.EdgeRuleActionRedirect,
				Params: map[string]any{"status": 301, "host": "example.com", "preserve_path": true},
			},
		}},
	})
	out, _ := json.Marshal(web.Graph()["edgeRules"])
	fmt.Println(string(out))
	// Output: [{"if":{"attr":"http.host","op":"eq","value":"old.example.com"},"priority":0,"then":{"action":"redirect","params":{"host":"example.com","preserve_path":true,"status":301}}}]
}

func ExampleFn() {
	// Relative to this file: testdata/hook.ts next to example_test.go.
	hook := railway.Fn("hook", railway.ServiceConfig{
		"codeFile": "testdata/hook.ts",
	})
	graph := hook.Graph()
	source := graph["source"].(map[string]any)
	fmt.Println(graph["kind"], source["image"] == railway.FunctionImages["bun"])
	// Output: function true
}
