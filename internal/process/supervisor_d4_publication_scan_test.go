package process

// ADR 021 §D4 item 4/5, T-7 — the writer publication scan.
//
// This file is a source-scanning control over internal/process (production
// files AND test files, because §D4 item 5 forbids excluding test files as a
// class). It implements the six requirements of §D4 item 4:
//
//  1. the known mutation forms are enumerated inside this control, so the list
//     is reviewed rather than implied — the thirteen *domain.LaunchInstance
//     lifecycle fields named by item 1, the controller fields run/active/
//     instance, and the three pointer-receiver mutating methods UpdateState /
//     UpdateError / Fail;
//  2. both forms are covered — direct assignment and a call to a mutating
//     method on a controller-owned receiver;
//  3. controller ownership is resolved by declaration (the InstanceController
//     type, its `instance *domain.LaunchInstance` field, and every value bound
//     from that field), never by a variable spelling, so an alias, a receiver
//     rename or a local copy of the pointer does not defeat the scan;
//  4. the detected set must equal the inventoried set in both directions, so a
//     new writer, a removed writer, and a publication that silently disappears
//     all fail;
//  5. test files are in scope;
//  6. the non-controller look-alikes are kept as an exact reviewed set with
//     reasons, and the §D3 test-support exception is asserted absent rather than
//     exempted by pattern.
//
// What this control is NOT (§D4 item 4, AH-1): it is not proof that no other
// mutation exists. It cannot see a mutation performed through a map or slice
// reached from the view (Environment, Args), a copy-mutate round trip that
// never assigns back through a controller-owned receiver, or a form nobody has
// thought of yet. `ctrl.instance` is inventoried precisely so that the
// assign-back step of such a round trip is caught, and the residual blind spots
// are pinned by TestD4ScanDocumentsItsOwnBlindSpots rather than left implied.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	// The scan parses the package under test, for the mutation sites, and the
	// package that declares the pointee type, for the declaration-site index.
	d4PackageDir    = "."
	d4DependencyDir = ".." + string(filepath.Separator) + "domain"

	d4ProcessPkg = "process"
	d4DomainPkg  = "domain"

	d4ControllerType  = d4ProcessPkg + ".InstanceController"
	d4LaunchInstance  = d4DomainPkg + ".LaunchInstance"
	d4LaunchInstanceP = "*" + d4LaunchInstance

	d4InstanceField = "instance"
	d4PublishCall   = "publishLocked"

	d4FormAssignment = "assignment"
	d4FormMethodCall = "method-call"
)

// d4InstanceStateFields enumerates requirement 1's field set: every
// *domain.LaunchInstance field named by §D4 item 4 requirement 1.
var d4InstanceStateFields = map[string]bool{
	"State": true, "LastError": true, "PID": true, "StartedAt": true,
	"StoppedAt": true, "UpdatedAt": true, "ExitCode": true, "ExitClass": true,
	"RuntimeID": true, "Executable": true, "Args": true, "WorkingDirectory": true,
	"Environment": true,
}

// d4ControllerFields are the InstanceController fields whose published identity
// is part of the view (D1): the run pointer, the active operation, and the
// instance pointer itself (the assign-back leg of a copy-mutate round trip).
var d4ControllerFields = map[string]bool{"run": true, "active": true, "instance": true}

// d4InstanceMutatingMethods enumerates requirement 2: domain.LaunchInstance's
// complete pointer-receiver mutating set.
// TestD4InstanceFieldEnumerationIsComplete re-checks the field enumeration
// against the declaration site, so a new field cannot enter the pointee
// unnoticed.
var d4InstanceMutatingMethods = map[string]bool{
	"UpdateState": true, "UpdateError": true, "Fail": true,
}

// d4ImmutableInstanceFields names the LaunchInstance fields requirement 1 does
// not enumerate. None of them is lifecycle state, and no production writer
// assigns to it after construction (§D5 records the ModelID absence for the
// lock-free readers).
var d4ImmutableInstanceFields = map[string]bool{
	"ID": true, "ModelID": true, "ModelName": true, "PipelineID": true,
	"PipelineEntryID": true, "CreatedAt": true, "RecoveryReason": true,
}

// d4PublicationHelpers are the mutation helpers that own a publication. A site
// inside one of them is authorized only if the helper itself calls
// publishLocked; a site outside one is authorized only if that function calls a
// helper (or publishLocked) after the mutation. It is the default set a scan
// runs with, never a scratch map: a fixture that needs an extra helper gets its
// own copy, so two tests can run in either order, or in parallel, without
// editing one another's view of the rule.
var d4PublicationHelpers = map[string]bool{
	"setStateLocked": true, "setTerminalLocked": true, "failLocked": true,
	"setErrorLocked": true, "setLastErrorLocked": true, "setRunLocked": true,
	"setOperationLocked": true, "clearOperationLocked": true,
	"setLaunchFieldsLocked": true,
}

// d4WithPublicationHelpers returns the default helper set plus extras.
func d4WithPublicationHelpers(extras ...string) map[string]bool {
	out := make(map[string]bool, len(d4PublicationHelpers)+len(extras))
	for name := range d4PublicationHelpers {
		out[name] = true
	}
	for _, name := range extras {
		out[name] = true
	}
	return out
}

// Inventories. Each entry is an exact, reviewed publication site: file,
// enclosing function, target and form. Line numbers are deliberately absent
// from the key so the inventory survives the line shifts every slice produces;
// the authorization check re-derives ordering from the parsed bytes.

