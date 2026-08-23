package repomap

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Kind classifies an extracted symbol.
const (
	KindPackage   = "package"
	KindFunc      = "func"
	KindMethod    = "method"
	KindType      = "type"
	KindClass     = "class"
	KindConst     = "const"
	KindVar       = "var"
	KindInterface = "interface"
)

// Symbol is one top-level declaration extracted from a source file.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"sig"`
	Line      int    `json:"line"`
	Exported  bool   `json:"exported"`
	Receiver  string `json:"recv,omitempty"`
}

// File is the extracted view of a single source file.
type File struct {
	Path     string   `json:"path"` // repo-relative, slash separated
	Lang     string   `json:"lang"`
	Package  string   `json:"pkg,omitempty"`
	Imports  []string `json:"imports,omitempty"`
	Symbols  []Symbol `json:"symbols,omitempty"`
	Refs     []string `json:"refs,omitempty"` // identifiers referenced but not defined here
	Size     int64    `json:"size"`
	ModTime  int64    `json:"mtime"`
	Rank     float64  `json:"-"`
	Language string   `json:"-"` // alias kept for readability in callers
}

// LangForPath maps a file extension to an extractor language id. The table
// lives in extract_lang.go so a new language is one entry, not three edits.
func LangForPath(rel string) string {
	return langByExt[strings.ToLower(filepath.Ext(rel))]
}

// Languages lists every language id with a symbol extractor, sorted.
func Languages() []string {
	out := make([]string, 0, len(langSpecs))
	for _, spec := range langSpecs {
		out = append(out, spec.id)
	}
	sort.Strings(out)
	return out
}

var (
	// Go
	goPackageRe = regexp.MustCompile(`^package\s+([A-Za-z_][A-Za-z0-9_]*)`)
	goFuncRe    = regexp.MustCompile(`^func\s+(?:\(([^)]*)\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\(`)
	goTypeRe    = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\s+(\w+)?`)
	goConstRe   = regexp.MustCompile(`^(const|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s`)
	goImportRe  = regexp.MustCompile(`^\s*(?:[A-Za-z_.][A-Za-z0-9_]*\s+)?"([^"]+)"`)
	// Grouped `const ( … )` / `var ( … )` / `type ( … )` declarations.
	goGroupOpenRe = regexp.MustCompile(`^(const|var|type)\s*\($`)
	// A member line inside such a block is `Name = v`, `Name Type = v`,
	// `Name Type`, or a bare `Name` continuing an iota run.
	goGroupMemberRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\b`)

	// Python
	pyDefRe   = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyClassRe = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[:(]`)
	// `from .models import User` and `from ..pkg import x` are the intra-package
	// edges the repo graph is built from; the leading dots must be accepted.
	pyImportRe = regexp.MustCompile(`^\s*(?:from\s+(\.*[A-Za-z_][A-Za-z0-9_.]*|\.+)\s+import|import\s+([A-Za-z_][A-Za-z0-9_.]*))`)
	// Module-level constant: UPPER_SNAKE, optionally annotated.
	pyConstRe = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)\s*(?::[^=]+)?=`)

	// JS / TS
	jsFuncRe  = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>]*>)?\s*\(`)
	jsClassRe = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	// The `<T,>(x: T) =>` generic-arrow form is idiomatic in .tsx (the trailing
	// comma disambiguates it from JSX) and the old pattern could not match it.
	jsConstFn  = regexp.MustCompile(`^\s*(export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:<[^>]*>\s*)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]*)?=>`)
	tsTypeRe   = regexp.MustCompile(`^\s*(export\s+)?(?:declare\s+)?(?:const\s+)?(interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	jsImportRe = regexp.MustCompile(`(?:^\s*import\b[^'"]*['"]([^'"]+)['"]|require\(\s*['"]([^'"]+)['"]\s*\))`)
	// `export const X = 1` (a value, not an arrow function) and class members.
	jsConstValRe = regexp.MustCompile(`^\s*(export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=`)
	jsMemberRe   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|readonly|abstract|override|declare|async|get|set)\s+)*\*?\s*([#A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>(]*>)?\s*\(`)

	// Rust
	rustFnRe   = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]*"\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustTypeRe = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(struct|enum|trait|type|impl)\s+(?:<[^>]*>\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	rustUseRe  = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z_][A-Za-z0-9_:]*)`)
	rustModRe  = regexp.MustCompile(`^\s*(?:pub\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)`)
	// impl [<generics>] Trait for Type  |  impl [<generics>] Type
	rustImplRe  = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl\s*(?:<[^>]*>)?\s*([A-Za-z_][A-Za-z0-9_:]*)(?:<[^>]*>)?\s*(?:for\s+([A-Za-z_][A-Za-z0-9_:]*))?`)
	rustConstRe = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(const|static)\s+(?:mut\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	rustMacroRe = regexp.MustCompile(`^\s*macro_rules!\s+([A-Za-z_][A-Za-z0-9_]*)`)

	// Java
	javaPkgRe   = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	javaImpRe   = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_][A-Za-z0-9_.*]*)\s*;`)
	javaTypeRe  = regexp.MustCompile(`^\s*(?:public\s+|protected\s+|private\s+|abstract\s+|final\s+|static\s+)*(class|interface|enum|record)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	javaMethRe  = regexp.MustCompile(`^\s+(?:public|protected|private)\s+(?:static\s+|final\s+|synchronized\s+|abstract\s+|native\s+|default\s+)*(?:<[^>]*>\s*)?[A-Za-z_<>\[\],.\s?]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaCtorRe  = regexp.MustCompile(`^\s*(?:public\s+|protected\s+|private\s+)?([A-Z][A-Za-z0-9_]*)\s*\(`)
	javaFieldRe = regexp.MustCompile(`^\s*(?:public|protected|private)\s+(?:static\s+|final\s+|volatile\s+|transient\s+)*[A-Za-z_][A-Za-z0-9_<>\[\],.?\s]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:=|;)`)
	identifierR = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
)

