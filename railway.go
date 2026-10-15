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
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

// Image declares a Docker image source. Options are spread into the source
// node. The "autoUpdates" option takes an AutoUpdates value (or the equivalent
// raw map) and is validated; it is only supported for Docker Hub and GHCR
// images, Image panics otherwise.
func Image(imageName string, options ...map[string]any) map[string]any {
	opts := firstMap(options)
	if _, ok := opts["autoUpdates"]; ok && !supportsImageAutoUpdates(imageName) {
		panic("Image auto updates are only supported for Docker Hub and GHCR images.")
	}
	out := map[string]any{"type": "image", "image": imageName}
	for k, v := range opts {
		out[k] = v
	}
	if value, ok := opts["autoUpdates"]; ok {
		if normalized := normalizeAutoUpdates(value); normalized != nil {
			out["autoUpdates"] = normalized
		} else {
			delete(out, "autoUpdates")
		}
	}
	return prune(out)
}

// AutoUpdatesType is the image auto update policy.
type AutoUpdatesType string

const (
	// AutoUpdatesDisabled turns image auto updates off.
	AutoUpdatesDisabled AutoUpdatesType = "disabled"
	// AutoUpdatesPatch follows patch releases of the image tag.
	AutoUpdatesPatch AutoUpdatesType = "patch"
	// AutoUpdatesMinor follows minor and patch releases of the image tag.
	AutoUpdatesMinor AutoUpdatesType = "minor"
)

// AutoUpdates configures image auto updates for a Docker Hub or GHCR image
// source. Pass it as the "autoUpdates" option of Image:
//
//	railway.Image("nginx:latest", map[string]any{
//		"autoUpdates": railway.AutoUpdates{
//			Type:     railway.AutoUpdatesPatch,
//			Schedule: []railway.AutoUpdateWindow{{Day: 6, StartHour: 2, EndHour: 6}},
//		},
//	})
//
// Mirrors source.autoUpdates in the environment config: Type is the update
// policy, Schedule the weekly UTC maintenance windows updates may run in, and
// TagMode ("semver" or "sha") how new tags are matched. Zero values are
// omitted from the graph; a nil Schedule leaves the window unset.
type AutoUpdates struct {
	Type     AutoUpdatesType
	Schedule []AutoUpdateWindow
	TagMode  string
}

// AutoUpdateWindow is one weekly UTC window in which scheduled image auto
// updates may run. Same shape as the GraphQL AutoUpdateScheduleWindowInput.
type AutoUpdateWindow struct {
	// Day is the UTC weekday, 0 (Sunday) through 6 (Saturday).
	Day int
	// StartHour is the UTC hour the window opens (inclusive), 0 through 23.
	StartHour int
	// EndHour is the UTC hour the window closes (exclusive), 1 through 24. It
	// must be greater than StartHour.
	EndHour int
}

func (w AutoUpdateWindow) graph() map[string]any {
	if w.Day < 0 || w.Day > 6 {
		panic("Invalid maintenance window: day must be 0 (Sunday) through 6 (Saturday) UTC, got " + strconv.Itoa(w.Day) + ".")
	}
	if w.StartHour < 0 || w.StartHour > 23 {
		panic("Invalid maintenance window: startHour must be 0 through 23 UTC, got " + strconv.Itoa(w.StartHour) + ".")
	}
	if w.EndHour < 1 || w.EndHour > 24 {
		panic("Invalid maintenance window: endHour must be 1 through 24 UTC, got " + strconv.Itoa(w.EndHour) + ".")
	}
	if w.EndHour <= w.StartHour {
		panic("Invalid maintenance window: endHour must be greater than startHour.")
	}
	return map[string]any{"day": w.Day, "startHour": w.StartHour, "endHour": w.EndHour}
}

