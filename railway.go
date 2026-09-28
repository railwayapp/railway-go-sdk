// Package railway is a thin Railway Infrastructure as Code authoring mirror.
// It builds the same conceptual RailwayGraph as railway/iac (TypeScript).
// Plan/apply stay in the CLI; this package has no Config as Code knowledge.
//
// One repository for the whole environment: one file, no partial. One
// repository per service: one file per repository, each declaring
// `const Partial = "<name>"` so the CLI tracks which partial owns each
// resource. See https://docs.railway.com/infrastructure-as-code#multi-repo-projects
package railway

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

type ServiceConfig map[string]any

type Service struct {
	Name   string
	Config ServiceConfig
	node   map[string]any
}

func ServiceNamed(name string, config ServiceConfig) Service {
	if config == nil {
		config = ServiceConfig{}
	}
	return Service{Name: name, Config: config, node: serviceNode(name, config)}
}

func (s Service) Graph() map[string]any {
	if s.node != nil {
		return cloneMap(s.node)
	}
	return serviceNode(s.Name, s.Config)
}

func (s Service) Address() string {
	if s.node != nil {
		if address, ok := s.node["address"].(string); ok {
			return address
		}
	}
	return "service." + s.Name
}

func (s Service) Env(output string) map[string]any {
	return map[string]any{"type": "reference", "resource": s.Address(), "output": output}
}

type Resource struct {
	node map[string]any
}

func (r Resource) Graph() map[string]any {
	return cloneMap(r.node)
}

func (r Resource) Address() string {
	if address, ok := r.node["address"].(string); ok {
		return address
	}
	return ""
}

func (r Resource) Env(output string) map[string]any {
	return map[string]any{"type": "reference", "resource": r.Address(), "output": output}
}

type Project struct {
	Name      string
	Resources []any
}

func ProjectNamed(name string, resources []any) Project {
	return Project{Name: name, Resources: flatten(resources)}
}

func (p Project) Graph() map[string]any {
	resources := make([]any, 0, len(p.Resources))
	for _, r := range flatten(p.Resources) {
		resources = append(resources, graphOf(r))
	}
	return map[string]any{
		"name":      p.Name,
		"resources": resources,
	}
}

func Github(repo string, options ...map[string]any) map[string]any {
	opts := firstMap(options)
	if _, ok := opts["autoUpdates"]; ok {
		panic("Image auto updates are only supported for Docker image sources.")
	}
	branch := "main"
	if value, ok := opts["branch"].(string); ok && value != "" {
		branch = value
	}
	out := map[string]any{"type": "github", "repo": repo, "branch": branch}
	for k, v := range opts {
		if k != "branch" {
			out[k] = v
		}
	}
	return prune(out)
}

func Image(imageName string, options ...map[string]any) map[string]any {
	opts := firstMap(options)
	if _, ok := opts["autoUpdates"]; ok && !supportsImageAutoUpdates(imageName) {
		panic("Image auto updates are only supported for Docker Hub and GHCR images.")
	}
	out := map[string]any{"type": "image", "image": imageName}
	for k, v := range opts {
		out[k] = v
	}
	return prune(out)
}

func Template(templateName string, options ...map[string]any) map[string]any {
	out := map[string]any{"type": "template", "template": templateName}
	for k, v := range firstMap(options) {
		out[k] = v
	}
	return prune(out)
}

func Empty(options ...map[string]any) map[string]any {
	out := map[string]any{"type": "empty"}
	for k, v := range firstMap(options) {
		out[k] = v
	}
	return prune(out)
}

func Fn(name string, config ServiceConfig) Service {
	svc := ServiceNamed(name, config)
	svc.node["kind"] = "function"
	return svc
}

func Postgres(name string, options ...map[string]any) Resource {
	return Database(name, "postgres", mergeOpts(map[string]any{
		"image":            "ghcr.io/railwayapp-templates/postgres-ssl:18",
		"output":           "DATABASE_URL",
		"defaultMountPath": "/var/lib/postgresql/data",
	}, options...))
}

func Mysql(name string, options ...map[string]any) Resource {
	return Database(name, "mysql", mergeOpts(map[string]any{
		"image":            "mysql:9",
		"output":           "MYSQL_URL",
		"defaultMountPath": "/var/lib/mysql",
	}, options...))
}

