package repomap

import (
	"regexp"
	"strings"
)

// Extractors for the languages the original scanner had no answer for
// (C#, Kotlin, Ruby, PHP, Swift, C/C++, shell) plus the language dispatch
// table both this file and extract.go read, so adding a language is one entry
// rather than three edits spread across a switch, a map and an if-chain.

// langSpec binds a language id to its file extensions and its scanner.
type langSpec struct {
	id      string
	exts    []string
	extract func(*File, []string)
}

var langSpecs = []langSpec{
	{"go", []string{".go"}, extractGo},
	{"python", []string{".py", ".pyi"}, extractPython},
	{"javascript", []string{".js", ".jsx", ".mjs", ".cjs"}, extractJS},
	{"typescript", []string{".ts", ".tsx", ".mts", ".cts"}, extractJS},
	{"rust", []string{".rs"}, extractRust},
	{"java", []string{".java"}, extractJava},
	{"csharp", []string{".cs", ".csx"}, extractCSharp},
	{"kotlin", []string{".kt", ".kts"}, extractKotlin},
	{"swift", []string{".swift"}, extractSwift},
	{"ruby", []string{".rb", ".rake"}, extractRuby},
	{"php", []string{".php", ".phtml"}, extractPHP},
	{"cpp", []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"}, extractCFamily},
	{"shell", []string{".sh", ".bash", ".zsh"}, extractShell},
}

var (
	langByExt     = map[string]string{}
	extractorByID = map[string]func(*File, []string){}
)

func init() {
	for _, spec := range langSpecs {
		extractorByID[spec.id] = spec.extract
		for _, e := range spec.exts {
			langByExt[e] = spec.id
		}
	}
}

// ---------------------------------------------------------------------------
// C# / .NET
// ---------------------------------------------------------------------------

var (
	csNamespaceRe = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	csUsingRe     = regexp.MustCompile(`^\s*(?:global\s+)?using\s+(?:static\s+)?(?:[A-Za-z_][A-Za-z0-9_]*\s*=\s*)?([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	csTypeRe      = regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|internal|private|protected|abstract|sealed|static|partial|readonly|file)\s+)*(class|interface|struct|record|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	csMemberRe    = regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|internal|private|protected|static|virtual|override|abstract|sealed|async|extern|unsafe|new|partial|required|readonly)\s+)*(?:[A-Za-z_][A-Za-z0-9_<>,\[\]\?\.\s]*\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>(]*>)?\s*\(`)
	csPropRe      = regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|internal|private|protected|static|virtual|override|abstract|required|readonly)\s+)+[A-Za-z_][A-Za-z0-9_<>,\[\]\?\.]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\{\s*(?:get|set|init)|=>)`)
)

func extractCSharp(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "//"):
			sc.advance(line)
			continue
		case csNamespaceRe.MatchString(line):
			f.Package = csNamespaceRe.FindStringSubmatch(line)[1]
			sc.advance(line)
			continue
		case csUsingRe.MatchString(line):
			f.Imports = append(f.Imports, csUsingRe.FindStringSubmatch(line)[1])
			sc.advance(line)
			continue
		}
		if m := csTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindClass
			if m[1] == "interface" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: strings.Contains(line, "public"), Receiver: sc.memberOwner(),
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		}
		if sc.atMemberLevel() {
			if m := csPropRe.FindStringSubmatch(line); m != nil {
				addSymbol(f, Symbol{
					Name: m[1], Kind: KindVar, Signature: trimSig(line), Line: i + 1,
					Exported: strings.Contains(line, "public"), Receiver: sc.owner,
				})
				sc.advance(line)
				continue
			}
			if m := csMemberRe.FindStringSubmatch(line); m != nil && !strings.HasSuffix(trimmed, ";") {
				addSymbol(f, Symbol{
					Name: m[1], Kind: KindMethod, Signature: trimSig(line), Line: i + 1,
					Exported: strings.Contains(line, "public"), Receiver: sc.owner,
				})
				sc.advance(line)
				continue
			}
		}
		sc.advance(line)
	}
}

// ---------------------------------------------------------------------------
// Kotlin
// ---------------------------------------------------------------------------

var (
	ktPackageRe = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	ktImportRe  = regexp.MustCompile(`^\s*import\s+([A-Za-z_][A-Za-z0-9_.*]*)`)
	ktTypeRe    = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|protected|open|abstract|sealed|final|inner|data|value|enum|annotation|companion)\s+)*(class|interface|object)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	ktFunRe     = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|protected|open|override|abstract|final|inline|suspend|operator|infix|tailrec|external|expect|actual)\s+)*fun\s*(?:<[^>]*>\s*)?(?:[A-Za-z_][A-Za-z0-9_.<>]*\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	ktPropRe    = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|protected|open|override|const|lateinit)\s+)*(val|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	ktTypeAlias = regexp.MustCompile(`^\s*(?:(?:public|private|internal)\s+)?typealias\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func extractKotlin(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			sc.advance(line)
			continue
		}
		if m := ktPackageRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			sc.advance(line)
			continue
		}
		if m := ktImportRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			sc.advance(line)
			continue
		}
		private := hasPrivateModifier(trimmed, "class", "interface", "object", "fun", "val", "var")
		switch {
		case ktTypeRe.MatchString(line):
			m := ktTypeRe.FindStringSubmatch(line)
			kind := KindClass
			if m[1] == "interface" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: !private, Receiver: sc.memberOwner(),
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		case ktFunRe.MatchString(line):
			m := ktFunRe.FindStringSubmatch(line)
			kind := KindFunc
			if sc.owner != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: !private, Receiver: sc.owner,
			})
		case ktTypeAlias.MatchString(line):
			m := ktTypeAlias.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindType, Signature: trimSig(line), Line: i + 1,
				Exported: !private,
			})
		case sc.depth == 0 || sc.atMemberLevel():
			if m := ktPropRe.FindStringSubmatch(line); m != nil {
				kind := KindVar
				if m[1] == "val" {
					kind = KindConst
				}
				addSymbol(f, Symbol{
					Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
					Exported: !private, Receiver: sc.owner,
				})
			}
		}
		sc.advance(line)
	}
}

