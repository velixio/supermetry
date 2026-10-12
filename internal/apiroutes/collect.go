package apiroutes

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
)

type function struct {
	decl *ast.FuncDecl
	file string
}

type handlerInfo struct {
	name string
	auth string
}

type collectEnv struct {
	strings  map[string]string
	handlers map[string]handlerInfo
}

type collector struct {
	fset      *token.FileSet
	constants map[string]string
	functions map[string]function
	routes    map[string]Route
	unknown   map[string]struct{}
	resolved  map[string]struct{}
	active    map[string]bool
}

// Collect finds route registrations in the supplied Go package directories.
func Collect(dirs ...string) ([]Route, []string, error) {
	var routes []Route
	var unknown []string
	for _, dir := range dirs {
		found, unresolved, err := collectDirectory(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("collect %s: %w", dir, err)
		}
		routes = append(routes, found...)
		unknown = append(unknown, unresolved...)
	}
	return Deduplicate(routes), uniqueStrings(unknown), nil
}

func collectDirectory(dir string) ([]Route, []string, error) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".gen.go")
	}, 0)
	if err != nil {
		return nil, nil, err
	}
	c := &collector{
		fset: fset, constants: make(map[string]string), functions: make(map[string]function),
		routes: make(map[string]Route), unknown: make(map[string]struct{}), resolved: make(map[string]struct{}), active: make(map[string]bool),
	}
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			c.readConstants(file)
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
					c.functions[fn.Name.Name] = function{decl: fn, file: filename}
				}
			}
		}
	}
	for _, name := range sortedFunctionNames(c.functions) {
		c.analyzeFunction(name, collectEnv{strings: cloneStrings(c.constants), handlers: make(map[string]handlerInfo)})
	}
	routes := make([]Route, 0, len(c.routes))
	for _, route := range c.routes {
		routes = append(routes, route)
	}
	sortRoutes(routes)
	unknown := sortedKeys(c.unknown)
	return routes, unknown, nil
}

func (c *collector) readConstants(file *ast.File) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if i < len(value.Values) {
					if text, ok := evalString(value.Values[i], c.constants); ok {
						c.constants[name.Name] = text
					}
				}
			}
		}
	}
}

func (c *collector) analyzeFunction(name string, env collectEnv) {
	fn, ok := c.functions[name]
	if !ok || c.active[name] {
		return
	}
	c.active[name] = true
	defer delete(c.active, name)
	c.analyzeBlock(fn.decl.Body.List, env, fn.file)
}

func (c *collector) analyzeBlock(statements []ast.Stmt, env collectEnv, filename string) {
	for _, statement := range statements {
		switch stmt := statement.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(stmt.Rhs) {
					continue
				}
				if value, ok := evalString(stmt.Rhs[i], env.strings); ok {
					env.strings[ident.Name] = value
				}
				env.handlers[ident.Name] = inspectHandler(stmt.Rhs[i], env.handlers)
			}
		case *ast.ExprStmt:
			if call, ok := stmt.X.(*ast.CallExpr); ok {
				c.analyzeCall(call, env, filename)
			}
		case *ast.IfStmt:
			c.analyzeBlock(stmt.Body.List, cloneEnv(env), filename)
			if block, ok := stmt.Else.(*ast.BlockStmt); ok {
				c.analyzeBlock(block.List, cloneEnv(env), filename)
			}
		case *ast.RangeStmt:
			c.analyzeRange(stmt, env, filename)
		case *ast.BlockStmt:
			c.analyzeBlock(stmt.List, cloneEnv(env), filename)
		}
	}
}

func (c *collector) analyzeRange(stmt *ast.RangeStmt, env collectEnv, filename string) {
	ident, ok := stmt.Value.(*ast.Ident)
	if !ok {
		return
	}
	literal, ok := stmt.X.(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, element := range literal.Elts {
		value, ok := evalString(element, env.strings)
		if !ok {
			continue
		}
		iteration := cloneEnv(env)
		iteration.strings[ident.Name] = value
		c.analyzeBlock(stmt.Body.List, iteration, filename)
	}
}

func (c *collector) analyzeCall(call *ast.CallExpr, env collectEnv, filename string) {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Handle" || selector.Sel.Name == "HandleFunc") {
		c.addRegistration(call, env, filename)
		return
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return
	}
	fn, ok := c.functions[ident.Name]
	if !ok {
		return
	}
	child := collectEnv{strings: cloneStrings(c.constants), handlers: make(map[string]handlerInfo)}
	index := 0
	for _, field := range fn.decl.Type.Params.List {
		for _, param := range field.Names {
			if index < len(call.Args) {
				if value, ok := evalString(call.Args[index], env.strings); ok {
					child.strings[param.Name] = value
				}
				child.handlers[param.Name] = inspectHandler(call.Args[index], env.handlers)
			}
			index++
		}
	}
	c.analyzeFunction(ident.Name, child)
}