// d4InventoriedOwned is §D4's eighteen production publication sites after the
// Slice 1 helper consolidation: every remaining mutation of controller-owned
// lifecycle state lives inside one of the nine helpers, plus startCore's
// PID/StartedAt pair, which is published by the setStateLocked call that
// follows it. The §D3 row each entry implements is named beside it.
//
// Row 3 (`starting`), row 7 (`failed`), row 8 (`starting` revert), row 10
// (`stopping` — the F-7 publication point) and row 6's `running` write all
// collapse into the single setStateLocked body; rows 5, 17 and 18 into
// failLocked; rows 9 and 11 into setErrorLocked; row 16 and row 7's companion
// LastError write into setLastErrorLocked. None of them can be mutated without
// publishing, which is why the helper body is one inventoried site per field
// rather than one per baseline call site.
var d4InventoriedOwned = d4Inventory{
	// row 1 — operation publication (arbitration claim)
	"supervisor.go#setOperationLocked.ctrl.active[assignment]": 1,
	// row 2 — operation release
	"supervisor.go#clearOperationLocked.ctrl.active[assignment]": 1,
	// row 4 — the generation becomes visible
	"supervisor.go#setRunLocked.ctrl.run[assignment]": 1,
	// rows 3, 6 (running), 7 (failed), 8, 10 (stopping)
	"supervisor.go#setStateLocked.instance.UpdateState[method-call]": 1,
	// rows 5, 17, 18
	"supervisor.go#failLocked.instance.Fail[method-call]": 1,
	// rows 9, 11
	"supervisor.go#setErrorLocked.instance.UpdateError[method-call]": 1,
	// row 16 + row 7's companion LastError write (UpdateError would also clobber
	// ExitClass/UpdatedAt, which those rows must not do)
	"supervisor.go#setLastErrorLocked.instance.LastError[assignment]": 1,
	// rows 13, 14, 15 — wait()'s terminal transition as one published view
	"supervisor.go#setTerminalLocked.instance.PID[assignment]":       1,
	"supervisor.go#setTerminalLocked.instance.ExitCode[assignment]":  1,
	"supervisor.go#setTerminalLocked.instance.ExitClass[assignment]": 1,
	"supervisor.go#setTerminalLocked.instance.LastError[assignment]": 1,
	"supervisor.go#setTerminalLocked.instance.State[assignment]":     1,
	"supervisor.go#setTerminalLocked.instance.StoppedAt[assignment]": 1,
	"supervisor.go#setTerminalLocked.instance.UpdatedAt[assignment]": 1,
	// row 12 — restart-spec refresh
	"supervisor.go#setLaunchFieldsLocked.instance.RuntimeID[assignment]":        1,
	"supervisor.go#setLaunchFieldsLocked.instance.Executable[assignment]":       1,
	"supervisor.go#setLaunchFieldsLocked.instance.Args[assignment]":             1,
	"supervisor.go#setLaunchFieldsLocked.instance.WorkingDirectory[assignment]": 1,
	"supervisor.go#setLaunchFieldsLocked.instance.Environment[assignment]":      1,
	// row 6 — the spawn snapshot, published by the setStateLocked(running)
	// that follows both lines (supervisor.go's startCore success path).
	"supervisor.go#startCore.instance.PID[assignment]":       1,
	"supervisor.go#startCore.instance.StartedAt[assignment]": 1,
}

// d4InventoriedForeignPointers is the reviewed set of
// *domain.LaunchInstance writes that no controller owns: the recovery and
// orphan-reconciliation paths work on store-derived copies, and the kill tests
// fabricate their own fixtures. Each is a genuine write to a LaunchInstance
// pointer, so the scan sees it; it is inventoried rather than excluded so that a
// NEW foreign writer fails the control (§D4 item 4).
var d4InventoriedForeignPointers = d4Inventory{
	// Recover classifies each store-derived record (domain.ToDomain) and
	// persists it back; it never constructs a controller, so this pointer is
	// never any controller's ic.instance.
	"supervisor.go#Recover.instance.UpdateState[method-call]": 1,
	// DismissOrphan mutates the orphan record it then persists, from a
	// domain.ToDomain copy the same way.
	"supervisor.go#DismissOrphan.instance.UpdateState[method-call]": 1,
	// supervisor_kill.go's kill paths act on the identity-verification
	// snapshot, not on a controller's instance.
	"supervisor_kill.go#finishKill.instance.UpdateState[method-call]": 1,
	"supervisor_kill.go#finishKill.instance.ExitClass[assignment]":    1,
	"supervisor_kill.go#refuseKill.instance.UpdateError[method-call]": 1,
	// The kill test fixtures build their own *domain.LaunchInstance values.
	"supervisor_kill_test.go#newKillFixture.instance.StartedAt[assignment]":                          1,
	"supervisor_kill_test.go#TestVerifyIdentityForKill_StrictMatrix.instance.StartedAt[assignment]":  1,
	"supervisor_kill_test.go#TestVerifyIdentityForKill_StrictMatrix.instance.Executable[assignment]": 1,
}

// d4InventoriedValueCopies is the reviewed set of writes to value-typed
// LaunchInstance copies. They are excluded from the ownership inventory
// structurally, by declared type, and are listed here so the exclusion itself
// stays visible and exact.
var d4InventoriedValueCopies = d4Inventory{
	// publishLocked: `view.instance = *ic.instance` followed by the
	// deep-copy of the two containers, which is what makes the published view
	// immutable in-package (ADR 021 D1/I-2).
	"supervisor.go#publishLocked.instance.Args[assignment]":        1,
	"supervisor.go#publishLocked.instance.Environment[assignment]": 1,
}

// d4InventoriedUnresolved is the reviewed set of mutation sites whose receiver
// the scan cannot type. Requirement 4 makes an unattributed receiver a failure,
// so "we cannot see it" has to be a human decision recorded here rather than a
// silent gap: a NEW unattributed site fails the control the same way a new
// writer does.
//
// Both keys are Snapshot's own return value (`snap := view.instance`, a
// domain.LaunchInstance VALUE). The receiver is unresolvable because it is read
// through ic.view.Load(), a method on sync/atomic.Pointer that the scan
// deliberately does not type — typing the pointee of a generic would make the
// scan's answer depend on the standard library's declarations. These writes
// touch no shared state: they build the caller-independent copy §D5 requires
// Snapshot to return, on a value that is never stored back into the controller.
var d4InventoriedUnresolved = d4Inventory{
	"supervisor.go#Snapshot.Args[assignment]":        2,
	"supervisor.go#Snapshot.Environment[assignment]": 2,
}

// d4InventoriedTestSupportPublications names the in-package test functions that
// reach a publication helper, which is the form §D4 item 5's five bare test
// writers were migrated to during Slice 2. The value is the number of
// publication calls in that function, so a helper call that is dropped or
// duplicated changes the inventory.
var d4InventoriedTestSupportPublications = d4Inventory{
	// bf01BindClaim mirrors publishClaimLocked: it binds a claim to a
	// controller through setOperationLocked.
	"supervisor_bf01_test.go#bf01BindClaim": 1,
	// The three BF-01/restart arbitration tests each stage an in-flight
	// operation on the target controller.
	"supervisor_bf01_test.go#TestArbitration_DoesNotHoldAcrossRestartStop":  1,
	"supervisor_bf01_test.go#TestRestart_DuringShutdown_DrainWins":          1,
	"supervisor_bf01_test.go#TestRestart_Stopping_AwaitsExistingGeneration": 1,
	// d3ControllerWithState stages the run for the cleanup scan tests.
	"supervisor_d3_cleanup_test.go#d3ControllerWithState": 1,
	// TestD3Cleanup_ActiveOperationAndReservationSurvive is §D3's test-support
	// exception and §D4 item 6's `:225` allow-list entry; both landed on
	// setOperationLocked instead of an exemption, and it publishes twice
	// (operation and run).
	"supervisor_d3_cleanup_test.go#TestD3Cleanup_ActiveOperationAndReservationSurvive": 2,
}

type d4Category int

const (
	d4Owned d4Category = iota
	d4Foreign
	d4Value
	d4Unresolved
)

// d4Val is a resolved value: its declared type plus the provenance needed to
// decide controller ownership.
type d4Val struct {
	typ            string
	owner          string // struct type this value was read out of, "" if not a field read
	fromController bool   // reached as <InstanceController>.instance
	lookAlike      bool   // reached as <other struct>.instance of type *domain.LaunchInstance
	result         string // for a func-literal binding: its single result type
}

// d4Site is one detected mutation. The key drops line numbers on purpose: the
// inventory must survive the line shifts that every slice produces while still
// pinning file, enclosing function, target and form.
type d4Site struct {
	File   string
	Func   string
	Target string
	Form   string
	Line   int
}