func Redis(name string, options ...map[string]any) Resource {
	return Database(name, "redis", mergeOpts(map[string]any{
		"image":            "railwayapp/redis:8.2",
		"output":           "REDIS_URL",
		"defaultMountPath": "/bitnami",
	}, options...))
}

func Mongo(name string, options ...map[string]any) Resource {
	return Database(name, "mongo", mergeOpts(map[string]any{
		"image":            "mongo:8",
		"output":           "MONGO_URL",
		"defaultMountPath": "/data/db",
	}, options...))
}

func Database(name, engine string, options ...map[string]any) Resource {
	opts := firstMap(options)
	imageName, _ := opts["image"].(string)
	output := "DATABASE_URL"
	if value, ok := opts["output"].(string); ok && value != "" {
		output = value
	}
	node := map[string]any{
		"address": "database." + name,
		"type":    "database",
		"kind":    "database",
		"engine":  engine,
		"name":    name,
		"image":   imageName,
		"output":  output,
		"source":  Image(imageName),
	}
	if mount, ok := opts["defaultMountPath"]; ok {
		node["defaultMountPath"] = mount
	}
	if region, ok := opts["region"].(string); ok && region != "" {
		node["deploy"] = map[string]any{
			"multiRegionConfig": map[string]any{region: map[string]any{"numReplicas": 1}},
		}
	}
	return Resource{node: node}
}

func Volume(name string, config ...map[string]any) Resource {
	return Resource{node: map[string]any{
		"address": "volume." + name,
		"type":    "volume",
		"name":    name,
		"config":  firstMap(config),
	}}
}

func Bucket(name string, config ...map[string]any) Resource {
	return Resource{node: map[string]any{
		"address": "bucket." + name,
		"type":    "bucket",
		"name":    name,
		"config":  firstMap(config),
	}}
}

func Group(name string, resources []any, options ...map[string]any) []any {
	node := Resource{node: mergeMaps(map[string]any{
		"address": "group." + name,
		"type":    "group",
		"name":    name,
	}, firstMap(options))}
	out := []any{node}
	for _, item := range flatten(resources) {
		out = append(out, withGroupID(item, name))
	}
	return out
}

func Ref(resource any, output string) map[string]any {
	return map[string]any{"type": "reference", "resource": addressOf(resource), "output": output}
}

func Preserve() map[string]any {
	return map[string]any{"type": "preserve"}
}

type Context struct {
	Command         string
	ProjectID       string
	ProjectName     string
	EnvironmentID   string
	Environment     string
	EnvironmentName string
}

func NewContext(input Context) Context {
	environment := input.Environment
	if environment == "" {
		environment = input.EnvironmentName
	}
	input.Environment = environment
	input.EnvironmentName = environment
	return input
}

func (c Context) RandomString(label string, bytes int) string {
	if label == "" {
		label = "random"
	}
	if bytes <= 0 {
		bytes = 12
	}
	environment := c.Environment
	if environment == "" {
		environment = "default"
	}
	sum := sha256.Sum256([]byte("railway-iac:" + environment + ":" + label))
	return hex.EncodeToString(sum[:])[: bytes*2]
}

func (c Context) IsEnvironment(name string) bool {
	return c.Environment == name
}

func Shared(name string) map[string]any {
	return map[string]any{"type": "sharedReference", "name": name}
}

func serviceNode(name string, config ServiceConfig) map[string]any {
	if config == nil {
		config = ServiceConfig{}
	}
	source := normalizeSource(config["source"], firstString(config["root"], config["rootDirectory"]))
	kind := "empty"
	if source != nil {
		switch source["type"] {
		case "github":
			kind = "github"
		case "image":
			kind = "docker-image"
		case "template":
			kind = "template"
		}
	}
	node := map[string]any{
		"address": "service." + name,
		"type":    "service",
		"kind":    kind,
		"name":    name,
	}
	if source != nil {
		node["source"] = source
	}
	if build := normalizeBuild(config["build"]); build != nil {
		node["build"] = build
	}
	if deploy := normalizeDeploy(config); deploy != nil {
		node["deploy"] = deploy
	}
	if networking := normalizeNetworking(config); networking != nil {
		node["networking"] = networking
	}
	variables := mergeVariableMaps(asMap(config["variables"]), asMap(config["env"]))
	if len(variables) > 0 {
		node["variables"] = normalizeVariables(variables)
	}
	for k, v := range normalizeVolumeMounts(config["volumeMounts"]) {
		node[k] = v
	}
	for _, key := range []string{"configFile", "parentServiceId", "groupId", "clusterRole", "replicaConfig", "clusterDisplay"} {
		if value, ok := config[key]; ok && value != nil {
			node[key] = value
		}
	}
	// Per-environment tracing switches: {"enabled": bool, "autoInstrumentation": bool}.
	// Railway serialises an untraced service without a block, so no switches is no block.
	if tracing := prune(asMap(config["tracing"])); tracing != nil {
		node["tracing"] = tracing
	}
	return node
}