func (c *collector) addRegistration(call *ast.CallExpr, env collectEnv, filename string) {
	if len(call.Args) < 2 {
		return
	}
	pattern, ok := evalString(call.Args[0], env.strings)
	position := c.fset.Position(call.Args[0].Pos())
	source := fmt.Sprintf("%s:%d", filename, position.Line)
	if !ok {
		if _, resolved := c.resolved[source]; !resolved {
			c.unknown[source] = struct{}{}
		}
		return
	}
	c.resolved[source] = struct{}{}
	delete(c.unknown, source)
	method, path := splitPattern(pattern)
	handler := inspectHandler(call.Args[1], env.handlers)
	auth := deriveAuth(method, path, handler.name, handler.auth)
	route := Route{Method: method, Pattern: path, Group: routeGroup(path), Handler: handler.name, Auth: auth}
	key := method + "\x00" + path
	c.routes[key] = mergeRoute(c.routes[key], route)
}

func evalString(expr ast.Expr, values map[string]string) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(node.Value)
		return value, err == nil
	case *ast.Ident:
		value, ok := values[node.Name]
		return value, ok
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, leftOK := evalString(node.X, values)
		right, rightOK := evalString(node.Y, values)
		return left + right, leftOK && rightOK
	case *ast.ParenExpr:
		return evalString(node.X, values)
	case *ast.SelectorExpr:
		switch expressionName(node) {
		case "deps.Config.EventPath":
			return "/api/public/v1/events", true
		case "deps.Config.TrackerPath":
			return "/js/script.js", true
		}
		if pkg, ok := node.X.(*ast.Ident); ok && pkg.Name == "http" && strings.HasPrefix(node.Sel.Name, "Method") {
			methods := map[string]string{"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT", "MethodPatch": "PATCH", "MethodDelete": "DELETE", "MethodOptions": "OPTIONS", "MethodHead": "HEAD"}
			value, ok := methods[node.Sel.Name]
			return value, ok
		}
	case *ast.CallExpr:
		if len(node.Args) != 1 {
			return "", false
		}
		argument, ok := evalString(node.Args[0], values)
		if !ok {
			return "", false
		}
		switch expressionName(node.Fun) {
		case "tracker.FullPath":
			return strings.TrimSuffix(argument, ".js") + ".full.js", true
		case "path.Dir":
			if slash := strings.LastIndexByte(argument, '/'); slash > 0 {
				return argument[:slash], true
			}
			return ".", true
		}
	}
	return "", false
}

func inspectHandler(expr ast.Expr, known map[string]handlerInfo) handlerInfo {
	switch node := expr.(type) {
	case *ast.Ident:
		if info, ok := known[node.Name]; ok && info.name != "" {
			return info
		}
		return handlerInfo{name: node.Name, auth: "none"}
	case *ast.SelectorExpr:
		return handlerInfo{name: expressionName(node), auth: "none"}
	case *ast.CallExpr:
		wrapper := expressionName(node.Fun)
		auth := wrapperAuth(wrapper)
		if auth == "unknown" || len(node.Args) == 0 {
			return handlerInfo{name: wrapper, auth: auth}
		}
		inner := inspectHandler(node.Args[len(node.Args)-1], known)
		if auth == "unknown" {
			auth = inner.auth
		}
		return handlerInfo{name: inner.name, auth: auth}
	case *ast.FuncLit:
		return handlerInfo{name: "func", auth: "none"}
	default:
		return handlerInfo{name: expressionName(expr), auth: "unknown"}
	}
}