func (s d4Site) key() string {
	return s.File + "#" + s.Func + "." + s.Target + "[" + s.Form + "]"
}

// d4Inventory is the expected multiset for one category.
type d4Inventory map[string]int

func (inv d4Inventory) lines() []string {
	out := make([]string, 0, len(inv))
	for k, v := range inv {
		out = append(out, k+" = "+d4Itoa(v))
	}
	sort.Strings(out)
	return out
}

type d4FuncInfo struct {
	name      string
	firstLine int
	lastLine  int
	publishAt []int
}

type d4Scan struct {
	structFields map[string]map[string]string // qualified struct -> field -> declared type
	funcResults  map[string]string            // qualified func -> single result type

	// helpers is the publication-helper set this scan judged with, so a fixture
	// never has to mutate state shared with another test.
	helpers map[string]bool

	currentFile string

	owned      []d4Site
	foreign    []d4Site
	valueCopy  []d4Site
	unresolved []d4Site

	functions map[string]*d4FuncInfo
}

// d4Norm qualifies bare type idents with the package that declares them, so a
// receiver inside internal/process and a result inside internal/domain share one
// spelling.
func d4Norm(typ, pkg string) string {
	if typ == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(typ, "*"):
		return "*" + d4Norm(typ[1:], pkg)
	case strings.HasPrefix(typ, "[]"):
		return "[]" + d4Norm(typ[2:], pkg)
	case strings.HasPrefix(typ, "map["):
		k, v, ok := d4CutKeyValue(typ[len("map[") : len(typ)-1])
		if !ok {
			return typ
		}
		return "map[" + d4Norm(k, pkg) + "]" + d4Norm(v, pkg)
	}
	if d4Predeclared[typ] || strings.Contains(typ, ".") {
		return typ
	}
	return pkg + "." + typ
}

// d4Predeclared is the set of type names that belong to no package, so they are
// not qualified and never resolve to an indexed struct.
var d4Predeclared = map[string]bool{
	"bool": true, "string": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"byte": true, "rune": true, "float32": true, "float64": true, "complex64": true,
	"complex128": true, "error": true, "any": true, "comparable": true, "uintptr": true,
}

func d4CutKeyValue(s string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

func d4TypeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		if inner := d4TypeString(t.X); inner != "" {
			return "*" + inner
		}
	case *ast.SelectorExpr:
		if x := d4TypeString(t.X); x != "" {
			return x + "." + t.Sel.Name
		}
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + d4TypeString(t.Elt)
		}
	case *ast.MapType:
		k, v := d4TypeString(t.Key), d4TypeString(t.Value)
		if k != "" && v != "" {
			return "map[" + k + "]" + v
		}
	}
	return ""
}

func d4BaseType(typ string) string { return strings.TrimPrefix(typ, "*") }
func d4IsPointer(typ string) bool  { return strings.HasPrefix(typ, "*") }

func d4SelName(e *ast.SelectorExpr) string {
	if e == nil || e.Sel == nil {
		return ""
	}
	return e.Sel.Name
}

func d4FilePkg(file *ast.File) string {
	if file == nil || file.Name == nil {
		return ""
	}
	return file.Name.Name
}

// indexFile records the struct types and function results declared in one file,
// keyed by the package that declares them.
func (sc *d4Scan) indexFile(file *ast.File) {
	pkg := d4FilePkg(file)
	ast.Inspect(file, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.TypeSpec:
			st, ok := decl.Type.(*ast.StructType)
			if !ok {
				return true
			}
			qualified := pkg + "." + decl.Name.Name
			fields := sc.structFields[qualified]
			if fields == nil {
				fields = map[string]string{}
				sc.structFields[qualified] = fields
			}
			for _, f := range st.Fields.List {
				typ := d4Norm(d4TypeString(f.Type), pkg)
				if typ == "" {
					continue
				}
				if len(f.Names) == 0 {
					key := d4BaseType(typ)
					if _, seen := fields[key]; !seen {
						fields[key] = typ
					}
					continue
				}
				for _, name := range f.Names {
					if _, seen := fields[name.Name]; !seen {
						fields[name.Name] = typ
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv != nil || decl.Type.Results == nil || len(decl.Type.Results.List) != 1 {
				return true
			}
			res := decl.Type.Results.List[0]
			if len(res.Names) > 1 {
				return true
			}
			typ := d4Norm(d4TypeString(res.Type), pkg)
			if typ == "" {
				return true
			}
			name := pkg + "." + decl.Name.Name
			if _, seen := sc.funcResults[name]; !seen {
				sc.funcResults[name] = typ
			}
		}
		return true
	})
}

// d4Bindings is the flat per-function view of local values. Shadowing is not
// modelled: a function that rebinds a name to another type is reported as
// unresolved at its write sites and has to be reviewed.
type d4Bindings map[string]d4Val

func (sc *d4Scan) collectBindings(fn *ast.FuncDecl, pkg string) d4Bindings {
	bindings := d4Bindings{}
	declare := func(name, typ string) {
		if name == "" || name == "_" || typ == "" {
			return
		}
		bindings[name] = d4Val{typ: d4Norm(typ, pkg)}
	}
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			if len(field.Names) > 0 {
				declare(field.Names[0].Name, d4TypeString(field.Type))
			}
		}
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			typ := d4TypeString(field.Type)
			for _, name := range field.Names {
				declare(name.Name, typ)
			}
		}
	}
	if fn.Body == nil {
		return bindings
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE {
				return true
			}
			if len(stmt.Rhs) != len(stmt.Lhs) {
				// x, y := f() — values cannot be matched to names without a
				// type checker; bind each as unknown so a write through it is
				// reported rather than guessed at.
				for _, lhs := range stmt.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
						bindings[ident.Name] = d4Val{}
					}
				}
				return true
			}
			for i, lhs := range stmt.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				bindings[ident.Name] = sc.exprValue(stmt.Rhs[i], bindings, pkg)
			}
		case *ast.DeclStmt:
			gen, ok := stmt.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, s := range gen.Specs {
				spec, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typ := ""
				if spec.Type != nil {
					typ = d4Norm(d4TypeString(spec.Type), pkg)
				}
				for j, name := range spec.Names {
					if name.Name == "_" {
						continue
					}
					if typ == "" && j < len(spec.Values) {
						bindings[name.Name] = sc.exprValue(spec.Values[j], bindings, pkg)
						continue
					}
					bindings[name.Name] = d4Val{typ: typ}
				}
			}
		case *ast.RangeStmt:
			if stmt.Tok != token.DEFINE {
				return true
			}
			elem := sc.rangeElementType(stmt.X, bindings, pkg)
			if elem == "" {
				return true
			}
			for _, target := range []ast.Expr{stmt.Key, stmt.Value} {
				if ident, ok := target.(*ast.Ident); ok && ident.Name != "_" {
					bindings[ident.Name] = d4Val{typ: elem}
				}
			}
		}
		return true
	})
	return bindings
}

