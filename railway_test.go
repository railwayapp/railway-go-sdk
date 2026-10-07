package railway

import (
	"encoding/json"
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
		"source":   Github("org/api"),
		"start":    "./api",
		"env":      map[string]any{"DATABASE_URL": db.Env("DATABASE_URL"), "NAME": "api"},
		"domains":  []any{"api.example.com"},
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

func TestProjectVariables(t *testing.T) {
	graph := ProjectNamed("demo", []any{}, ProjectConfig{
		Variables: &VariablesPolicy{Managed: true, Ignore: []string{"FOO"}},
	}).Graph()
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Variables struct {
			Managed bool     `json:"managed"`
			Ignore  []string `json:"ignore"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Variables.Managed || len(payload.Variables.Ignore) != 1 || payload.Variables.Ignore[0] != "FOO" {
		t.Fatalf("variables: %s", raw)
	}

	if _, ok := ProjectNamed("demo", []any{}).Graph()["variables"]; ok {
		t.Fatal("policy omitted should not emit variables")
	}

	empty := ProjectNamed("demo", []any{}, ProjectConfig{Variables: &VariablesPolicy{}}).Graph()
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Variables.Managed || len(payload.Variables.Ignore) != 0 {
		t.Fatalf("empty policy: %s", raw)
	}
	if !strings.Contains(string(raw), `"ignore":[]`) {
		t.Fatalf("ignore null: %s", raw)
	}
}

func TestContextPR(t *testing.T) {
	var ctx Context
	if err := json.Unmarshal([]byte(`{"environment":"pr-12","pr":{"number":12,"branch":"feat","base":"main"}}`), &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Environment != "pr-12" || ctx.PR == nil || ctx.PR.Number != 12 || ctx.PR.Branch != "feat" || ctx.PR.Base != "main" {
		t.Fatalf("ctx: %+v pr=%+v", ctx, ctx.PR)
	}
	web := ServiceNamed("web", ServiceConfig{"env": map[string]any{"PR_BRANCH": ctx.PR.Branch}})
	graph := ProjectNamed("demo", []any{web}).Graph()
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"feat"`) {
		t.Fatalf("pr did not reach payload: %s", raw)
	}

	var none Context
	if err := json.Unmarshal([]byte(`{"environmentName":"production"}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.PR != nil || none.Environment != "production" {
		t.Fatalf("none: %+v", none)
	}
	var nullPR Context
	if err := json.Unmarshal([]byte(`{"pr":null}`), &nullPR); err != nil {
		t.Fatal(err)
	}
	if nullPR.PR != nil {
		t.Fatal("null pr")
	}
}

func TestEnvironments(t *testing.T) {
	envs := []string{"production", "staging"}
	svc := ServiceNamed("web", ServiceConfig{"environments": envs, "start": "./app"}).Graph()
	db := Postgres("db", map[string]any{"environments": envs}).Graph()
	bucket := Bucket("assets", map[string]any{"environments": envs, "region": "sjc"}).Graph()
	vol := Volume("data", map[string]any{"environments": envs, "sizeMB": 10}).Graph()
	grouped := Group("app", []any{}, map[string]any{"environments": envs, "color": "blue"})
	groupNode := grouped[0].(Resource).Graph()

	for _, node := range []map[string]any{svc, db, bucket, vol, groupNode} {
		got, _ := node["environments"].([]string)
		if len(got) != 2 || got[0] != "production" || got[1] != "staging" {
			t.Fatalf("environments on %v: %v", node["address"], node["environments"])
		}
	}
	cfg := vol["config"].(map[string]any)
	if _, ok := cfg["environments"]; ok || cfg["sizeMB"] != 10 {
		t.Fatalf("volume config: %v", cfg)
	}
	bucketCfg := bucket["config"].(map[string]any)
	if _, ok := bucketCfg["environments"]; ok || bucketCfg["region"] != "sjc" {
		t.Fatalf("bucket config: %v", bucketCfg)
	}
	if groupNode["color"] != "blue" {
		t.Fatalf("group: %v", groupNode)
	}

	raw, err := json.Marshal(ProjectNamed("demo", []any{
		ServiceNamed("web", ServiceConfig{"environments": envs}),
		Postgres("db", map[string]any{"environments": envs}),
		Bucket("assets", map[string]any{"environments": envs}),
		Volume("data", map[string]any{"environments": envs}),
		Group("app", []any{}, map[string]any{"environments": envs}),
	}).Graph())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"environments":["production","staging"]`) {
		t.Fatalf("payload: %s", raw)
	}
}

func TestGithubBranchless(t *testing.T) {
	owned := Github("org/app")
	if _, ok := owned["branch"]; ok || owned["repo"] != "org/app" || owned["type"] != "github" {
		t.Fatalf("owned: %v", owned)
	}
	node := ServiceNamed("web", ServiceConfig{"source": owned}).Graph()
	source := node["source"].(map[string]any)
	if _, ok := source["branch"]; ok {
		t.Fatalf("payload branch: %v", source)
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "branch") {
		t.Fatalf("branch in payload: %s", raw)
	}

	pinned := Github("org/app", map[string]any{"branch": "dev", "rootDirectory": "api"})
	if pinned["branch"] != "dev" || pinned["rootDirectory"] != "api" {
		t.Fatalf("pinned: %v", pinned)
	}
	rawMap := ServiceNamed("raw", ServiceConfig{"source": map[string]any{"repo": "org/app"}}).Graph()
	if _, ok := rawMap["source"].(map[string]any)["branch"]; ok {
		t.Fatalf("raw source defaulted branch: %v", rawMap["source"])
	}
	empty := Github("org/app", map[string]any{"branch": ""})
	if _, ok := empty["branch"]; ok {
		t.Fatalf("empty branch: %v", empty)
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
