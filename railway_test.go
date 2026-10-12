package railway

import (
	"encoding/base64"
	"fmt"
	"strings"
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

func TestFnCode(t *testing.T) {
	code := "export default { fetch: () => new Response(\"ok\") };"
	worker := Fn("worker", ServiceConfig{
		"code": code,
		"env":  map[string]any{"NAME": "worker"},
	})
	node := worker.Graph()
	if node["kind"] != "function" {
		t.Fatalf("kind: %v", node["kind"])
	}
	source, _ := node["source"].(map[string]any)
	if source["type"] != "image" || source["image"] != FunctionImages["bun"] {
		t.Fatalf("source: %v", source)
	}
	deploy, _ := node["deploy"].(map[string]any)
	start, _ := deploy["startCommand"].(string)
	if !strings.HasPrefix(start, "./run.sh ") {
		t.Fatalf("startCommand: %q", start)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(start, "./run.sh "))
	if err != nil || string(decoded) != code {
		t.Fatalf("decoded: %q %v", decoded, err)
	}
	if _, ok := node["code"]; ok {
		t.Fatalf("code leaked into the graph: %v", node)
	}
	if _, ok := node["runtime"]; ok {
		t.Fatalf("runtime leaked into the graph: %v", node)
	}
	// The caller's config is neither mutated nor replaced.
	if worker.Config["code"] != code {
		t.Fatalf("config: %v", worker.Config)
	}
	variables := node["variables"].(map[string]any)
	if variables["NAME"].(map[string]any)["value"] != "worker" {
		t.Fatalf("variables: %v", variables)
	}

	// Explicit runtime, and an explicit source to pin another image version.
	explicit := Fn("explicit", ServiceConfig{"code": code, "runtime": "bun"}).Graph()
	if explicit["source"].(map[string]any)["image"] != FunctionImages["bun"] {
		t.Fatalf("explicit runtime: %v", explicit["source"])
	}
	pinned := Fn("pinned", ServiceConfig{
		"code":   code,
		"source": Image("ghcr.io/railwayapp/function-bun:1.2.16"),
	}).Graph()
	if pinned["source"].(map[string]any)["image"] != "ghcr.io/railwayapp/function-bun:1.2.16" {
		t.Fatalf("pinned source: %v", pinned["source"])
	}

	// No code and no runtime is the old behaviour: a function node with
	// whatever the caller declared, and no synthesised source.
	plain := Fn("plain", ServiceConfig{"start": "./run.sh abc"}).Graph()
	if _, ok := plain["source"]; ok || plain["kind"] != "function" {
		t.Fatalf("plain: %v", plain)
	}
}

func TestFnCodeLimit(t *testing.T) {
	atLimit := strings.Repeat("a", FunctionSourceMaxBytes)
	if !strings.HasPrefix(FunctionStartCommand(atLimit), "./run.sh ") {
		t.Fatalf("at limit")
	}
	msg := mustPanic(t, func() { FunctionStartCommand(atLimit + "a") })
	if msg != "Function source code is 97 KB; the limit is 96 KB" {
		t.Fatalf("limit message: %q", msg)
	}
	// Bytes, not runes: a multi-byte character counts its UTF-8 width.
	mustPanic(t, func() { Fn("big", ServiceConfig{"code": strings.Repeat("é", FunctionSourceMaxBytes/2+1)}) })
}

func TestFnPanics(t *testing.T) {
	mustPanic(t, func() { Fn("f", ServiceConfig{"code": ""}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"code": 42}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"code": "x", "start": "./app"}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"code": "x", "startCommand": "./app"}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"runtime": "bun", "source": Image("nginx")}) })
	msg := mustPanic(t, func() { Fn("f", ServiceConfig{"code": "x", "runtime": "deno"}) })
	if msg != `Unknown function runtime "deno"; supported runtimes: bun.` {
		t.Fatalf("runtime message: %q", msg)
	}
}

func mustPanic(t *testing.T, fn func()) (message string) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected a panic")
		}
		message = fmt.Sprint(recovered)
	}()
	fn()
	return ""
}