func (sc *d4Scan) rangeElementType(x ast.Expr, bindings d4Bindings, pkg string) string {
	val := sc.exprValue(x, bindings, pkg)
	switch {
	case strings.HasPrefix(val.typ, "[]"):
		return val.typ[2:]
	case strings.HasPrefix(val.typ, "map["):
		if _, v, ok := d4CutKeyValue(val.typ[len("map[") : len(val.typ)-1]); ok {
			return v
		}
	}
	return ""
}

// exprValue resolves the value produced by an initializer expression.
func (sc *d4Scan) exprValue(expr ast.Expr, bindings d4Bindings, pkg string) d4Val {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return sc.exprValue(e.X, bindings, pkg)
	case *ast.UnaryExpr:
		inner := sc.exprValue(e.X, bindings, pkg)
		if inner.typ == "" {
			return d4Val{}
		}
		switch e.Op {
		case token.AND:
			return d4Val{typ: "*" + d4BaseType(inner.typ)}
		case token.MUL:
			// Dereferencing yields a value copy, which is no longer
			// controller-owned mutable state — exactly why §D1 I-2 clones the
			// slices at publication instead of sharing the pointee.
			return d4Val{typ: d4BaseType(inner.typ)}
		}
	case *ast.CompositeLit:
		if e.Type == nil {
			return d4Val{}
		}
		return d4Val{typ: d4Norm(d4TypeString(e.Type), pkg)}
	case *ast.FuncLit:
		if e.Type == nil || e.Type.Results == nil || len(e.Type.Results.List) != 1 {
			return d4Val{}
		}
		return d4Val{typ: "func", result: d4Norm(d4TypeString(e.Type.Results.List[0].Type), pkg)}
	case *ast.SelectorExpr:
		// A qualified type name, e.g. domain.LaunchInstance used as a value.
		if t := d4TypeString(e); t != "" && sc.structFields[d4BaseType(t)] != nil && !d4IsPointer(t) {
			return d4Val{typ: d4Norm(t, pkg)}
		}
		// Otherwise a field read: resolved through the chain, so the alias
		// `target := ic.instance` keeps the controller provenance (requirement
		// 3 is declaration-based, not spelling-based).
		if segs, ok := d4Chain(e); ok {
			if val, ok := sc.resolve(segs, bindings); ok {
				return val
			}
		}
	case *ast.Ident:
		if e.Name == "nil" {
			return d4Val{}
		}
		if val, ok := bindings[e.Name]; ok {
			return val
		}
	case *ast.CallExpr:
		if fn, ok := e.Fun.(*ast.Ident); ok {
			if val, exists := bindings[fn.Name]; exists {
				if val.result != "" {
					return d4Val{typ: val.result}
				}
				return d4Val{}
			}
			if res, exists := sc.funcResults[pkg+"."+fn.Name]; exists {
				return d4Val{typ: res}
			}
		}
		if se, ok := e.Fun.(*ast.SelectorExpr); ok {
			if root, isIdent := se.X.(*ast.Ident); isIdent {
				if val, exists := bindings[root.Name]; exists && val.result != "" {
					return d4Val{typ: val.result}
				}
				if res, exists := sc.funcResults[root.Name+"."+d4SelName(se)]; exists {
					return d4Val{typ: res}
				}
			}
		}
	}
	return d4Val{}
}

// resolve walks a dotted chain from a binding and returns the value it denotes.
// The first segment must be a bound name; each later segment must be a declared
// field of the struct type reached so far.
func (sc *d4Scan) resolve(segs []string, bindings d4Bindings) (d4Val, bool) {
	if len(segs) == 0 {
		return d4Val{}, false
	}
	cur, ok := bindings[segs[0]]
	if !ok || cur.typ == "" {
		return d4Val{}, false
	}
	for _, name := range segs[1:] {
		fields := sc.structFields[d4BaseType(cur.typ)]
		if fields == nil {
			return d4Val{}, false
		}
		typ, exists := fields[name]
		if !exists {
			return d4Val{}, false
		}
		next := d4Val{typ: typ, owner: d4BaseType(cur.typ)}
		if name == d4InstanceField && d4BaseType(typ) == d4LaunchInstance && d4IsPointer(typ) {
			// Requirement 3: ownership comes from the declaration site of the
			// struct the pointer was read out of, not from the spelling.
			next.fromController = next.owner == d4ControllerType
			next.lookAlike = next.owner != d4ControllerType
		}
		cur = next
	}
	return cur, true
}

// classifyMutation resolves one write target. `holder` is the dotted chain of
// the entity being written through (for an assignment, the LHS minus its last
// segment; for a call, the receiver), and `field` is the field name or the
// method name written.
func (sc *d4Scan) classifyMutation(holder []string, field string, bindings d4Bindings, inFunc string, line int, form string) {
	if len(holder) == 0 || field == "" {
		return
	}
	val, resolved := sc.resolve(holder, bindings)
	if !resolved || val.typ == "" || val.lookAlike {
		// Unattributable receiver, or a second type that owns a
		// *domain.LaunchInstance field: the scan must not guess, and a
		// lifecycle-shaped write through it has to be reviewed.
		if d4InstanceStateFields[field] || d4InstanceMutatingMethods[field] || val.lookAlike {
			sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: field, Form: form, Line: line}, d4Unresolved)
		}
		return
	}

	base := d4BaseType(val.typ)
	switch {
	case base == d4ControllerType && form == d4FormAssignment && d4ControllerFields[field]:
		sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: "ctrl." + field, Form: form, Line: line}, d4Owned)

	case base == d4LaunchInstance && d4IsPointer(val.typ):
		if val.fromController {
			// Any field of the controller's own pointee is view-carried state,
			// including fields requirement 1 did not enumerate.
			sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: "instance." + field, Form: form, Line: line}, d4Owned)
			return
		}
		if d4InstanceStateFields[field] || d4InstanceMutatingMethods[field] {
			sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: "instance." + field, Form: form, Line: line}, d4Foreign)
		}

	case base == d4LaunchInstance:
		if d4InstanceStateFields[field] || d4InstanceMutatingMethods[field] {
			sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: "instance." + field, Form: form, Line: line}, d4Value)
		}

	case sc.structFields[base] != nil:
		// A declared struct that is neither the controller nor the pointee:
		// spec.Args, entry.PID, status.StartedAt are not controller state.

	default:
		if d4InstanceStateFields[field] || d4InstanceMutatingMethods[field] {
			sc.record(d4Site{File: sc.currentFile, Func: inFunc, Target: field, Form: form, Line: line}, d4Unresolved)
		}
	}
}

func (sc *d4Scan) record(site d4Site, cat d4Category) {
	switch cat {
	case d4Owned:
		sc.owned = append(sc.owned, site)
	case d4Foreign:
		sc.foreign = append(sc.foreign, site)
	case d4Value:
		sc.valueCopy = append(sc.valueCopy, site)
	default:
		sc.unresolved = append(sc.unresolved, site)
	}
}