func normalizeSource(source any, rootDirectory string) map[string]any {
	src := asMap(source)
	if src == nil {
		if rootDirectory != "" {
			return map[string]any{"type": "empty", "rootDirectory": rootDirectory}
		}
		return nil
	}
	if _, ok := src["type"]; ok {
		if src["rootDirectory"] == nil && rootDirectory != "" {
			src["rootDirectory"] = rootDirectory
		}
		return prune(src)
	}
	if repo, ok := src["repo"].(string); ok && repo != "" {
		branch := "main"
		if value, ok := src["branch"].(string); ok && value != "" {
			branch = value
		}
		return prune(map[string]any{"type": "github", "repo": repo, "branch": branch, "rootDirectory": emptyToNil(rootDirectory)})
	}
	if imageName, ok := src["image"].(string); ok && imageName != "" {
		return prune(map[string]any{"type": "image", "image": imageName, "rootDirectory": emptyToNil(rootDirectory)})
	}
	if rootDirectory != "" {
		return map[string]any{"type": "empty", "rootDirectory": rootDirectory}
	}
	return nil
}

func normalizeBuild(build any) map[string]any {
	if command, ok := build.(string); ok {
		return map[string]any{"buildCommand": command}
	}
	return prune(asMap(build))
}

func normalizeDeploy(config ServiceConfig) map[string]any {
	run := asMap(config["run"])
	deploy := asMap(config["deploy"])
	if deploy == nil {
		deploy = map[string]any{}
	}
	preDeploy := config["preDeploy"]
	if preDeploy == nil {
		preDeploy = config["preDeployCommand"]
	}
	if preDeploy == nil {
		preDeploy = run["preDeploy"]
	}
	if command, ok := preDeploy.(string); ok {
		preDeploy = []any{command}
	}
	replicas := normalizeReplicas(config["replicas"], config["regions"])
	payload := map[string]any{}
	for k, v := range deploy {
		payload[k] = v
	}
	payload["startCommand"] = firstNonNil(config["start"], config["startCommand"], run["command"], deploy["startCommand"])
	if preDeploy != nil {
		payload["preDeployCommand"] = preDeploy
	} else if value, ok := deploy["preDeployCommand"]; ok {
		payload["preDeployCommand"] = value
	}
	payload["healthcheckPath"] = firstNonNil(config["healthcheck"], config["healthcheckPath"], run["healthcheck"], deploy["healthcheckPath"])
	payload["healthcheckTimeout"] = firstNonNil(config["healthcheckTimeout"], run["healthcheckTimeout"], deploy["healthcheckTimeout"])
	for k, v := range replicas {
		payload[k] = v
	}
	return prune(payload)
}

func normalizeReplicas(replicas any, regions any) map[string]any {
	switch value := replicas.(type) {
	case int:
		return map[string]any{"numReplicas": value}
	case map[string]any:
		return map[string]any{"multiRegionConfig": normalizeRegions(value)}
	}
	if regs := asMap(regions); regs != nil {
		return map[string]any{"multiRegionConfig": normalizeRegions(regs)}
	}
	return nil
}

func normalizeRegions(regions map[string]any) map[string]any {
	out := map[string]any{}
	for region, value := range regions {
		if count, ok := value.(int); ok {
			out[region] = map[string]any{"numReplicas": count}
			continue
		}
		cfg := asMap(value)
		out[region] = prune(map[string]any{
			"numReplicas":       firstNonNil(cfg["count"], cfg["replicas"]),
			"stackerAssignment": cfg["stacker"],
		})
		if out[region] == nil {
			out[region] = map[string]any{}
		}
	}
	return out
}

