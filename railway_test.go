package railway

import (
	"reflect"
	"testing"
)

func TestProjectGraph(t *testing.T) {
	web := ServiceNamed("web", ServiceConfig{
		"build": "go build -o app .",
		"start": "./app",
	})
	graph := ProjectNamed("demo", []any{web}).Graph()
	if graph["name"] != "demo" {
		t.Fatalf("name: %v", graph["name"])
	}
	resources, ok := graph["resources"].([]any)
	if !ok || len(resources) != 1 {
		t.Fatalf("resources: %v", graph["resources"])
	}
	node, ok := resources[0].(map[string]any)
	if !ok {
		t.Fatalf("node type: %T", resources[0])
	}
	if node["type"] != "service" || node["name"] != "web" || node["address"] != "service.web" {
		t.Fatalf("node: %v", node)
	}
	build, _ := node["build"].(map[string]any)
	deploy, _ := node["deploy"].(map[string]any)
	if build["buildCommand"] != "go build -o app ." || deploy["startCommand"] != "./app" {
		t.Fatalf("commands: %v %v", build, deploy)
	}
}

func TestGithubEnvAndGroup(t *testing.T) {
	db := Postgres("db")
	api := ServiceNamed("api", ServiceConfig{
		"source":  Github("org/api"),
		"start":   "./api",
		"env":     map[string]any{"DATABASE_URL": db.Env("DATABASE_URL"), "NAME": "api"},
		"domains": []any{"api.example.com"},
		"replicas": 2,
	})
	data := Volume("data")
	web := ServiceNamed("web", ServiceConfig{
		"start":        "./app",
		"volumeMounts": map[string]any{"/data": data},
	})
	graph := ProjectNamed("demo", Group("app", []any{api, web, data})).Graph()
	resources := graph["resources"].([]any)
	if len(resources) != 4 {
		t.Fatalf("resources: %d", len(resources))
	}
	apiNode := resources[1].(map[string]any)
	if apiNode["kind"] != "github" {
		t.Fatalf("kind: %v", apiNode["kind"])
	}
	variables := apiNode["variables"].(map[string]any)
	ref := variables["DATABASE_URL"].(map[string]any)
	if ref["resource"] != "database.db" {
		t.Fatalf("ref: %v", ref)
	}
	if apiNode["groupId"] != "app" {
		t.Fatalf("groupId: %v", apiNode["groupId"])
	}
	webNode := resources[2].(map[string]any)
	attachments := webNode["volumeAttachments"].(map[string]any)
	if attachments["data"].(map[string]any)["volume"] != "volume.data" {
		t.Fatalf("attachments: %v", attachments)
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := NewContext(Context{Environment: "prod"})
	if !ctx.IsEnvironment("prod") || ctx.IsEnvironment("dev") {
		t.Fatalf("environment: %v", ctx.Environment)
	}
	if Shared("STRIPE_KEY")["name"] != "STRIPE_KEY" {
		t.Fatalf("shared")
	}
	if len(ctx.RandomString("secret", 12)) != 24 {
		t.Fatalf("random")
	}
}

func TestRefAndPreserve(t *testing.T) {
	db := Postgres("db")
	got := Ref(db, "DATABASE_URL")
	if got["type"] != "reference" || got["resource"] != "database.db" {
		t.Fatalf("ref: %v", got)
	}
	if Preserve()["type"] != "preserve" {
		t.Fatalf("preserve")
	}
	if Image("nginx:latest")["type"] != "image" {
		t.Fatalf("image")
	}
	if Bucket("assets").Address() != "bucket.assets" {
		t.Fatalf("bucket")
	}
}

func TestTracing(t *testing.T) {
	web := ServiceNamed("web", ServiceConfig{
		"start":   "./app",
		"tracing": map[string]any{"enabled": true, "autoInstrumentation": true},
	})
	tracing, _ := web.Graph()["tracing"].(map[string]any)
	if tracing["enabled"] != true || tracing["autoInstrumentation"] != true {
		t.Fatalf("tracing: %v", web.Graph()["tracing"])
	}

	worker := Fn("worker", ServiceConfig{"tracing": map[string]any{"enabled": true}})
	tracing, _ = worker.Graph()["tracing"].(map[string]any)
	if worker.Graph()["kind"] != "function" || tracing["enabled"] != true {
		t.Fatalf("function tracing: %v", worker.Graph())
	}

	// No switches is no block; nil switches are dropped.
	empty := ServiceNamed("empty", ServiceConfig{"tracing": map[string]any{}})
	if _, ok := empty.Graph()["tracing"]; ok {
		t.Fatalf("empty tracing: %v", empty.Graph()["tracing"])
	}
	partial := ServiceNamed("partial", ServiceConfig{"tracing": map[string]any{"enabled": true, "autoInstrumentation": nil}})
	tracing, _ = partial.Graph()["tracing"].(map[string]any)
	if len(tracing) != 1 || tracing["enabled"] != true {
		t.Fatalf("partial tracing: %v", tracing)
	}

	if _, ok := Postgres("db").Graph()["tracing"]; ok {
		t.Fatalf("database carries tracing")
	}
}

func TestImageAutoUpdates(t *testing.T) {
	expectPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected panic", name)
			}
		}()
		fn()
	}
	window := func(got any) map[string]any {
		t.Helper()
		schedule, _ := got.([]any)
		if len(schedule) != 1 {
			t.Fatalf("schedule: %v", got)
		}
		return schedule[0].(map[string]any)
	}

	typed := Image("nginx:latest", map[string]any{"autoUpdates": AutoUpdates{
		Type:     AutoUpdatesPatch,
		Schedule: []AutoUpdateWindow{{Day: 6, StartHour: 2, EndHour: 6}},
	}})
	auto, _ := typed["autoUpdates"].(map[string]any)
	if auto["type"] != "patch" || len(auto) != 2 {
		t.Fatalf("typed autoUpdates: %v", auto)
	}
	if w := window(auto["schedule"]); w["day"] != 6 || w["startHour"] != 2 || w["endHour"] != 6 {
		t.Fatalf("typed window: %v", w)
	}

	// The raw map form keeps working and is normalised to the same graph.
	raw := Image("ghcr.io/acme/api:1", map[string]any{"autoUpdates": map[string]any{
		"type":     "patch",
		"schedule": []any{map[string]any{"day": 6, "startHour": 2, "endHour": 6}},
	}})
	if !reflect.DeepEqual(raw["autoUpdates"], typed["autoUpdates"]) {
		t.Fatalf("raw %v != typed %v", raw["autoUpdates"], typed["autoUpdates"])
	}
	// JSON-decoded numbers are floats; integral ones are accepted.
	floats := Image("nginx", map[string]any{"autoUpdates": map[string]any{
		"schedule": []any{map[string]any{"day": 0.0, "startHour": 0.0, "endHour": 24.0}},
	}})
	if w := window(floats["autoUpdates"].(map[string]any)["schedule"]); w["day"] != 0 || w["endHour"] != 24 {
		t.Fatalf("float window: %v", w)
	}

	// Zero values are omitted; a nil schedule leaves the window unset.
	policyOnly := Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Type: AutoUpdatesMinor}})
	if got := policyOnly["autoUpdates"]; !reflect.DeepEqual(got, map[string]any{"type": "minor"}) {
		t.Fatalf("policy only: %v", got)
	}
	if _, ok := Image("nginx", map[string]any{"autoUpdates": AutoUpdates{}})["autoUpdates"]; ok {
		t.Fatalf("empty AutoUpdates should be dropped")
	}

	expectPanic("day 7", func() {
		Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Schedule: []AutoUpdateWindow{{Day: 7, StartHour: 0, EndHour: 1}}}})
	})
	expectPanic("startHour 24", func() {
		Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Schedule: []AutoUpdateWindow{{Day: 1, StartHour: 24, EndHour: 24}}}})
	})
	expectPanic("endHour 25", func() {
		Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Schedule: []AutoUpdateWindow{{Day: 1, StartHour: 0, EndHour: 25}}}})
	})
	expectPanic("end before start", func() {
		Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Schedule: []AutoUpdateWindow{{Day: 1, StartHour: 6, EndHour: 6}}}})
	})
	expectPanic("bad policy", func() {
		Image("nginx", map[string]any{"autoUpdates": AutoUpdates{Type: "weekly"}})
	})
	expectPanic("raw bad window", func() {
		Image("nginx", map[string]any{"autoUpdates": map[string]any{"schedule": []any{map[string]any{"day": 6, "startHour": 2}}}})
	})
	expectPanic("raw unknown field", func() {
		Image("nginx", map[string]any{"autoUpdates": map[string]any{"schedule": []any{map[string]any{"day": 6, "startHour": 2, "endHour": 6, "tz": "UTC"}}}})
	})
	expectPanic("raw bad shape", func() {
		Image("nginx", map[string]any{"autoUpdates": "patch"})
	})
	expectPanic("unsupported registry", func() {
		Image("quay.io/acme/api", map[string]any{"autoUpdates": AutoUpdates{Type: AutoUpdatesPatch}})
	})
	expectPanic("github source", func() {
		Github("acme/api", map[string]any{"autoUpdates": AutoUpdates{Type: AutoUpdatesPatch}})
	})
}