// scanFile walks one file's top-level declarations.
func (sc *d4Scan) scanFile(fset *token.FileSet, file *ast.File, name string) {
	pkg := d4FilePkg(file)
	sc.currentFile = name
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bindings := sc.collectBindings(fn, pkg)
		info := &d4FuncInfo{name: fn.Name.Name, firstLine: fset.Position(fn.Pos()).Line}
		info.lastLine = fset.Position(fn.End()).Line
		sc.functions[name+"#"+fn.Name.Name] = info

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				if node.Tok == token.DEFINE {
					return true
				}
				for _, lhs := range node.Lhs {
					segs, ok := d4Chain(lhs)
					if !ok || len(segs) < 2 {
						continue
					}
					sc.classifyMutation(segs[:len(segs)-1], segs[len(segs)-1], bindings,
						info.name, fset.Position(lhs.Pos()).Line, d4FormAssignment)
				}
			case *ast.IncDecStmt:
				segs, ok := d4Chain(node.X)
				if !ok || len(segs) < 2 {
					return true
				}
				sc.classifyMutation(segs[:len(segs)-1], segs[len(segs)-1], bindings,
					info.name, fset.Position(node.X.Pos()).Line, d4FormAssignment)
			case *ast.CallExpr:
				se, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if !d4InstanceMutatingMethods[d4SelName(se)] {
					sc.recordPublicationCall(se, info, fset)
					return true
				}
				segs, ok := d4Chain(se.X)
				if !ok {
					return true
				}
				sc.classifyMutation(segs, d4SelName(se), bindings,
					info.name, fset.Position(node.Pos()).Line, d4FormMethodCall)
			}
			return true
		})
	}
}

// recordPublicationCall notes where a function reaches a publication so the
// authorization check can order mutations against it.
func (sc *d4Scan) recordPublicationCall(se *ast.SelectorExpr, info *d4FuncInfo, fset *token.FileSet) {
	name := d4SelName(se)
	if !sc.helpers[name] && name != d4PublishCall {
		return
	}
	info.publishAt = append(info.publishAt, fset.Position(se.Pos()).Line)
}

// d4Chain splits an expression into dotted segments. It stops when a component
// is neither an identifier nor a selector, so an index or a call inside a chain
// makes the whole chain unseen rather than silently truncated:
// `store.instances["x"].PID` is not a field write this scan claims to see (that
// is §D4 item 4's documented map blind spot), and `ic.view.Store(v)` carries no
// lifecycle field.
func d4Chain(expr ast.Expr) ([]string, bool) {
	var segs []string
	cur := expr
	for {
		switch c := cur.(type) {
		case *ast.SelectorExpr:
			name := d4SelName(c)
			if name == "" {
				return nil, false
			}
			segs = append([]string{name}, segs...)
			cur = c.X
		case *ast.Ident:
			return append([]string{c.Name}, segs...), true
		default:
			return nil, false
		}
	}
}

func d4ParseDir(dir string) (*token.FileSet, map[string]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if err != nil {
			return nil, nil, err
		}
		files[e.Name()] = parsed
	}
	return fset, files, nil
}

// d4ScanSources runs the control over the package plus any injected files. An
// injected file replaces the package file of the same name, which is how the
// detection fixtures plant violations without touching repository bytes.
func d4ScanSources(dir string, injected map[string]string) (*d4Scan, error) {
	return d4ScanSourcesWithHelpers(dir, injected, nil)
}

// d4ScanSourcesWithHelpers is d4ScanSources with an explicit publication-helper
// set; a nil set uses the repository's own helpers. The detection fixtures that
// plant a helper-shaped function pass it here instead of editing shared state.
func d4ScanSourcesWithHelpers(dir string, injected map[string]string, helpers map[string]bool) (*d4Scan, error) {
	if helpers == nil {
		helpers = d4WithPublicationHelpers()
	}
	fset, files, err := d4ParseDir(dir)
	if err != nil {
		return nil, err
	}
	for name, src := range injected {
		parsed, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files[name] = parsed
	}

	sc := &d4Scan{
		structFields: map[string]map[string]string{},
		funcResults:  map[string]string{},
		functions:    map[string]*d4FuncInfo{},
		helpers:      helpers,
	}

	names := d4SortedFileNames(files)
	for _, name := range names {
		sc.indexFile(files[name])
	}
	_, depFiles, err := d4ParseDir(d4DependencyDir)
	if err != nil {
		return nil, err
	}
	for _, name := range d4SortedFileNames(depFiles) {
		sc.indexFile(depFiles[name])
	}
	for _, name := range names {
		sc.scanFile(fset, files[name], name)
	}
	return sc, nil
}

func d4SortedFileNames(files map[string]*ast.File) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func countsOf(sites []d4Site) d4Inventory {
	out := d4Inventory{}
	for _, s := range sites {
		out[s.key()]++
	}
	return out
}

func d4SiteLines(sites []d4Site) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, "  "+s.File+":"+d4Itoa(s.Line)+" "+s.Func+" "+s.Target+"["+s.Form+"]")
	}
	sort.Strings(out)
	return out
}

func d4Itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// authorizationOf is requirement 4's second half: an inventoried site is only
// legitimate while a publication still follows it in the same function.
func (sc *d4Scan) authorizationOf(site d4Site) (string, bool) {
	info := sc.functions[site.File+"#"+site.Func]
	if info == nil {
		return "no enclosing function was found", false
	}
	if site.Func == d4PublishCall {
		return "publishLocked itself", true
	}
	for _, line := range info.publishAt {
		if line > site.Line {
			return "publication after the mutation in " + site.Func, true
		}
	}
	if sc.helpers[site.Func] {
		return "helper body without a publication", false
	}
	return "no publication after the mutation in " + site.Func, false
}