// normalizeAutoUpdates turns the "autoUpdates" option of Image into the graph
// shape, accepting the typed AutoUpdates struct or the raw map form, and
// validates the policy and every maintenance window either way.
func normalizeAutoUpdates(value any) map[string]any {
	var out map[string]any
	switch v := value.(type) {
	case nil:
		return nil
	case AutoUpdates:
		out = map[string]any{}
		if v.Type != "" {
			out["type"] = v.Type
		}
		if v.Schedule != nil {
			out["schedule"] = v.Schedule
		}
		if v.TagMode != "" {
			out["tagMode"] = v.TagMode
		}
	case *AutoUpdates:
		if v == nil {
			return nil
		}
		return normalizeAutoUpdates(*v)
	case map[string]any:
		out = cloneMap(v)
	default:
		panic("Image autoUpdates must be a railway.AutoUpdates value or a map[string]any.")
	}

	if raw, ok := out["type"]; ok && raw != nil {
		var policy string
		switch t := raw.(type) {
		case AutoUpdatesType:
			policy = string(t)
		case string:
			policy = t
		default:
			panic("Image autoUpdates.type must be \"disabled\", \"patch\" or \"minor\".")
		}
		switch AutoUpdatesType(policy) {
		case AutoUpdatesDisabled, AutoUpdatesPatch, AutoUpdatesMinor:
			out["type"] = policy
		default:
			panic("Image autoUpdates.type must be \"disabled\", \"patch\" or \"minor\", got " + strconv.Quote(policy) + ".")
		}
	}
	if raw, ok := out["tagMode"]; ok && raw != nil {
		mode, isString := raw.(string)
		if !isString || (mode != "semver" && mode != "sha") {
			panic("Image autoUpdates.tagMode must be \"semver\" or \"sha\".")
		}
	}
	if raw, ok := out["schedule"]; ok && raw != nil {
		out["schedule"] = normalizeAutoUpdateSchedule(raw)
	}
	return prune(out)
}

func normalizeAutoUpdateSchedule(value any) []any {
	var windows []any
	switch v := value.(type) {
	case []AutoUpdateWindow:
		for _, w := range v {
			windows = append(windows, w)
		}
	case []map[string]any:
		for _, w := range v {
			windows = append(windows, w)
		}
	case []any:
		windows = v
	default:
		panic("Image autoUpdates.schedule must be a list of railway.AutoUpdateWindow values.")
	}
	out := make([]any, 0, len(windows))
	for _, raw := range windows {
		var window AutoUpdateWindow
		switch w := raw.(type) {
		case AutoUpdateWindow:
			window = w
		case map[string]any:
			for key := range w {
				if key != "day" && key != "startHour" && key != "endHour" {
					panic("Invalid maintenance window: unknown field " + strconv.Quote(key) + ".")
				}
			}
			window = AutoUpdateWindow{
				Day:       windowField(w, "day"),
				StartHour: windowField(w, "startHour"),
				EndHour:   windowField(w, "endHour"),
			}
		default:
			panic("Image autoUpdates.schedule entries must be railway.AutoUpdateWindow values.")
		}
		out = append(out, window.graph())
	}
	return out
}

func windowField(window map[string]any, key string) int {
	raw, ok := window[key]
	if !ok {
		panic("Invalid maintenance window: missing " + key + ".")
	}
	value, ok := asInt(raw)
	if !ok {
		panic("Invalid maintenance window: " + key + " must be an integer.")
	}
	return value
}

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		return int(v), true
	case float32:
		if float32(int(v)) == v {
			return int(v), true
		}
	case float64:
		if float64(int(v)) == v {
			return int(v), true
		}
	}
	return 0, false
}

// ServiceCdnHtmlCaching says whether the edge caches HTML documents.
type ServiceCdnHtmlCaching string

const (
	// ServiceCdnHtmlCachingAuto follows the response headers.
	ServiceCdnHtmlCachingAuto ServiceCdnHtmlCaching = "auto"
	// ServiceCdnHtmlCachingForce caches HTML documents regardless of headers.
	ServiceCdnHtmlCachingForce ServiceCdnHtmlCaching = "force"
	// ServiceCdnHtmlCachingNever leaves HTML documents uncached.
	ServiceCdnHtmlCachingNever ServiceCdnHtmlCaching = "never"
)

