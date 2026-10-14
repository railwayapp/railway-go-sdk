package railway

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func TestServiceCdn(t *testing.T) {
	expectPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected panic", name)
			}
		}()
		fn()
	}
	cdnOf := func(svc Service) map[string]any {
		t.Helper()
		cdn, _ := svc.Graph()["cdn"].(map[string]any)
		return cdn
	}

	// The CLI compiles `cdn` into `services[id].cdn` and Railway serialises
	// only the knobs that differ from the defaults, so the SDK passes the block
	// through rather than filling defaults in.
	swr := false
	typed := ServiceNamed("docs", ServiceConfig{"cdn": ServiceCdn{
		Enabled:              true,
		DefaultTtlSeconds:    86400,
		HtmlCaching:          ServiceCdnHtmlCachingForce,
		StaleWhileRevalidate: &swr,
		PurgeOnDeploy:        ServiceCdnPurgeOnDeployAll,
	}})
	want := map[string]any{
		"enabled":              true,
		"defaultTtlSeconds":    86400,
		"htmlCaching":          "force",
		"staleWhileRevalidate": false,
		"purgeOnDeploy":        "all",
	}
	if got := cdnOf(typed); !reflect.DeepEqual(got, want) {
		t.Fatalf("typed cdn: %v", got)
	}

	// The raw map form `railway config pull` writes is normalised to the same graph.
	raw := ServiceNamed("docs", ServiceConfig{"cdn": map[string]any{
		"enabled":              true,
		"defaultTtlSeconds":    86400.0, // JSON-decoded numbers are floats.
		"htmlCaching":          "force",
		"staleWhileRevalidate": false,
		"purgeOnDeploy":        "all",
	}})
	if got := cdnOf(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("raw cdn: %v", got)
	}

	// {enabled: true} is a service caching with the defaults; nothing is filled in.
	enabled := ServiceNamed("web", ServiceConfig{"cdn": ServiceCdn{Enabled: true}})
	if got := cdnOf(enabled); !reflect.DeepEqual(got, map[string]any{"enabled": true}) {
		t.Fatalf("enabled only: %v", got)
	}
	disabled := ServiceNamed("web", ServiceConfig{"cdn": map[string]any{"enabled": false}})
	if got := cdnOf(disabled); !reflect.DeepEqual(got, map[string]any{"enabled": false}) {
		t.Fatalf("disabled: %v", got)
	}
	worker := Fn("worker", ServiceConfig{"cdn": ServiceCdn{Enabled: true}})
	if worker.Graph()["kind"] != "function" || cdnOf(worker)["enabled"] != true {
		t.Fatalf("function cdn: %v", worker.Graph())
	}

	// Unknown raw keys pass through so a newer `railway config pull` still evaluates.
	future := ServiceNamed("web", ServiceConfig{"cdn": map[string]any{"enabled": true, "compression": "br"}})
	if got := cdnOf(future); got["compression"] != "br" {
		t.Fatalf("unknown key: %v", got)
	}

	// No knobs is no block; nil knobs are dropped.
	for name, cfg := range map[string]ServiceConfig{
		"absent":  {},
		"nil":     {"cdn": nil},
		"nil ptr": {"cdn": (*ServiceCdn)(nil)},
		"empty":   {"cdn": map[string]any{}},
		"nils":    {"cdn": map[string]any{"enabled": nil, "htmlCaching": nil}},
	} {
		if _, ok := ServiceNamed("web", cfg).Graph()["cdn"]; ok {
			t.Fatalf("%s: cdn should be dropped", name)
		}
	}
	if _, ok := Postgres("db").Graph()["cdn"]; ok {
		t.Fatalf("database carries cdn")
	}

	expectPanic("bad htmlCaching", func() {
		ServiceNamed("web", ServiceConfig{"cdn": ServiceCdn{Enabled: true, HtmlCaching: "always"}})
	})
	expectPanic("raw bad purgeOnDeploy", func() {
		ServiceNamed("web", ServiceConfig{"cdn": map[string]any{"purgeOnDeploy": "none"}})
	})
	expectPanic("negative ttl", func() {
		ServiceNamed("web", ServiceConfig{"cdn": ServiceCdn{Enabled: true, DefaultTtlSeconds: -1}})
	})
	expectPanic("fractional ttl", func() {
		ServiceNamed("web", ServiceConfig{"cdn": map[string]any{"defaultTtlSeconds": 1.5}})
	})
	expectPanic("string enabled", func() {
		ServiceNamed("web", ServiceConfig{"cdn": map[string]any{"enabled": "yes"}})
	})
	expectPanic("bad shape", func() {
		ServiceNamed("web", ServiceConfig{"cdn": true})
	})
}