// TestD4PublicationScanInventoryMatchesTheRepository is §D4 item 4 requirement
// 4: the detected set must equal the inventoried set, in both directions, with
// every inventoried site still authorized by a publication.
func TestD4PublicationScanInventoryMatchesTheRepository(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, nil)
	if err != nil {
		t.Fatalf("scan internal/process: %v", err)
	}

	if got := countsOf(sc.owned); !reflect.DeepEqual(got, d4InventoriedOwned) {
		t.Errorf("controller-owned publication sites drifted from the inventory.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got.lines(), "\n  "), strings.Join(d4InventoriedOwned.lines(), "\n  "))
	}
	if got := countsOf(sc.foreign); !reflect.DeepEqual(got, d4InventoriedForeignPointers) {
		t.Errorf("non-controller *domain.LaunchInstance writers drifted from the reviewed set.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got.lines(), "\n  "), strings.Join(d4InventoriedForeignPointers.lines(), "\n  "))
	}
	if got := countsOf(sc.valueCopy); !reflect.DeepEqual(got, d4InventoriedValueCopies) {
		t.Errorf("writes to value-typed LaunchInstance copies drifted from the reviewed set.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got.lines(), "\n  "), strings.Join(d4InventoriedValueCopies.lines(), "\n  "))
	}
	if got := countsOf(sc.unresolved); !reflect.DeepEqual(got, d4InventoriedUnresolved) {
		t.Errorf("mutation receivers the scan cannot attribute drifted from the reviewed set.\ngot:\n  %s\nwant:\n  %s\nlines:\n  %s",
			strings.Join(got.lines(), "\n  "), strings.Join(d4InventoriedUnresolved.lines(), "\n  "),
			strings.Join(d4SiteLines(sc.unresolved), "\n  "))
	}

	for _, site := range sc.owned {
		if reason, ok := sc.authorizationOf(site); !ok {
			t.Errorf("%s:%d %s in %s: inventoried mutation is no longer followed by a publication (%s)",
				site.File, site.Line, site.Target, site.Func, reason)
		}
	}
}

// TestD4InstanceFieldEnumerationIsComplete checks requirement 1 against the
// declaration site: the enumerated lifecycle set plus the reviewed immutable set
// must still cover every field domain.LaunchInstance declares, and the
// controller must still own its instance pointer as requirement 3 assumes.
func TestD4InstanceFieldEnumerationIsComplete(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, nil)
	if err != nil {
		t.Fatalf("scan internal/process: %v", err)
	}
	fields := sc.structFields[d4LaunchInstance]
	if len(fields) == 0 {
		t.Fatalf("the scan did not index %s; requirement 1's enumeration is unverifiable", d4LaunchInstance)
	}
	for name := range fields {
		if !d4InstanceStateFields[name] && !d4ImmutableInstanceFields[name] {
			t.Errorf("domain.LaunchInstance declares field %q, which is in neither the enumerated lifecycle set nor the reviewed immutable set", name)
		}
	}
	controller := sc.structFields[d4ControllerType]
	if controller == nil {
		t.Fatalf("the scan did not index %s", d4ControllerType)
	}
	if controller[d4InstanceField] != d4LaunchInstanceP {
		t.Errorf("%s.%s is %q, not %q: requirement 3's ownership rule resolves the wrong type",
			d4ControllerType, d4InstanceField, controller[d4InstanceField], d4LaunchInstanceP)
	}
	for _, name := range []string{"run", "active"} {
		if !d4ControllerFields[name] {
			t.Errorf("the controller field %q is published by the view but not enumerated by the scan", name)
		}
	}
}

// TestD4PublicationScanAuthorizesTestSupportPublications covers the
// test-support publication form: §D3's exception and §D4 item 5's in-package
// test writers must reach the view through the helpers, so none of them may
// appear as a bare mutation site.
func TestD4PublicationScanAuthorizesTestSupportPublications(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, nil)
	if err != nil {
		t.Fatalf("scan internal/process: %v", err)
	}

	helperCalls := d4Inventory{}
	for name, info := range sc.functions {
		if !strings.Contains(name, "_test.go#") {
			continue
		}
		if len(info.publishAt) > 0 {
			helperCalls[name] = len(info.publishAt)
		}
	}
	if !reflect.DeepEqual(helperCalls, d4InventoriedTestSupportPublications) {
		t.Errorf("the in-package test writers that publish drifted from the inventory of the sites corrected during Slice 2.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(helperCalls.lines(), "\n  "), strings.Join(d4InventoriedTestSupportPublications.lines(), "\n  "))
	}

	// §D3's test-support exception and §D4 item 5's allow-list entry both landed
	// on migration to setOperationLocked, so there must be nothing left to
	// exempt. Asserting the absence keeps a re-bared assignment from being
	// silently allowed.
	for _, site := range append(append([]d4Site{}, sc.owned...), sc.foreign...) {
		if site.File == "arbitration.go" && site.Func == "mintTestSpawnClaim" && strings.Contains(site.Target, "active") {
			t.Errorf("mintTestSpawnClaim writes ctrl.active directly again; §D3 requires it to go through setOperationLocked")
		}
		if site.File == "supervisor_d3_cleanup_test.go" &&
			site.Func == "TestD3Cleanup_ActiveOperationAndReservationSurvive" && strings.Contains(site.Target, "active") {
			t.Errorf("the cleanup reservation test writes ctrl.active directly again; it must publish through setOperationLocked")
		}
	}
}

// d4PlantedViolationsSrc is the injected fixture body. `publish` is appended to
// each function for the corrected-file fixture, so the same writes become
// authorized without changing what is written.
func d4PlantedViolationsSrc(publish string) string {
	call := ""
	if publish != "" {
		call = "\t" + publish + "\n"
	}
	return `package process

import (
	"github.com/dsdred/goal/internal/domain"
)

func (ic *InstanceController) plantedDirectAssignment() {
	ic.instance.State = domain.InstanceStateRunning
` + call + `}

func (ic *InstanceController) plantedMethodCall() {
	ic.instance.UpdateState(domain.InstanceStateRunning)
` + call + `}

func (ic *InstanceController) plantedAliasDefeatsSpelling() {
	target := ic.instance
	target.LastError = ""
` + call + `}

func (ic *InstanceController) plantedControllerFieldWrite() {
	ic.active = nil
` + call + `}

func (ic *InstanceController) plantedInstanceAssignBack() {
	fresh := &domain.LaunchInstance{}
	fresh.State = domain.InstanceStateRunning
	ic.instance = fresh
` + call + `}

func (ic *InstanceController) setPlantedLocked(cls domain.InstanceExitClass) {
	ic.instance.ExitClass = cls
` + call + `}
`
}

// d4PlantedKeys are the controller-owned sites the fixture above must produce.
var d4PlantedKeys = []string{
	"zz_planted.go#plantedDirectAssignment.instance.State[assignment]",
	"zz_planted.go#plantedMethodCall.instance.UpdateState[method-call]",
	"zz_planted.go#plantedAliasDefeatsSpelling.instance.LastError[assignment]",
	"zz_planted.go#plantedControllerFieldWrite.ctrl.active[assignment]",
	"zz_planted.go#plantedInstanceAssignBack.ctrl.instance[assignment]",
	"zz_planted.go#setPlantedLocked.instance.ExitClass[assignment]",
}

// TestD4ScanCatchesPlantedViolations is T-7: the control must detect each
// planted form — a direct assignment, a mutating-method call, an alias that
// defeats the spelling, a controller-field write, the assign-back leg of a
// copy-mutate round trip, and a helper that forgets to publish — and must report
// every one of them as unauthorized.
func TestD4ScanCatchesPlantedViolations(t *testing.T) {
	base, err := d4ScanSources(d4PackageDir, nil)
	if err != nil {
		t.Fatalf("baseline scan: %v", err)
	}
	baseOwned := countsOf(base.owned)
	if len(baseOwned) == 0 {
		t.Fatal("the baseline scan found no controller-owned site; the injected violations below would prove nothing")
	}

	// setPlantedLocked is planted as a helper: it has the *Locked name and the
	// single-responsibility shape, so the control must judge it by the same rule
	// as the nine real helpers. It is registered for this scan only, never in
	// state shared with another test.
	sc, err := d4ScanSourcesWithHelpers(d4PackageDir,
		map[string]string{"zz_planted.go": d4PlantedViolationsSrc("")},
		d4WithPublicationHelpers("setPlantedLocked"))
	if err != nil {
		t.Fatalf("scan with planted violations: %v", err)
	}

	got := countsOf(sc.owned)
	want := d4Inventory{}
	for k, v := range baseOwned {
		want[k] = v
	}
	for _, key := range d4PlantedKeys {
		want[key] = want[key] + 1
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the scan did not detect exactly the planted violations on top of the inventory.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got.lines(), "\n  "), strings.Join(want.lines(), "\n  "))
	}

	// Each planted site must also be reported as unauthorized, which is what
	// turns detection into a failing gate rather than a count.
	for _, site := range sc.owned {
		if site.File != "zz_planted.go" {
			continue
		}
		if reason, ok := sc.authorizationOf(site); ok {
			t.Errorf("planted site %s in %s was authorized by the control (%s)", site.Target, site.Func, reason)
		}
	}
	if got, want := len(sc.foreign)-len(base.foreign), 1; got != want {
		t.Errorf("the planted copy-mutate round trip produced %d foreign detections, want %d (the fabricated pointee)", got, want)
	}
}