func wrapperAuth(name string) string {
	switch name {
	case "authenticateRateLimited":
		return "public-key"
	case "authenticate", "requireSession":
		return "session"
	case "instanceAdmin", "requireInstanceAdmin":
		return "instance-admin"
	case "anonymousRateLimited", "trackerCORS", "http.HandlerFunc":
		return "none"
	default:
		return "unknown"
	}
}

// Auth is primarily derived from the wrapper passed to Handle/HandleFunc.
// Child mux registrations inherit the repository's mount convention: public
// v1 routes use API keys, private routes use sessions, and the two instance
// storage routes add the instance-admin check. Explicit anonymous auth/setup
// routes and operational endpoints remain unauthenticated. Anything outside
// these known wrappers and mount conventions is reported as unknown.
func deriveAuth(method, pattern, handler, wrapper string) string {
	if wrapper == "public-key" || wrapper == "session" || wrapper == "instance-admin" {
		return wrapper
	}
	if strings.HasPrefix(handler, "deps.Ingest") || strings.HasPrefix(handler, "deps.Replay") || strings.HasPrefix(handler, "tracker.") || handler == "serveOpenAPI" || handler == "metricsHandler" || handler == "func" {
		return "none"
	}
	if strings.HasPrefix(pattern, "/api/private/instance/") {
		return "instance-admin"
	}
	if strings.HasPrefix(pattern, "/api/public/v1/") {
		return "public-key"
	}
	if strings.HasPrefix(pattern, "/api/private/") {
		for _, public := range []string{
			"/api/private/setup", "/api/private/auth/config", "/api/private/auth/login",
			"/api/private/auth/signup", "/api/private/auth/password-reset",
			"/api/private/auth/logout", "/api/private/invitations/", "/api/private/share/",
		} {
			if pattern == public || strings.HasPrefix(pattern, public) {
				return "none"
			}
		}
		return "session"
	}
	if wrapper == "none" || wrapper == "public-key" || wrapper == "session" || wrapper == "instance-admin" {
		return wrapper
	}
	if method == "" || strings.HasPrefix(pattern, "/health") || strings.HasPrefix(pattern, "/ready") || pattern == "/metrics" {
		return "none"
	}
	return "unknown"
}

func expressionName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		prefix := expressionName(node.X)
		if prefix == "" {
			return node.Sel.Name
		}
		return prefix + "." + node.Sel.Name
	}
	return "expression"
}

func splitPattern(pattern string) (string, string) {
	if method, path, ok := strings.Cut(pattern, " "); ok && strings.HasPrefix(path, "/") {
		return strings.ToUpper(method), path
	}
	return "", pattern
}

func cloneEnv(env collectEnv) collectEnv {
	handlers := make(map[string]handlerInfo, len(env.handlers))
	for key, value := range env.handlers {
		handlers[key] = value
	}
	return collectEnv{strings: cloneStrings(env.strings), handlers: handlers}
}

func cloneStrings(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func authRank(auth string) int {
	switch auth {
	case "instance-admin":
		return 4
	case "public-key", "session":
		return 3
	case "none":
		return 2
	default:
		return 1
	}
}

func handlerRank(handler string) int {
	if strings.HasPrefix(handler, "h.") {
		return 3
	}
	if handler == "platform" || handler == "deps.Platform" || handler == "public" || handler == "private" || handler == "handler" || handler == "expression" {
		return 1
	}
	return 2
}

func mergeRoute(existing, candidate Route) Route {
	if existing.Pattern == "" && existing.Method == "" && existing.Handler == "" {
		return candidate
	}
	merged := existing
	if authRank(candidate.Auth) > authRank(existing.Auth) {
		merged.Auth = candidate.Auth
	}
	if handlerRank(candidate.Handler) > handlerRank(existing.Handler) || (handlerRank(candidate.Handler) == handlerRank(existing.Handler) && candidate.Handler < existing.Handler) {
		merged.Handler = candidate.Handler
	}
	return merged
}

// Deduplicate merges registrations with the same method and pattern.
func Deduplicate(routes []Route) []Route {
	byPattern := make(map[string]Route)
	for _, route := range routes {
		key := route.Method + "\x00" + route.Pattern
		byPattern[key] = mergeRoute(byPattern[key], route)
	}
	result := make([]Route, 0, len(byPattern))
	for _, route := range byPattern {
		result = append(result, route)
	}
	sortRoutes(result)
	return result
}

func uniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedFunctionNames(functions map[string]function) []string {
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