// ServiceCdnPurgeOnDeploy says what a successful deploy purges from the cache.
type ServiceCdnPurgeOnDeploy string

const (
	// ServiceCdnPurgeOnDeployOff purges nothing.
	ServiceCdnPurgeOnDeployOff ServiceCdnPurgeOnDeploy = "off"
	// ServiceCdnPurgeOnDeployHtml purges HTML documents and keeps immutable
	// assets warm.
	ServiceCdnPurgeOnDeployHtml ServiceCdnPurgeOnDeploy = "html"
	// ServiceCdnPurgeOnDeployAll clears everything.
	ServiceCdnPurgeOnDeployAll ServiceCdnPurgeOnDeploy = "all"
)

// ServiceCdn configures CDN caching for the service's public domains in this
// environment. Settings live on the edge and apply without a deploy. Pass it
// as the "cdn" option of ServiceNamed or Fn:
//
//	railway.ServiceNamed("docs", railway.ServiceConfig{
//		"cdn": railway.ServiceCdn{
//			Enabled:           true,
//			DefaultTtlSeconds: 86400,
//			HtmlCaching:       railway.ServiceCdnHtmlCachingForce,
//		},
//	})
//
// Mirrors services[id].cdn in the environment config. Railway serialises the
// block only while caching is on, and only the knobs that differ from the
// defaults, so an omitted block, {Enabled: false} and nil all mean off and
// {Enabled: true} is a service caching with the defaults. The SDK does not
// fill defaults in: zero values other than Enabled are omitted from the graph.
type ServiceCdn struct {
	// Enabled caches responses from the service's public domains at Railway's
	// edge. Needs a public domain; cannot be combined with Railway
	// Authentication.
	Enabled bool
	// DefaultTtlSeconds is how long the edge keeps a cacheable response without
	// Cache-Control headers of its own, in seconds. Defaults to 7200; zero
	// leaves the default (the edge accepts 30 minutes to a day).
	DefaultTtlSeconds int
	// HtmlCaching says whether HTML documents are cached. Defaults to auto.
	HtmlCaching ServiceCdnHtmlCaching
	// StaleWhileRevalidate honours stale-while-revalidate Cache-Control
	// directives, serving a stale copy while the edge refreshes it. Defaults
	// to true; nil leaves the default.
	StaleWhileRevalidate *bool
	// PurgeOnDeploy says what a successful deploy purges from the cache.
	// Defaults to html.
	PurgeOnDeploy ServiceCdnPurgeOnDeploy
}