// ---------------------------------------------------------------------------
// Swift
// ---------------------------------------------------------------------------

var (
	swImportRe = regexp.MustCompile(`^\s*import\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	swTypeRe   = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|fileprivate|open|final|indirect)\s+)*(class|struct|enum|protocol|actor|extension)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	swFuncRe   = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|fileprivate|open|static|class|final|override|mutating|nonmutating|convenience|required)\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)`)
	swInitRe   = regexp.MustCompile(`^\s*(?:(?:public|private|internal|fileprivate|open|required|convenience)\s+)*(init)\??\s*\(`)
	swPropRe   = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|private|internal|fileprivate|open|static|class|final|lazy|weak|unowned)\s+)*(let|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	swAliasRe  = regexp.MustCompile(`^\s*(?:(?:public|private|internal)\s+)?typealias\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func extractSwift(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			sc.advance(line)
			continue
		}
		if m := swImportRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			sc.advance(line)
			continue
		}
		// Swift's default access level is `internal` — visible in the module but
		// not outside it — so "exported" here means explicitly public or open.
		exported := strings.Contains(trimmed, "public ") || strings.Contains(trimmed, "open ")
		switch {
		case swTypeRe.MatchString(line):
			m := swTypeRe.FindStringSubmatch(line)
			kind := KindClass
			switch m[1] {
			case "protocol":
				kind = KindInterface
			case "struct", "enum":
				kind = KindType
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: exported, Receiver: sc.memberOwner(),
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		case swFuncRe.MatchString(line):
			m := swFuncRe.FindStringSubmatch(line)
			kind := KindFunc
			if sc.owner != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: exported, Receiver: sc.owner,
			})
		case swInitRe.MatchString(line) && sc.owner != "":
			addSymbol(f, Symbol{
				Name: "init", Kind: KindMethod, Signature: trimSig(line), Line: i + 1,
				Exported: exported, Receiver: sc.owner,
			})
		case swAliasRe.MatchString(line):
			m := swAliasRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindType, Signature: trimSig(line), Line: i + 1, Exported: exported,
			})
		case sc.depth == 0 || sc.atMemberLevel():
			if m := swPropRe.FindStringSubmatch(line); m != nil {
				kind := KindVar
				if m[1] == "let" {
					kind = KindConst
				}
				addSymbol(f, Symbol{
					Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
					Exported: exported, Receiver: sc.owner,
				})
			}
		}
		sc.advance(line)
	}
}

// ---------------------------------------------------------------------------
// Ruby (indentation- and keyword-scoped, not brace-scoped)
// ---------------------------------------------------------------------------

var (
	rbClassRe   = regexp.MustCompile(`^(\s*)class\s+([A-Z][A-Za-z0-9_:]*)`)
	rbModuleRe  = regexp.MustCompile(`^(\s*)module\s+([A-Z][A-Za-z0-9_:]*)`)
	rbDefRe     = regexp.MustCompile(`^(\s*)def\s+(?:self\.)?([A-Za-z_][A-Za-z0-9_]*[?!=]?)`)
	rbRequireRe = regexp.MustCompile(`^\s*require(?:_relative)?\s+['"]([^'"]+)['"]`)
	rbAttrRe    = regexp.MustCompile(`^\s*attr_(?:reader|writer|accessor)\s+(.+)$`)
	rbConstRe   = regexp.MustCompile(`^\s*([A-Z][A-Z0-9_]{1,})\s*=`)
	rbSymbolRe  = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)
)

func extractRuby(f *File, lines []string) {
	// Ruby closes every block with `end`, so the enclosing class is tracked by
	// the indentation of its `class` line rather than by braces.
	type frame struct {
		name    string
		indent  int
		private bool
	}
	var stack []frame
	owner := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].name
	}
	// `private` in Ruby is a section marker, not a per-method modifier: every
	// def after it in the same body is private.
	inPrivate := func() bool {
		return len(stack) > 0 && stack[len(stack)-1].private
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		// `end` at or left of the opening indent closes that class/module body.
		if trimmed == "end" {
			for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
				break
			}
			continue
		}
		if trimmed == "private" || trimmed == "protected" {
			if len(stack) > 0 {
				stack[len(stack)-1].private = true
			}
			continue
		}
		if m := rbRequireRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			continue
		}
		if m := rbClassRe.FindStringSubmatch(line); m != nil {
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindClass, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: owner(),
			})
			stack = append(stack, frame{name: m[2], indent: indent})
			continue
		}
		if m := rbModuleRe.FindStringSubmatch(line); m != nil {
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindType, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: owner(),
			})
			stack = append(stack, frame{name: m[2], indent: indent})
			continue
		}
		if m := rbDefRe.FindStringSubmatch(line); m != nil {
			kind := KindFunc
			if owner() != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: !inPrivate() && !strings.HasPrefix(m[2], "_"), Receiver: owner(),
			})
			continue
		}
		if m := rbAttrRe.FindStringSubmatch(line); m != nil {
			// attr_accessor :name, :size — each symbol is a real accessor pair.
			for _, sm := range rbSymbolRe.FindAllStringSubmatch(m[1], -1) {
				addSymbol(f, Symbol{
					Name: sm[1], Kind: KindVar, Signature: trimSig(line), Line: i + 1,
					Exported: true, Receiver: owner(),
				})
			}
			continue
		}
		if m := rbConstRe.FindStringSubmatch(line); m != nil {
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindConst, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: owner(),
			})
		}
	}
}

// ---------------------------------------------------------------------------
// PHP
// ---------------------------------------------------------------------------

var (
	phpNsRe     = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][A-Za-z0-9_\\]*)\s*;`)
	phpUseRe    = regexp.MustCompile(`^\s*use\s+(?:function\s+|const\s+)?([A-Za-z_][A-Za-z0-9_\\]*)`)
	phpTypeRe   = regexp.MustCompile(`^\s*(?:(?:final|abstract|readonly)\s+)*(class|interface|trait|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	phpFuncRe   = regexp.MustCompile(`^\s*(?:(?:final|abstract|public|protected|private|static)\s+)*function\s+&?\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	phpConstRe  = regexp.MustCompile(`^\s*(?:(?:public|protected|private|final)\s+)*const\s+([A-Za-z_][A-Za-z0-9_]*)`)
	phpPropRe   = regexp.MustCompile(`^\s*(?:(?:public|protected|private|static|readonly)\s+)+(?:\??[A-Za-z_][A-Za-z0-9_\\|]*\s+)?\$([A-Za-z_][A-Za-z0-9_]*)`)
	phpDefineRe = regexp.MustCompile(`^\s*define\s*\(\s*['"]([^'"]+)['"]`)
)

func extractPHP(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "*") {
			sc.advance(line)
			continue
		}
		if m := phpNsRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			sc.advance(line)
			continue
		}
		if m := phpUseRe.FindStringSubmatch(line); m != nil && sc.depth == 0 {
			f.Imports = append(f.Imports, m[1])
			sc.advance(line)
			continue
		}
		if m := phpTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindClass
			if m[1] == "interface" {
				kind = KindInterface
			}
			addSymbol(f, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line), Line: i + 1, Exported: true,
			})
			sc.enter(m[2])
			sc.advance(line)
			continue
		}
		switch {
		case phpFuncRe.MatchString(line):
			m := phpFuncRe.FindStringSubmatch(line)
			kind := KindFunc
			if sc.owner != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: !strings.Contains(trimmed, "private "), Receiver: sc.owner,
			})
		case phpConstRe.MatchString(line):
			m := phpConstRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindConst, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: sc.owner,
			})
		case phpDefineRe.MatchString(line):
			m := phpDefineRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindConst, Signature: trimSig(line), Line: i + 1, Exported: true,
			})
		case sc.atMemberLevel():
			if m := phpPropRe.FindStringSubmatch(line); m != nil {
				addSymbol(f, Symbol{
					Name: m[1], Kind: KindVar, Signature: trimSig(line), Line: i + 1,
					Exported: !strings.Contains(trimmed, "private "), Receiver: sc.owner,
				})
			}
		}
		sc.advance(line)
	}
}