// ExtractSource parses one source blob. rel is used only for the language
// decision and the recorded path; it never touches the filesystem.
func ExtractSource(rel, src string) File {
	rel = filepath.ToSlash(rel)
	lang := LangForPath(rel)
	f := File{Path: rel, Lang: lang, Language: lang}
	if lang == "" {
		return f
	}
	lines := strings.Split(src, "\n")
	if extract := extractorByID[lang]; extract != nil {
		extract(&f, lines)
	}
	f.Refs = collectRefs(lines, f)
	return f
}

func trimSig(line string) string {
	line = strings.TrimRight(strings.TrimSpace(line), " \t{")
	line = strings.TrimSuffix(line, "{")
	return strings.TrimSpace(line)
}

func isExportedGo(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

func extractGo(f *File, lines []string) {
	inImport := false
	group := "" // "const" | "var" | "type" while inside a grouped declaration
	for i, raw := range lines {
		line := raw
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if m := goPackageRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			continue
		}
		if strings.HasPrefix(trimmed, "import (") {
			inImport = true
			continue
		}
		if inImport {
			if trimmed == ")" {
				inImport = false
				continue
			}
			if m := goImportRe.FindStringSubmatch(line); m != nil {
				f.Imports = append(f.Imports, m[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			if m := goImportRe.FindStringSubmatch(strings.TrimPrefix(trimmed, "import ")); m != nil {
				f.Imports = append(f.Imports, m[1])
			}
			continue
		}
		if m := goFuncRe.FindStringSubmatch(line); m != nil {
			recv := strings.TrimSpace(m[1])
			kind := KindFunc
			if recv != "" {
				kind = KindMethod
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[2]), Receiver: recvType(recv),
			})
			continue
		}
		if m := goTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindType
			if strings.Contains(line, " interface") {
				kind = KindInterface
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[1]),
			})
			continue
		}
		if m := goConstRe.FindStringSubmatch(line); m != nil {
			kind := KindConst
			if m[1] == "var" {
				kind = KindVar
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[2]),
			})
			continue
		}
		// Grouped declarations. Go's idiom for a package's exported constants
		// is `const ( … )`, and the single-line regexes above see none of it —
		// so a package whose entire public surface is a const block used to
		// contribute zero symbols to the repo map.
		if m := goGroupOpenRe.FindStringSubmatch(trimmed); m != nil {
			group = m[1]
			continue
		}
		if group != "" {
			if trimmed == ")" {
				group = ""
				continue
			}
			if m := goGroupMemberRe.FindStringSubmatch(trimmed); m != nil {
				kind := KindConst
				switch group {
				case "var":
					kind = KindVar
				case "type":
					kind = KindType
					if strings.Contains(trimmed, " interface") {
						kind = KindInterface
					}
				}
				f.Symbols = append(f.Symbols, Symbol{
					Name: m[1], Kind: kind, Signature: trimSig(line),
					Line: i + 1, Exported: isExportedGo(m[1]),
				})
			}
		}
	}
}

func recvType(recv string) string {
	recv = strings.TrimSpace(recv)
	if recv == "" {
		return ""
	}
	fields := strings.Fields(recv)
	t := fields[len(fields)-1]
	t = strings.TrimLeft(t, "*")
	if i := strings.IndexByte(t, '['); i > 0 {
		t = t[:i]
	}
	return t
}