// normalizeServiceCdn turns the "cdn" option of a service into the graph
// shape, accepting the typed ServiceCdn struct or the raw map form, and
// validates the knobs either way. Unknown keys in a raw map pass through so a
// newer `railway config pull` still evaluates.
func normalizeServiceCdn(value any) map[string]any {
	var out map[string]any
	switch v := value.(type) {
	case nil:
		return nil
	case ServiceCdn:
		out = map[string]any{"enabled": v.Enabled}
		if v.DefaultTtlSeconds != 0 {
			out["defaultTtlSeconds"] = v.DefaultTtlSeconds
		}
		if v.HtmlCaching != "" {
			out["htmlCaching"] = v.HtmlCaching
		}
		if v.StaleWhileRevalidate != nil {
			out["staleWhileRevalidate"] = *v.StaleWhileRevalidate
		}
		if v.PurgeOnDeploy != "" {
			out["purgeOnDeploy"] = v.PurgeOnDeploy
		}
	case *ServiceCdn:
		if v == nil {
			return nil
		}
		return normalizeServiceCdn(*v)
	case map[string]any:
		out = cloneMap(v)
	default:
		panic("Service cdn must be a railway.ServiceCdn value or a map[string]any.")
	}

	for _, key := range []string{"enabled", "staleWhileRevalidate"} {
		if raw, ok := out[key]; ok && raw != nil {
			if _, isBool := raw.(bool); !isBool {
				panic("Service cdn." + key + " must be a bool.")
			}
		}
	}
	if raw, ok := out["defaultTtlSeconds"]; ok && raw != nil {
		ttl, isInt := asInt(raw)
		if !isInt || ttl < 0 {
			panic("Service cdn.defaultTtlSeconds must be a non-negative integer.")
		}
		out["defaultTtlSeconds"] = ttl
	}
	if raw, ok := out["htmlCaching"]; ok && raw != nil {
		var mode string
		switch m := raw.(type) {
		case ServiceCdnHtmlCaching:
			mode = string(m)
		case string:
			mode = m
		}
		switch ServiceCdnHtmlCaching(mode) {
		case ServiceCdnHtmlCachingAuto, ServiceCdnHtmlCachingForce, ServiceCdnHtmlCachingNever:
			out["htmlCaching"] = mode
		default:
			panic("Service cdn.htmlCaching must be \"auto\", \"force\" or \"never\", got " + strconv.Quote(mode) + ".")
		}
	}
	if raw, ok := out["purgeOnDeploy"]; ok && raw != nil {
		var mode string
		switch m := raw.(type) {
		case ServiceCdnPurgeOnDeploy:
			mode = string(m)
		case string:
			mode = m
		}
		switch ServiceCdnPurgeOnDeploy(mode) {
		case ServiceCdnPurgeOnDeployOff, ServiceCdnPurgeOnDeployHtml, ServiceCdnPurgeOnDeployAll:
			out["purgeOnDeploy"] = mode
		default:
			panic("Service cdn.purgeOnDeploy must be \"off\", \"html\" or \"all\", got " + strconv.Quote(mode) + ".")
		}
	}
	return prune(out)
}

// EdgeRuleAction is what the edge does when a rule's condition matches.
type EdgeRuleAction string

const (
	// EdgeRuleActionBlock answers the request at the edge with an error.
	EdgeRuleActionBlock EdgeRuleAction = "block"
	// EdgeRuleActionAllow lets the request through to the service.
	EdgeRuleActionAllow EdgeRuleAction = "allow"
	// EdgeRuleActionChallenge makes the client pass a challenge first.
	EdgeRuleActionChallenge EdgeRuleAction = "challenge"
	// EdgeRuleActionRedirect answers the request with a redirect.
	EdgeRuleActionRedirect EdgeRuleAction = "redirect"
	// EdgeRuleActionCacheOverride overrides the CDN's caching for the request.
	EdgeRuleActionCacheOverride EdgeRuleAction = "cache_override"
)

// EdgeRuleMode is how the edge applies a matching rule.
type EdgeRuleMode string

const (
	// EdgeRuleModeEnforce applies the rule's action. The only mode today, and
	// the default.
	EdgeRuleModeEnforce EdgeRuleMode = "enforce"
)

// EdgeRuleExpr is a rule's condition: one clause over a request attribute
// (Attr, Op, Value and, for http.header, Key), or exactly one of the And, Or
// and Not combinators over other conditions. Attributes and operators come
// from the edge's registry (ipv4.src, http.host, http.path and http.header,
// with eq, neq, in, not_in, contains and matches); the edge validates them
// when the change is committed.
//
//	railway.EdgeRuleExpr{And: []railway.EdgeRuleExpr{
//		{Attr: "http.path", Op: "matches", Value: "/admin/*"},
//		{Not: &railway.EdgeRuleExpr{Attr: "ipv4.src", Op: "in", Value: []string{"10.0.0.0/8"}}},
//	}}
type EdgeRuleExpr struct {
	// And matches when every condition matches.
	And []EdgeRuleExpr
	// Or matches when any condition matches.
	Or []EdgeRuleExpr
	// Not matches when the condition does not.
	Not *EdgeRuleExpr
	// Attr is the request attribute: ipv4.src, http.host, http.path or
	// http.header.
	Attr string
	// Op is the comparison: eq, neq, in, not_in, contains or matches (glob),
	// as the attribute allows.
	Op string
	// Value is the string to compare with, or the []string for in and not_in.
	Value any
	// Key is the header name; required for http.header only.
	Key string
}