// TestD4ScanDoesNotInventViolations is the negative half of the planted run: the
// same writes, each followed by a publication, must add no unauthorized site and
// must leave the repository inventory unchanged. Without it the planted test
// could pass by flagging anything at all.
func TestD4ScanDoesNotInventViolations(t *testing.T) {
	base, err := d4ScanSources(d4PackageDir, nil)
	if err != nil {
		t.Fatalf("baseline scan: %v", err)
	}
	sc, err := d4ScanSourcesWithHelpers(d4PackageDir,
		map[string]string{"zz_planted.go": d4PlantedViolationsSrc("ic.publishLocked()")},
		d4WithPublicationHelpers("setPlantedLocked"))
	if err != nil {
		t.Fatalf("scan with the corrected file: %v", err)
	}

	// Detection is not gated on publication: the same sites are still found.
	found := map[string]int{}
	for _, site := range sc.owned {
		if site.File != "zz_planted.go" {
			continue
		}
		found[site.Func]++
		if reason, ok := sc.authorizationOf(site); !ok {
			t.Errorf("published site %s in %s is still reported unauthorized: %s", site.Target, site.Func, reason)
		}
	}
	for _, key := range d4PlantedKeys {
		fn := strings.SplitN(key[strings.Index(key, "#")+1:], ".", 2)[0]
		if found[fn] == 0 {
			t.Errorf("the corrected fixture no longer detects %s; the control counts, it does not look", fn)
		}
	}

	// And the repository inventory must still be exactly itself: what fails the
	// gate is a missing publication, not the presence of the file. Every site
	// outside the planted file must still match the reviewed inventory.
	filtered := d4Inventory{}
	for _, site := range sc.owned {
		if site.File == "zz_planted.go" {
			continue
		}
		filtered[site.key()]++
	}
	if !reflect.DeepEqual(filtered, countsOf(base.owned)) {
		t.Errorf("the planted file changed the repository inventory itself.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(filtered.lines(), "\n  "), strings.Join(countsOf(base.owned).lines(), "\n  "))
	}
}

// TestD4ScanFailsOnAVanishedPublication is requirement 4's other direction,
// executed rather than implied: the multiset comparison catches a site that
// disappears, but a site that stays while losing its publication must also
// fail. The control re-scans supervisor.go with setStateLocked's publishLocked
// call deleted — the exact regression §D4 was written to prevent, "forgot to
// publish" as a review finding rather than a structural impossibility.
//
// The deletion is made line-wise, never with a byte string that spans a line
// break, and it runs over both the LF and the CRLF spelling of the same
// content: the fixture must not depend on how a checkout terminates lines. The
// published `38070d86` proved that a byte-string needle does, because a Windows
// checkout re-terminates the source while Linux keeps it as committed. Each
// direction is also paired with a scan of the unmutated content of the same
// variant, so "the scanner rejected the mutation" cannot be satisfied by a scan
// that stopped authorizing anything at all.
func TestD4ScanFailsOnAVanishedPublication(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(d4PackageDir, "supervisor.go"))
	if err != nil {
		t.Fatalf("read supervisor.go: %v", err)
	}
	lf := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, variant := range []struct{ name, content string }{
		{"LF", lf},
		{"CRLF", strings.ReplaceAll(lf, "\n", "\r\n")},
	} {
		patched, ok := d4DropLineAfter(variant.content,
			"\tic.instance.UpdateState(state)",
			"\tic.publishLocked()")
		if !ok {
			t.Fatalf("%s variant: setStateLocked no longer has the UpdateState + publishLocked shape the control removes; the vanished-publication fixture would prove nothing", variant.name)
		}
		assertPublicationRemovalIsRejected(t, variant.name, variant.content, patched)
	}
}

// d4DropLineAfter removes the first line equal to second that immediately
// follows a line equal to first, comparing lines with their terminators stripped
// so the match cannot depend on the source's line endings. Every other line,
// terminators included, comes back byte-for-byte unchanged.
func d4DropLineAfter(src, first, second string) (string, bool) {
	lines := strings.SplitAfter(src, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") != first ||
			strings.TrimRight(lines[i+1], "\r\n") != second {
			continue
		}
		out := make([]string, 0, len(lines)-1)
		out = append(out, lines[:i+1]...)
		out = append(out, lines[i+2:]...)
		return strings.Join(out, ""), true
	}
	return "", false
}

// assertPublicationRemovalIsRejected proves, for one line-ending variant, that
// setStateLocked's mutation is authorized before the removal, is still detected
// after it, and is rejected after it.
func assertPublicationRemovalIsRejected(t *testing.T, variant, content, patched string) {
	t.Helper()

	// authorize scans `src` as supervisor.go and returns the scan with the
	// setStateLocked site it must still detect.
	authorize := func(src string) (*d4Scan, d4Site) {
		t.Helper()
		sc, err := d4ScanSources(d4PackageDir, map[string]string{"supervisor.go": src})
		if err != nil {
			t.Fatalf("%s variant: scan: %v", variant, err)
		}
		for _, site := range d4SitesIn(sc.owned, "supervisor.go") {
			if site.Func == "setStateLocked" {
				return sc, site
			}
		}
		t.Fatalf("%s variant: the scan stopped detecting setStateLocked's UpdateState write; a lost publication must not make a mutation invisible", variant)
		return nil, d4Site{}
	}

	sc, site := authorize(content)
	if _, ok := sc.authorizationOf(site); !ok {
		t.Fatalf("%s variant: setStateLocked's mutation is not authorized even before the removal, so rejecting it after the removal would prove nothing", variant)
	}

	sc, site = authorize(patched)
	if reason, ok := sc.authorizationOf(site); ok {
		t.Fatalf("%s variant: setStateLocked's mutation was still authorized after its publication was deleted (%s)", variant, reason)
	}
}