// ---------------------------------------------------------------------------
// C / C++
// ---------------------------------------------------------------------------

var (
	cIncludeRe = regexp.MustCompile(`^\s*#\s*include\s*[<"]([^>"]+)[>"]`)
	cTypeRe    = regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?(class|struct|union|enum(?:\s+class)?)\s+(?:[A-Z_]+_API\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	cNsRe      = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][A-Za-z0-9_]*)`)
	cFuncRe    = regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?(?:(?:static|inline|extern|virtual|explicit|constexpr|consteval|friend|const)\s+)*[A-Za-z_][A-Za-z0-9_:<>,\s\*&\[\]]*?\s+[\*&]?\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	cDefineRe  = regexp.MustCompile(`^\s*#\s*define\s+([A-Za-z_][A-Za-z0-9_]*)`)
	cTypedefRe = regexp.MustCompile(`^\s*(?:typedef|using)\s+(?:.*\s)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:;|=)`)
	// A constructor/destructor has no return type, so cFuncRe cannot see it.
	cCtorRe = regexp.MustCompile(`^\s*(?:explicit\s+|virtual\s+|constexpr\s+)*(~?[A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func extractCFamily(f *File, lines []string) {
	var sc scopeTracker
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
			sc.advance(line)
			continue
		}
		if m := cIncludeRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			continue
		}
		if m := cDefineRe.FindStringSubmatch(line); m != nil {
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindConst, Signature: trimSig(line), Line: i + 1, Exported: true,
			})
			continue
		}
		if m := cNsRe.FindStringSubmatch(line); m != nil {
			if f.Package == "" {
				f.Package = m[1]
			}
			sc.advance(line)
			continue
		}
		if m := cTypeRe.FindStringSubmatch(line); m != nil {
			addSymbol(f, Symbol{
				Name: m[2], Kind: KindType, Signature: trimSig(line), Line: i + 1, Exported: true,
			})
			if strings.Contains(line, "{") {
				sc.enter(m[2])
			}
			sc.advance(line)
			continue
		}
		switch {
		case sc.atMemberLevel() && cCtorRe.MatchString(line) &&
			strings.TrimPrefix(cCtorRe.FindStringSubmatch(line)[1], "~") == sc.owner:
			m := cCtorRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindMethod, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: sc.owner,
			})
		case cFuncRe.MatchString(line) && (!strings.HasSuffix(trimmed, ";") || sc.owner != ""):
			m := cFuncRe.FindStringSubmatch(line)
			kind := KindFunc
			if sc.owner != "" {
				kind = KindMethod
			}
			addSymbol(f, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line), Line: i + 1,
				Exported: true, Receiver: sc.owner,
			})
		case cTypedefRe.MatchString(line):
			m := cTypedefRe.FindStringSubmatch(line)
			addSymbol(f, Symbol{
				Name: m[1], Kind: KindType, Signature: trimSig(line), Line: i + 1, Exported: true,
			})
		}
		sc.advance(line)
	}
}

// ---------------------------------------------------------------------------
// Shell
// ---------------------------------------------------------------------------

var (
	// Both POSIX `name() {` and the ksh/bash `function name {` form.
	shFuncRe   = regexp.MustCompile(`^\s*(?:([A-Za-z_][A-Za-z0-9_:-]*)\s*\(\s*\)|function\s+([A-Za-z_][A-Za-z0-9_:-]*)\s*(?:\(\s*\))?)\s*\{?\s*$`)
	shSourceRe = regexp.MustCompile(`^\s*(?:\.|source)\s+([^\s;]+)`)
)

func extractShell(f *File, lines []string) {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := shSourceRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, strings.Trim(m[1], `"'`))
			continue
		}
		if m := shFuncRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			addSymbol(f, Symbol{
				Name: name, Kind: KindFunc, Signature: trimSig(line), Line: i + 1,
				Exported: !strings.HasPrefix(name, "_"),
			})
		}
	}
}

// hasPrivateModifier reports whether a visibility keyword appears BEFORE the
// declaration keyword. `class Repo(private val db: Db)` is a public class with
// a private constructor parameter — a naive substring search calls it private.
func hasPrivateModifier(line string, keywords ...string) bool {
	head := line
	for _, kw := range keywords {
		if i := strings.Index(line, kw+" "); i >= 0 && i < len(head) {
			head = line[:i]
		}
	}
	return strings.Contains(head, "private") || strings.Contains(head, "internal") ||
		strings.Contains(head, "fileprivate")
}