// EdgeRuleThen is what the edge does when a rule's condition matches.
type EdgeRuleThen struct {
	Action EdgeRuleAction
	// Params are the action's parameters: status and body for block; status
	// and either location or host (plus preserve_path and preserve_query) for
	// redirect; ttl_seconds or bypass for cache_override. The SDK passes them
	// through; the edge validates them when the change is committed.
	Params map[string]any
}

// EdgeRule is one edge rule. Rules carry no id: the edge assigns one, and a
// staged change keeps a live rule's id when the priority matches. Enabled and
// Mode are omitted at their defaults.
type EdgeRule struct {
	// Description is free text.
	Description string
	// Priority is the evaluation order, lowest first. Unique within the
	// service.
	Priority int
	// Enabled false keeps the rule without evaluating it. Defaults to true;
	// nil leaves the default.
	Enabled *bool
	// Mode defaults to enforce, the only mode today; empty leaves the default.
	Mode EdgeRuleMode
	If   EdgeRuleExpr
	Then EdgeRuleThen
}

// EdgeRules is the service's edge rules in this environment, evaluated at the
// edge before a request reaches the service. Pass it as the "edgeRules"
// option of ServiceNamed or Fn:
//
//	railway.ServiceNamed("web", railway.ServiceConfig{
//		"edgeRules": railway.EdgeRules{{
//			Priority: 0,
//			If:       railway.EdgeRuleExpr{Attr: "http.host", Op: "eq", Value: "old.example.com"},
//			Then: railway.EdgeRuleThen{
//				Action: railway.EdgeRuleActionRedirect,
//				Params: map[string]any{"status": 301, "host": "example.com", "preserve_path": true},
//			},
//		}},
//	})
//
// Mirrors services[id].edgeRules in the environment config. Railway
// serialises the list sorted by priority and without default-valued fields,
// so a reordered or default-laden copy does not plan as drift; the SDK does
// not sort or strip defaults either. An omitted key, nil and an empty list
// all mean no rules; the list is replaced as a whole and applies without a
// deploy.
type EdgeRules []EdgeRule

// normalizeEdgeRules turns the "edgeRules" option of a service into the graph
// shape, accepting the typed EdgeRules list or the raw list of maps that
// `railway config pull` writes, and validates the shape of each rule either
// way. Unknown keys in a raw map pass through so a newer `railway config pull`
// still evaluates.
func normalizeEdgeRules(value any) []any {
	var items []any
	switch v := value.(type) {
	case nil:
		return nil
	case EdgeRules:
		for _, rule := range v {
			items = append(items, rule)
		}
	case []EdgeRule:
		return normalizeEdgeRules(EdgeRules(v))
	case *EdgeRules:
		if v == nil {
			return nil
		}
		return normalizeEdgeRules(*v)
	case []any:
		items = v
	case []map[string]any:
		for _, rule := range v {
			items = append(items, rule)
		}
	default:
		panic("Service edgeRules must be a railway.EdgeRules value or a list of map[string]any.")
	}
	if len(items) == 0 {
		return nil
	}
	out := make([]any, 0, len(items))
	for i, item := range items {
		out = append(out, normalizeEdgeRule(item, "edgeRules["+strconv.Itoa(i)+"]"))
	}
	return out
}