func extractPython(f *File, lines []string) {
	// Python scopes by indentation, so the enclosing class is tracked with a
	// stack of (name, indent) frames. Three things the line-local scanner got
	// wrong and this fixes: methods had no receiver (so a repo map named
	// `save` with no hint of which class owns it), a helper `def` nested inside
	// a FUNCTION was reported as a method, and relative imports — the very
	// edges that make the repo graph useful — were dropped entirely.
	type frame struct {
		name   string
		indent int
	}
	var classes []frame
	funcIndent := -1
	owner := func() string {
		if len(classes) == 0 {
			return ""
		}
		return classes[len(classes)-1].name
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := pyImportRe.FindStringSubmatch(line); m != nil {
			mod := m[1]
			if mod == "" {
				mod = m[2]
			}
			if mod != "" {
				f.Imports = append(f.Imports, mod)
			}
			continue
		}
		isClass := pyClassRe.MatchString(line)
		isDef := pyDefRe.MatchString(line)
		if !isClass && !isDef {
			if funcIndent < 0 && len(classes) == 0 {
				if m := pyConstRe.FindStringSubmatch(line); m != nil {
					f.Symbols = append(f.Symbols, Symbol{
						Name: m[1], Kind: KindConst, Signature: trimSig(line),
						Line: i + 1, Exported: !strings.HasPrefix(m[1], "_"),
					})
				}
			}
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		for len(classes) > 0 && classes[len(classes)-1].indent >= indent {
			classes = classes[:len(classes)-1]
		}
		if funcIndent >= 0 && indent > funcIndent {
			continue // helper defined inside a function body: not API
		}
		if funcIndent >= 0 && indent <= funcIndent {
			funcIndent = -1
		}
		name := ""
		kind := KindClass
		if isClass {
			name = pyClassRe.FindStringSubmatch(line)[2]
		} else {
			name = pyDefRe.FindStringSubmatch(line)[2]
			kind = KindFunc
			if owner() != "" {
				kind = KindMethod
			}
		}
		f.Symbols = append(f.Symbols, Symbol{
			Name: name, Kind: kind,
			Signature: trimSig(strings.TrimSuffix(trimmed, ":")),
			Line:      i + 1, Exported: !strings.HasPrefix(name, "_"), Receiver: owner(),
		})
		if isClass {
			classes = append(classes, frame{name: name, indent: indent})
		} else {
			funcIndent = indent
		}
	}
}

func extractJS(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
			sc.advance(line)
			continue
		}
		if m := jsImportRe.FindStringSubmatch(line); m != nil {
			mod := m[1]
			if mod == "" {
				mod = m[2]
			}
			if mod != "" {
				f.Imports = append(f.Imports, mod)
			}
		}
		switch {
		case jsClassRe.MatchString(line):
			m := jsClassRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindClass, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		case jsFuncRe.MatchString(line):
			m := jsFuncRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case tsTypeRe.MatchString(line):
			m := tsTypeRe.FindStringSubmatch(line)
			kind := KindType
			if m[2] == "interface" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[3], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case jsConstFn.MatchString(line):
			// `export const Card: React.FC<P> = ({x}) => …` and the generic
			// `export const useThing = <T,>(x: T) => …` are both extremely
			// common in real TS and neither is a `function` declaration.
			m := jsConstFn.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case jsConstValRe.MatchString(line) && sc.depth == 0:
			m := jsConstValRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindConst, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case sc.atMemberLevel():
			// Class members. Without these a repo map of an OO TypeScript file
			// names the class and nothing you can actually call on it.
			if m := jsMemberRe.FindStringSubmatch(line); m != nil && !isControlWord(m[1]) {
				addSymbol(f, Symbol{
					Name: m[1], Kind: KindMethod, Signature: trimSig(line),
					Line: i + 1, Exported: !strings.HasPrefix(m[1], "#") && !hasPrivateModifier(trimmed, m[1]),
					Receiver: sc.owner,
				})
			}
		}
		sc.advance(line)
	}
}

