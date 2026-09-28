package railway

import "testing"

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