func normalizeEdgeRule(value any, path string) map[string]any {
	var out map[string]any
	switch v := value.(type) {
	case EdgeRule:
		out = map[string]any{"priority": v.Priority, "if": v.If, "then": v.Then}
		if v.Description != "" {
			out["description"] = v.Description
		}
		if v.Enabled != nil {
			out["enabled"] = *v.Enabled
		}
		if v.Mode != "" {
			out["mode"] = v.Mode
		}
	case *EdgeRule:
		if v == nil {
			panic("Service " + path + " must not be nil.")
		}
		return normalizeEdgeRule(*v, path)
	case map[string]any:
		out = cloneMap(v)
	default:
		panic("Service " + path + " must be a railway.EdgeRule value or a map[string]any.")
	}

	if raw, ok := out["description"]; ok && raw != nil {
		if _, isString := raw.(string); !isString {
			panic("Service " + path + ".description must be a string.")
		}
	}
	raw, ok := out["priority"]
	if !ok || raw == nil {
		panic("Service " + path + " is missing priority.")
	}
	priority, isInt := asInt(raw)
	if !isInt {
		panic("Service " + path + ".priority must be an integer.")
	}
	out["priority"] = priority
	if raw, ok := out["enabled"]; ok && raw != nil {
		if _, isBool := raw.(bool); !isBool {
			panic("Service " + path + ".enabled must be a bool.")
		}
	}
	if raw, ok := out["mode"]; ok && raw != nil {
		var mode string
		switch m := raw.(type) {
		case EdgeRuleMode:
			mode = string(m)
		case string:
			mode = m
		}
		if EdgeRuleMode(mode) != EdgeRuleModeEnforce {
			panic("Service " + path + ".mode must be \"enforce\", got " + strconv.Quote(mode) + ".")
		}
		out["mode"] = mode
	}
	if raw, ok := out["if"]; !ok || raw == nil {
		panic("Service " + path + " is missing if.")
	}
	out["if"] = normalizeEdgeRuleExpr(out["if"], path+".if")
	if raw, ok := out["then"]; !ok || raw == nil {
		panic("Service " + path + " is missing then.")
	}
	out["then"] = normalizeEdgeRuleThen(out["then"], path+".then")
	return prune(out)
}

func normalizeEdgeRuleExpr(value any, path string) map[string]any {
	var out map[string]any
	switch v := value.(type) {
	case EdgeRuleExpr:
		out = map[string]any{}
		if v.And != nil {
			out["and"] = v.And
		}
		if v.Or != nil {
			out["or"] = v.Or
		}
		if v.Not != nil {
			out["not"] = *v.Not
		}
		if v.Attr != "" || v.Op != "" || v.Value != nil || v.Key != "" {
			out["attr"] = v.Attr
			out["op"] = v.Op
			out["value"] = v.Value
			if v.Key != "" {
				out["key"] = v.Key
			}
		}
	case *EdgeRuleExpr:
		if v == nil {
			panic("Service " + path + " must not be nil.")
		}
		return normalizeEdgeRuleExpr(*v, path)
	case map[string]any:
		out = prune(cloneMap(v))
	default:
		panic("Service " + path + " must be a railway.EdgeRuleExpr value or a map[string]any.")
	}

	forms := 0
	for _, key := range []string{"and", "or", "not"} {
		if _, ok := out[key]; ok {
			forms++
		}
	}
	isClause := false
	for _, key := range []string{"attr", "op", "value", "key"} {
		if _, ok := out[key]; ok {
			isClause = true
		}
	}
	if isClause {
		forms++
	}
	if forms != 1 {
		panic("Service " + path + " must be one clause {attr, op, value} or exactly one of and, or, not.")
	}

	for _, key := range []string{"and", "or"} {
		raw, ok := out[key]
		if !ok {
			continue
		}
		var children []any
		switch list := raw.(type) {
		case []EdgeRuleExpr:
			for _, child := range list {
				children = append(children, child)
			}
		case []map[string]any:
			for _, child := range list {
				children = append(children, child)
			}
		case []any:
			children = list
		default:
			panic("Service " + path + "." + key + " must be a list of conditions.")
		}
		normalized := make([]any, 0, len(children))
		for i, child := range children {
			normalized = append(normalized, normalizeEdgeRuleExpr(child, path+"."+key+"["+strconv.Itoa(i)+"]"))
		}
		out[key] = normalized
		return out
	}
	if raw, ok := out["not"]; ok {
		out["not"] = normalizeEdgeRuleExpr(raw, path+".not")
		return out
	}

	attr, _ := out["attr"].(string)
	if attr == "" {
		panic("Service " + path + ".attr must be a non-empty string.")
	}
	op, _ := out["op"].(string)
	if op == "" {
		panic("Service " + path + ".op must be a non-empty string.")
	}
	switch v := out["value"].(type) {
	case string:
	case []string:
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			text, isString := item.(string)
			if !isString {
				panic("Service " + path + ".value must be a string or a list of strings.")
			}
			values = append(values, text)
		}
		out["value"] = values
	default:
		panic("Service " + path + ".value must be a string or a list of strings.")
	}
	if raw, ok := out["key"]; ok {
		if _, isString := raw.(string); !isString {
			panic("Service " + path + ".key must be a string.")
		}
	}
	return out
}