func normalizeNetworking(config ServiceConfig) map[string]any {
	var customDomains map[string]any
	if domains, ok := config["domains"].([]any); ok {
		customDomains = map[string]any{}
		for _, domain := range domains {
			if name, ok := domain.(string); ok {
				customDomains[name] = map[string]any{"port": 8080}
				continue
			}
			item := asMap(domain)
			port := 8080
			if value, ok := item["port"].(int); ok {
				port = value
			}
			customDomains[item["domain"].(string)] = map[string]any{"port": port}
		}
	}
	var tcpProxies map[string]any
	if tcp, ok := config["tcp"].([]any); ok {
		tcpProxies = map[string]any{}
		for _, port := range tcp {
			tcpProxies[stringify(port)] = map[string]any{}
		}
	} else if proxies, ok := config["tcpProxies"].([]any); ok {
		tcpProxies = map[string]any{}
		for _, port := range proxies {
			tcpProxies[stringify(port)] = map[string]any{}
		}
	}
	payload := asMap(config["networking"])
	if payload == nil {
		payload = map[string]any{}
	}
	payload["customDomains"] = customDomains
	payload["tcpProxies"] = tcpProxies
	return prune(payload)
}

func normalizeVariables(variables map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range variables {
		if text, ok := value.(string); ok {
			out[key] = map[string]any{"type": "literal", "value": text}
			continue
		}
		if item := asMap(value); item != nil {
			if _, ok := item["type"]; ok {
				out[key] = item
				continue
			}
			out[key] = map[string]any{"type": "raw", "value": value}
			continue
		}
		out[key] = map[string]any{"type": "raw", "value": value}
	}
	return out
}

func normalizeVolumeMounts(volumeMounts any) map[string]any {
	mounts := asMap(volumeMounts)
	if mounts == nil {
		return nil
	}
	raw := map[string]any{}
	attachments := map[string]any{}
	for key, value := range mounts {
		node := graphOf(value)
		if node != nil && node["type"] == "volume" {
			attachments[node["name"].(string)] = prune(map[string]any{
				"volume":       node["address"],
				"mountPath":    key,
				"volumeConfig": node["config"],
			})
			continue
		}
		raw[key] = value
	}
	return prune(map[string]any{"volumeMounts": emptyMapToNil(raw), "volumeAttachments": emptyMapToNil(attachments)})
}

func supportsImageAutoUpdates(imageName string) bool {
	normalized := strings.TrimSpace(strings.ToLower(imageName))
	if normalized == "" {
		return false
	}
	if !strings.Contains(normalized, "/") {
		return true
	}
	registry, _, _ := strings.Cut(normalized, "/")
	return (!strings.Contains(registry, ".") && !strings.Contains(registry, ":") && registry != "localhost") ||
		registry == "docker.io" ||
		registry == "ghcr.io"
}

func graphOf(value any) map[string]any {
	switch v := value.(type) {
	case Service:
		return v.Graph()
	case Resource:
		return v.Graph()
	case map[string]any:
		return v
	default:
		return map[string]any{"value": value}
	}
}

func withGroupID(value any, name string) any {
	switch v := value.(type) {
	case Service:
		node := v.Graph()
		node["groupId"] = name
		v.node = node
		return v
	case Resource:
		node := v.Graph()
		node["groupId"] = name
		v.node = node
		return v
	case map[string]any:
		out := cloneMap(v)
		out["groupId"] = name
		return out
	default:
		return value
	}
}

func addressOf(resource any) string {
	switch v := resource.(type) {
	case Service:
		return v.Address()
	case Resource:
		return v.Address()
	case map[string]any:
		if address, ok := v["address"].(string); ok {
			return address
		}
	}
	return ""
}

func flatten(items []any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		if nested, ok := item.([]any); ok {
			out = append(out, flatten(nested)...)
			continue
		}
		out = append(out, item)
	}
	return out
}

func prune(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range value {
		if v != nil {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMap(value map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range value {
		out[k] = v
	}
	return out
}

func asMap(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case ServiceConfig:
		return cloneMap(v)
	default:
		return nil
	}
}

func firstMap(options []map[string]any) map[string]any {
	if len(options) == 0 || options[0] == nil {
		return map[string]any{}
	}
	return cloneMap(options[0])
}

func mergeOpts(base map[string]any, options ...map[string]any) map[string]any {
	return mergeMaps(base, firstMap(options))
}

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := cloneMap(base)
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func mergeVariableMaps(first, second map[string]any) map[string]any {
	if first == nil && second == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range first {
		out[k] = v
	}
	for k, v := range second {
		out[k] = v
	}
	return out
}

func firstString(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func emptyMapToNil(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func stringify(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}