func extractRust(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			sc.advance(line)
			continue
		}
		if m := rustUseRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			sc.advance(line)
			continue
		}
		if m := rustModRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, "crate::"+m[1])
		}
		pub := strings.HasPrefix(trimmed, "pub")
		// `impl<T: Send> Config<T>` and `impl Store for Config<u8>` are the
		// shapes the old single regex could not read: the first because the
		// generics follow `impl` with no space, the second because it recorded
		// the TRAIT as the type. Both matter — an impl block is where a Rust
		// crate's methods live.
		if m := rustImplRe.FindStringSubmatch(line); m != nil {
			// `impl Trait for Type` names the TYPE; the trait it satisfies is
			// recorded as the receiver so a search for either finds this block.
			target, trait := m[1], ""
			if m[2] != "" {
				target, trait = m[2], m[1]
			}
			addSymbol(f, Symbol{
				Name: target, Kind: KindClass, Signature: trimSig(line),
				Line: i + 1, Exported: true, Receiver: trait,
			})
			sc.enter(target)
			sc.advance(line)
			continue
		}
		if m := rustFnRe.FindStringSubmatch(line); m != nil {
			kind := KindFunc
			if sc.owner != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.TrimSpace(m[1]) != "", Receiver: sc.owner,
			})
			sc.advance(line)
			continue
		}
		if m := rustTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindType
			if m[2] == "trait" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[3], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.TrimSpace(m[1]) != "",
			})
			if m[2] == "trait" && strings.Contains(line, "{") {
				sc.enter(m[3])
			}
			sc.advance(line)
			continue
		}
		switch {
		case rustConstRe.MatchString(line):
			m := rustConstRe.FindStringSubmatch(line)
			kind := KindConst
			if m[2] == "static" {
				kind = KindVar
			}
			addSymbol(f, Symbol{
				Name: m[3], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.TrimSpace(m[1]) != "",
			})
		case rustMacroRe.MatchString(line):
			m := rustMacroRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: pub || strings.Contains(trimmed, "#[macro_export]"),
			})
		}
		sc.advance(line)
	}
}

func extractJava(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "@") {
			sc.advance(line)
			continue
		}
		if m := javaPkgRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			sc.advance(line)
			continue
		}
		if m := javaImpRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			sc.advance(line)
			continue
		}
		if m := javaTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindClass
			if m[1] == "interface" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.Contains(line, "public"), Receiver: sc.memberOwner(),
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		}
		public := strings.Contains(line, "public")
		switch {
		// A constructor has no return type, so the method regex never saw it —
		// and a constructor is exactly what a caller needs to know about.
		case sc.owner != "" && javaCtorRe.MatchString(line) &&
			javaCtorRe.FindStringSubmatch(line)[1] == sc.owner:
			addSymbol(f, Symbol{
				Name: sc.owner, Kind: KindMethod, Signature: trimSig(line),
				Line: i + 1, Exported: public, Receiver: sc.owner,
			})
		case javaMethRe.MatchString(line):
			m := javaMethRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindMethod, Signature: trimSig(line),
				Line: i + 1, Exported: public, Receiver: sc.owner,
			})
		case sc.atMemberLevel() && javaFieldRe.MatchString(line):
			m := javaFieldRe.FindStringSubmatch(line)
			kind := KindVar
			if strings.Contains(line, "final") {
				kind = KindConst
			}
			addSymbol(f, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: public, Receiver: sc.owner,
			})
		}
		sc.advance(line)
	}
}

// collectRefs gathers identifiers used in the file that it does not itself
// define. These become the graph edges: file A references symbol S, file B
// defines S ⇒ edge A→B.
func collectRefs(lines []string, f File) []string {
	defined := map[string]bool{}
	for _, s := range f.Symbols {
		defined[s.Name] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") ||
			strings.HasPrefix(t, "*") {
			continue
		}
		for _, id := range identifierR.FindAllString(line, -1) {
			if defined[id] || seen[id] || isCommonWord(id) {
				continue
			}
			seen[id] = true
			out = append(out, id)
			if len(out) >= 400 {
				return out
			}
		}
	}
	return out
}

var commonWords = map[string]bool{
	"func": true, "package": true, "import": true, "return": true, "string": true,
	"error": true, "nil": true, "true": true, "false": true, "int": true, "bool": true,
	"var": true, "const": true, "type": true, "struct": true, "interface": true,
	"map": true, "range": true, "for": true, "if": true, "else": true, "switch": true,
	"case": true, "default": true, "break": true, "continue": true, "defer": true,
	"len": true, "make": true, "append": true, "self": true, "this": true, "def": true,
	"class": true, "from": true, "and": true, "not": true, "None": true, "let": true,
	"fmt": true, "pub": true, "use": true, "mod": true, "impl": true, "fn": true,
	"public": true, "private": true, "static": true, "void": true, "new": true,
	"const_": true, "float64": true, "float32": true, "int64": true, "byte": true,
	"the": true, "with": true, "that": true, "this_": true, "value": true,
}

func isCommonWord(id string) bool { return commonWords[id] }