func normalizeEdgeRuleThen(value any, path string) map[string]any {
	var out map[string]any
	switch v := value.(type) {
	case EdgeRuleThen:
		out = map[string]any{"action": v.Action}
		if v.Params != nil {
			out["params"] = cloneMap(v.Params)
		}
	case *EdgeRuleThen:
		if v == nil {
			panic("Service " + path + " must not be nil.")
		}
		return normalizeEdgeRuleThen(*v, path)
	case map[string]any:
		out = cloneMap(v)
	default:
		panic("Service " + path + " must be a railway.EdgeRuleThen value or a map[string]any.")
	}

	var action string
	switch a := out["action"].(type) {
	case EdgeRuleAction:
		action = string(a)
	case string:
		action = a
	}
	switch EdgeRuleAction(action) {
	case EdgeRuleActionBlock, EdgeRuleActionAllow, EdgeRuleActionChallenge, EdgeRuleActionRedirect, EdgeRuleActionCacheOverride:
		out["action"] = action
	default:
		panic("Service " + path + ".action must be \"block\", \"allow\", \"challenge\", \"redirect\" or \"cache_override\", got " + strconv.Quote(action) + ".")
	}
	if raw, ok := out["params"]; ok && raw != nil {
		params := asMap(raw)
		if params == nil {
			panic("Service " + path + ".params must be a map[string]any.")
		}
		out["params"] = params
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

// FunctionImagePrefix is the registry prefix shared by every Railway Function
// runtime image. Railway recognises a service as a function by it.
const FunctionImagePrefix = "ghcr.io/railwayapp/function-"

// FunctionImages maps a runtime name to the image Fn deploys for it. The
// default runtime is "bun".
var FunctionImages = map[string]string{
	"bun": FunctionImagePrefix + "bun:1.4.0",
}

// functionStartCommandPrefix is what run.sh in the function image expects in
// front of the base64 source.
const functionStartCommandPrefix = "./run.sh "

// FunctionSourceMaxBytes is the ceiling on a function's source, in UTF-8 bytes:
// 98295, just under 96 KB. The whole base64 payload is one argv entry of the
// container's start command, the kernel caps a single argument at 128 KiB, and
// the base64 has to fit after the "./run.sh " prefix. Railway derives the same
// limit and rejects larger functions on apply; Fn and FunctionStartCommand
// panic first.
const FunctionSourceMaxBytes = (128*1024 - 1 - len(functionStartCommandPrefix)) / 4 * 3

// Fn declares a Railway Function: a single-file service that runs on a
// function runtime image with no repository or build step. It accepts every
// ServiceNamed config key plus two of its own:
//
//   - "codeFile": path of the function's source file. A relative path is
//     resolved against the directory of the Go file that calls Fn (the config
//     file, so "hook.ts" is the file next to railway.go; a helper in another
//     file resolves against that helper, and a -trimpath build falls back to
//     the working directory, which the CLI sets to the config directory). Fn
//     reads the file when the config is evaluated and encodes it into the
//     deploy start command (see FunctionStartCommand), so "codeFile" and any
//     start command ("start", "startCommand", "run.command" or
//     "deploy.startCommand") are mutually exclusive.
//   - "runtime": a key of FunctionImages, "bun" by default. Fn sets the source
//     to that runtime's image. To pin another image version, pass "source"
//     with Image("ghcr.io/railwayapp/function-bun:1.2.16") instead of "runtime".
//
// Fn panics when the file is missing, empty or over FunctionSourceMaxBytes,
// on an unknown runtime, or on "runtime" combined with an explicit "source".
func Fn(name string, config ServiceConfig) Service {
	input := asMap(config)
	if input == nil {
		input = map[string]any{}
	}
	codeFile, hasCodeFile := input["codeFile"]
	runtimeName, hasRuntime := input["runtime"]
	delete(input, "codeFile")
	delete(input, "runtime")
	if hasCodeFile {
		path, ok := codeFile.(string)
		if !ok || path == "" {
			panic("Function codeFile must be a non-empty path.")
		}
		if firstNonNil(input["start"], input["startCommand"], asMap(input["run"])["command"], asMap(input["deploy"])["startCommand"]) != nil {
			panic("Function codeFile and a start command are mutually exclusive: the code is the start command.")
		}
		input["start"] = FunctionStartCommand(readFunctionCode(resolveCallerPath(path)))
	}
	if hasRuntime && input["source"] != nil {
		panic("Function runtime and an explicit source are mutually exclusive: pass the runtime image as the source instead.")
	}
	if (hasCodeFile || hasRuntime) && input["source"] == nil {
		input["source"] = Image(functionImage(runtimeName))
	}
	svc := ServiceNamed(name, ServiceConfig(input))
	svc.Config = config
	svc.node["kind"] = "function"
	return svc
}

// FunctionStartCommand is the deploy start command that carries a function's
// source: "./run.sh <base64 code>". The runtime image's run.sh decodes the
// argument, writes it to index.tsx and runs it. Fn calls this with the
// contents of "codeFile"; use it directly to build a function service by hand.
// It panics when the code is over FunctionSourceMaxBytes, with the error
// Railway returns.
func FunctionStartCommand(code string) string {
	if len(code) > FunctionSourceMaxBytes {
		panic(fmt.Sprintf("Function source code is %d bytes; the limit is %d bytes", len(code), FunctionSourceMaxBytes))
	}
	return functionStartCommandPrefix + base64.StdEncoding.EncodeToString([]byte(code))
}

func readFunctionCode(path string) string {
	code, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("Function codeFile %s: %v", path, err))
	}
	if len(code) == 0 {
		panic(fmt.Sprintf("Function codeFile %s is empty.", path))
	}
	return string(code)
}