func TestFnCodeFile(t *testing.T) {
	code, err := os.ReadFile("testdata/hook.ts")
	if err != nil {
		t.Fatal(err)
	}
	// A relative codeFile resolves against this file's directory, not the
	// working directory: move away and the lookup must still succeed.
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	worker := Fn("worker", ServiceConfig{
		"codeFile": "testdata/hook.ts",
		"env":      map[string]any{"NAME": "worker"},
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
	if err != nil || string(decoded) != string(code) {
		t.Fatalf("decoded: %q %v", decoded, err)
	}
	for _, key := range []string{"codeFile", "runtime"} {
		if _, ok := node[key]; ok {
			t.Fatalf("%s leaked into the graph: %v", key, node)
		}
	}
	// The caller's config is neither mutated nor replaced.
	if worker.Config["codeFile"] != "testdata/hook.ts" {
		t.Fatalf("config: %v", worker.Config)
	}
	variables := node["variables"].(map[string]any)
	if variables["NAME"].(map[string]any)["value"] != "worker" {
		t.Fatalf("variables: %v", variables)
	}

	// An absolute path is used as is.
	absolute := filepath.Join(t.TempDir(), "abs.ts")
	if err := os.WriteFile(absolute, []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Fn("abs", ServiceConfig{"codeFile": absolute}).Graph()["deploy"].(map[string]any)["startCommand"] != FunctionStartCommand("export {}") {
		t.Fatalf("absolute codeFile")
	}

	// Explicit runtime, and an explicit source to pin another image version.
	explicit := Fn("explicit", ServiceConfig{"codeFile": "testdata/hook.ts", "runtime": "bun"}).Graph()
	if explicit["source"].(map[string]any)["image"] != FunctionImages["bun"] {
		t.Fatalf("explicit runtime: %v", explicit["source"])
	}
	pinned := Fn("pinned", ServiceConfig{
		"codeFile": "testdata/hook.ts",
		"source":   Image("ghcr.io/railwayapp/function-bun:1.2.16"),
	}).Graph()
	if pinned["source"].(map[string]any)["image"] != "ghcr.io/railwayapp/function-bun:1.2.16" {
		t.Fatalf("pinned source: %v", pinned["source"])
	}

	// No codeFile and no runtime is the old behaviour: a function node with
	// whatever the caller declared, and no synthesised source.
	plain := Fn("plain", ServiceConfig{"start": "./run.sh abc"}).Graph()
	if _, ok := plain["source"]; ok || plain["kind"] != "function" {
		t.Fatalf("plain: %v", plain)
	}
}

func TestFnCodeLimit(t *testing.T) {
	// The kernel caps one argv entry at 128 KiB; after the "./run.sh " prefix
	// the base64 has room for 98295 source bytes. Railway derives the same.
	if FunctionSourceMaxBytes != 98295 {
		t.Fatalf("limit: %d", FunctionSourceMaxBytes)
	}
	atLimit := strings.Repeat("a", FunctionSourceMaxBytes)
	command := FunctionStartCommand(atLimit)
	if !strings.HasPrefix(command, "./run.sh ") || len(command) > 128*1024-1 {
		t.Fatalf("at limit: %d bytes", len(command))
	}
	msg := mustPanic(t, func() { FunctionStartCommand(atLimit + "a") })
	if msg != "Function source code is 98296 bytes; the limit is 98295 bytes" {
		t.Fatalf("limit message: %q", msg)
	}
	// Through a file, and in bytes, not runes: a multi-byte character counts
	// its UTF-8 width.
	big := filepath.Join(t.TempDir(), "big.ts")
	if err := os.WriteFile(big, []byte(strings.Repeat("é", FunctionSourceMaxBytes/2+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := mustPanic(t, func() { Fn("big", ServiceConfig{"codeFile": big}) }); msg != "Function source code is 98296 bytes; the limit is 98295 bytes" {
		t.Fatalf("file limit message: %q", msg)
	}
}

func TestFnPanics(t *testing.T) {
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": ""}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": 42}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/hook.ts", "start": "./app"}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/hook.ts", "startCommand": "./app"}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/hook.ts", "run": map[string]any{"command": "./app"}}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/hook.ts", "deploy": map[string]any{"startCommand": "./app"}}) })
	mustPanic(t, func() { Fn("f", ServiceConfig{"runtime": "bun", "source": Image("nginx")}) })
	msg := mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/hook.ts", "runtime": "deno"}) })
	if msg != `Unknown function runtime "deno"; supported runtimes: bun.` {
		t.Fatalf("runtime message: %q", msg)
	}
	msg = mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": "testdata/missing.ts"}) })
	if !strings.HasPrefix(msg, "Function codeFile ") || !strings.Contains(msg, "testdata/missing.ts") {
		t.Fatalf("missing file message: %q", msg)
	}
	empty := filepath.Join(t.TempDir(), "empty.ts")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := mustPanic(t, func() { Fn("f", ServiceConfig{"codeFile": empty}) }); !strings.HasSuffix(msg, "is empty.") {
		t.Fatalf("empty file message: %q", msg)
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