// TestD4ScanIgnoresNonControllerLookAlikes pins the requirement-3 boundary with
// planted non-owners: a value copy inside a view-shaped struct, a fabricated
// LaunchInstance, a store-derived pointer and a foreign parameter must never
// enter the controller-owned inventory, and must be seen as what they are.
func TestD4ScanIgnoresNonControllerLookAlikes(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, map[string]string{
		"zz_planted_lookalikes.go": `package process

import (
	"time"

	"github.com/dsdred/goal/internal/domain"
)

type plantedView struct {
	instance domain.LaunchInstance
}

func plantedValueHolderWrites() {
	view := &plantedView{}
	view.instance.Args = nil
	snap := view.instance
	snap.State = domain.InstanceStateRunning
}

func plantedFabricatedPointer() {
	fresh := &domain.LaunchInstance{}
	fresh.PID = 1
	fresh.UpdateState(domain.InstanceStateRunning)
}

func plantedStorePointer() {
	inst := domain.ToDomain(nil)
	inst.StartedAt = time.Now()
	inst.UpdateError("", "")
}

func plantedForeignParameter(inst *domain.LaunchInstance) {
	inst.Executable = ""
	inst.Fail("", "")
}
`,
	})
	if err != nil {
		t.Fatalf("scan with look-alike fixtures: %v", err)
	}

	if planted := d4SitesIn(sc.owned, "zz_planted_lookalikes.go"); len(planted) != 0 {
		lines := make([]string, 0, len(planted))
		for _, s := range planted {
			lines = append(lines, s.key())
		}
		t.Fatalf("look-alike receivers were classified as controller-owned:\n  %s", strings.Join(lines, "\n  "))
	}

	foreign := countsOf(sc.foreign)
	for _, key := range []string{
		"zz_planted_lookalikes.go#plantedFabricatedPointer.instance.PID[assignment]",
		"zz_planted_lookalikes.go#plantedFabricatedPointer.instance.UpdateState[method-call]",
		"zz_planted_lookalikes.go#plantedStorePointer.instance.StartedAt[assignment]",
		"zz_planted_lookalikes.go#plantedStorePointer.instance.UpdateError[method-call]",
		"zz_planted_lookalikes.go#plantedForeignParameter.instance.Executable[assignment]",
		"zz_planted_lookalikes.go#plantedForeignParameter.instance.Fail[method-call]",
	} {
		if foreign[key] == 0 {
			t.Errorf("the reviewed non-controller set lost %s; the scan must still see these writes", key)
		}
	}

	values := countsOf(sc.valueCopy)
	for _, key := range []string{
		"zz_planted_lookalikes.go#plantedValueHolderWrites.instance.Args[assignment]",
		"zz_planted_lookalikes.go#plantedValueHolderWrites.instance.State[assignment]",
	} {
		if values[key] == 0 {
			t.Errorf("the reviewed value-copy set lost %s; the view's own copies must stay visible", key)
		}
	}

	if planted := d4SitesIn(sc.unresolved, "zz_planted_lookalikes.go"); len(planted) != 0 {
		t.Errorf("look-alike fixtures produced unresolved receivers:\n%s", strings.Join(d4SiteLines(planted), "\n"))
	}
}

// d4SitesIn filters a detected set down to one file, which is how the planted
// fixtures assert their own behaviour without re-asserting the repository.
func d4SitesIn(sites []d4Site, file string) []d4Site {
	out := make([]d4Site, 0, len(sites))
	for _, s := range sites {
		if s.File == file {
			out = append(out, s)
		}
	}
	return out
}

// TestD4ScanFailsClosedOnAnUnattributedControllerReceiver is requirement 4's
// silence-by-non-match guard: a second type that owns a
// *domain.LaunchInstance field must be reported as unresolved, not skipped,
// while a declared struct that merely shares a field name must stay invisible.
func TestD4ScanFailsClosedOnAnUnattributedControllerReceiver(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, map[string]string{
		"zz_planted_shadow.go": `package process

import (
	"github.com/dsdred/goal/internal/domain"
)

type plantedShadowController struct {
	instance *domain.LaunchInstance
}

func (p *plantedShadowController) writeThroughShadow() {
	p.instance.State = domain.InstanceStateRunning
}

func (p *plantedShadowController) methodCallThroughShadow() {
	p.instance.Fail("", "")
}

func plantedUnattributedRoot() {
	var mystery mysteryHolder
	mystery.State = domain.InstanceStateRunning
}

type mysteryHolder struct {
	State domain.InstanceState
}

func plantedCallWithNoIndexedResult() {
	hidden := unindexedFactory()
	hidden.ExitClass = domain.InstanceExitClassSignaled
}

func unindexedFactory() { panic("fixture only") }
`,
	})
	if err != nil {
		t.Fatalf("scan with a shadow controller: %v", err)
	}
	if planted := d4SitesIn(sc.owned, "zz_planted_shadow.go"); len(planted) != 0 {
		t.Errorf("a shadow controller receiver was inventoried as production-owned:\n%s", strings.Join(d4SiteLines(planted), "\n"))
	}

	wantFuncs := map[string]bool{"writeThroughShadow": false, "methodCallThroughShadow": false}
	for _, s := range d4SitesIn(sc.unresolved, "zz_planted_shadow.go") {
		if _, ok := wantFuncs[s.Func]; ok {
			wantFuncs[s.Func] = true
		}
	}
	for fn, seen := range wantFuncs {
		if !seen {
			t.Errorf("the scan silently ignored a write through the unattributed receiver in %s; unresolved set:\n%s",
				fn, strings.Join(d4SiteLines(sc.unresolved), "\n"))
		}
	}

	// A declared struct that only shares a field name is not controller state,
	// and an unresolvable receiver must fail closed rather than vanish.
	inUnresolved := map[string]bool{}
	for _, s := range sc.unresolved {
		inUnresolved[s.Func] = true
	}
	if inUnresolved["plantedUnattributedRoot"] {
		t.Errorf("a write to a declared non-lifecycle struct was reported as unresolved")
	}
	if !inUnresolved["plantedCallWithNoIndexedResult"] {
		t.Errorf("a write through a receiver the scan cannot type was not reported; the control failed open")
	}
}

// TestD4ScanDocumentsItsOwnBlindSpots keeps AH-1 honest. The control cannot see
// an in-place mutation of a map or slice reached through the view, because that
// is not a write to a field of a controller-owned receiver. This test records
// the boundary as an executed fact instead of leaving it as prose.
func TestD4ScanDocumentsItsOwnBlindSpots(t *testing.T) {
	sc, err := d4ScanSources(d4PackageDir, map[string]string{
		"zz_planted_blind_spot.go": `package process

func (ic *InstanceController) mutateThroughView() {
	ic.instance.Environment["LEAK"] = "value"
	ic.instance.Args = append(ic.instance.Args, "--leak")
	store := ic.store
	_ = store
}
`,
	})
	if err != nil {
		t.Fatalf("scan with the blind-spot fixture: %v", err)
	}
	// The append-assign rebinds Args, so it is a field write and is caught; the
	// map index assignment is not, and is the documented residual.
	caught := false
	for _, s := range sc.owned {
		if s.Func == "mutateThroughView" && strings.HasSuffix(s.Target, "Args") {
			caught = true
		}
	}
	if !caught {
		t.Errorf("the scan no longer catches a rebind of a view-carried slice; §D4 item 4's coverage claim changed")
	}
	for _, s := range append(sc.owned, sc.foreign...) {
		if s.Func == "mutateThroughView" && strings.Contains(s.Target, "Environment") {
			t.Errorf("the scan now claims to see an in-place map mutation; update the blind-spot record")
		}
	}
}