// resolveCallerPath makes a relative path absolute against the directory of
// the user's Go file that called into this package: the config file under
// `go run`, which records absolute source paths. Without one (trimpath, or a
// caller that cannot be determined) the path is left for the OS to resolve
// against the working directory, which the CLI sets to the config directory.
func resolveCallerPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	// Frame 0 is this function, 1 is Fn, 2 is the user's call site.
	if _, file, _, ok := runtime.Caller(2); ok && filepath.IsAbs(file) {
		return filepath.Join(filepath.Dir(file), path)
	}
	return path
}

func functionImage(requested any) string {
	if requested == nil {
		requested = "bun"
	}
	if name, ok := requested.(string); ok {
		if image, found := FunctionImages[name]; found {
			return image
		}
	}
	known := make([]string, 0, len(FunctionImages))
	for key := range FunctionImages {
		known = append(known, key)
	}
	sort.Strings(known)
	panic(fmt.Sprintf("Unknown function runtime %q; supported runtimes: %s.", fmt.Sprint(requested), strings.Join(known, ", ")))
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
	// Per-environment CDN caching: see ServiceCdn. Railway serialises an
	// uncached service without a block, so no knobs is no block.
	if cdn := normalizeServiceCdn(config["cdn"]); cdn != nil {
		node["cdn"] = cdn
	}
	// Per-environment edge rules: see EdgeRules. Railway canonicalises the
	// list (sorted by priority, defaults dropped), so it passes through as
	// written; no rules is no list.
	if edgeRules := normalizeEdgeRules(config["edgeRules"]); edgeRules != nil {
		node["edgeRules"] = edgeRules
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
