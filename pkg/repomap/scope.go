package repomap

import "strings"

// Brace-scoped member extraction.
//
// The original extractors were purely line-local, which is fine for Go and
// Python (top-level declarations start in column 0) and wrong for every
// brace-scoped OO language: a TypeScript, C#, Kotlin, Swift or PHP file's real
// content is its METHODS, and a line-local matcher either misses them entirely
// or cannot say which type they belong to. Both failures hurt: a repo map
// listing `class OrderService` and nothing else tells a small model that the
// class exists but not what it can call.
//
// scopeTracker is the minimum machinery that fixes it — a brace counter that
// remembers which type body the cursor is currently inside, so a member line
// can be recorded as a method WITH a receiver instead of a free function.
type scopeTracker struct {
	depth int
	// owner is the type whose body the cursor is directly inside, and
	// ownerDepth is the brace depth of that body. bodyOpened distinguishes
	// "declared, brace is on the next line" (extremely common in C#, PHP and
	// Java house styles) from "declaration ended without a body at all"
	// (`public record Point(int X, int Y);`). Without it the first case drops
	// the owner immediately and every member loses its receiver.
	owner      string
	ownerDepth int
	bodyOpened bool
	// pending counts the lines a declaration may wait for its opening brace.
	// Two is enough for the `class Foo\n{` house style; a Kotlin
	// `data class User(val id: Long)` never opens one and is dropped instead of
	// silently adopting the next declaration as its member.
	pending int
	// inBlockComment survives across lines so a commented-out class body never
	// unbalances the counter.
	inBlockComment bool
}

// enter records that this line declares a type named name. Call it BEFORE
// advance for the same line.
func (s *scopeTracker) enter(name string) {
	s.owner = name
	s.ownerDepth = s.depth + 1
	s.bodyOpened = false
	s.pending = 2
}

// memberOwner returns the enclosing type ONLY when the cursor is genuinely
// inside its body, so a bodyless declaration can never lend its name as the
// receiver of the declaration that follows it.
func (s *scopeTracker) memberOwner() string {
	if s.atMemberLevel() {
		return s.owner
	}
	return ""
}

// atMemberLevel reports whether the cursor sits directly inside a type body,
// i.e. this line is a member declaration rather than a nested block's contents.
func (s *scopeTracker) atMemberLevel() bool {
	return s.owner != "" && s.bodyOpened && s.depth == s.ownerDepth
}

// advance updates the brace depth for one line and drops the owner once its
// body closes. Call it AFTER matching the line.
func (s *scopeTracker) advance(line string) {
	code := stripCodeNoise(line, &s.inBlockComment)
	// peak matters as much as the final depth: `trait Loggable { fn x() {} }`
	// opens and closes the body on ONE line, ending at the same depth it
	// started, and treating that as "body never opened" leaves the type adopting
	// the next declaration as its member.
	peak := s.depth
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '{':
			s.depth++
			if s.depth > peak {
				peak = s.depth
			}
		case '}':
			if s.depth > 0 {
				s.depth--
			}
		}
	}
	if s.owner == "" {
		return
	}
	if peak >= s.ownerDepth {
		s.bodyOpened = true
	}
	if s.depth >= s.ownerDepth {
		return
	}
	if s.bodyOpened {
		// The body closed.
		s.reset()
		return
	}
	// A declaration that ended with `;`, or that never opened a body within the
	// grace window, has no members at all.
	if strings.HasSuffix(strings.TrimSpace(code), ";") {
		s.reset()
		return
	}
	s.pending--
	if s.pending <= 0 {
		s.reset()
	}
}

func (s *scopeTracker) reset() {
	s.owner = ""
	s.ownerDepth = 0
	s.bodyOpened = false
	s.pending = 0
}

// stripCodeNoise removes string/char literals, line comments and block comments
// so a brace inside "}" or /* } */ never moves the depth counter. It is
// deliberately approximate — template literals with nested expressions and raw
// strings are rare enough in declaration-heavy regions that a one-line slip
// costs a receiver name, not correctness elsewhere.
func stripCodeNoise(line string, inBlock *bool) string {
	var b strings.Builder
	b.Grow(len(line))
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if *inBlock {
			if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
				*inBlock = false
				i++
			}
			continue
		}
		if quote != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch {
		case ch == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String()
		case ch == '#' && i == 0:
			// Ruby / shell / PHP line comment at the start of a line.
			return b.String()
		case ch == '/' && i+1 < len(line) && line[i+1] == '*':
			*inBlock = true
			i++
		case ch == '"' || ch == '\'' || ch == '`':
			quote = ch
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// controlKeywords are statement heads that look like a declaration to a regex
// that just wants "identifier followed by a paren" — `if (…) {`, `catch (e) {`.
//
// The list is deliberately SHORT. Words that are a keyword in one language and
// an ordinary member name in another (`new` is Rust's conventional constructor
// and C#'s allocation keyword; `match`, `require`, `print`, `self`, `when` are
// all real method names somewhere) must NOT be here: dropping them silently
// deletes the most important symbol in the file. Only words that can never name
// a declaration in any supported language belong.
var controlKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "else": true, "elseif": true, "elif": true, "try": true,
	"foreach": true, "lock": true, "throw": true, "case": true, "unless": true,
	"until": true, "finally": true, "synchronized": true,
}

func isControlWord(name string) bool { return controlKeywords[name] }

// addSymbol appends a symbol, skipping obvious noise and bounding the total so
// a generated or minified file can never blow up the repo map.
func addSymbol(f *File, s Symbol) {
	if f == nil || s.Name == "" || len(f.Symbols) >= maxSymbolsPerFile {
		return
	}
	if isControlWord(s.Name) {
		return
	}
	f.Symbols = append(f.Symbols, s)
}

// maxSymbolsPerFile bounds one file's contribution to the ranked map.
const maxSymbolsPerFile = 400
