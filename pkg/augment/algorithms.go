package augment

// AlgorithmKnowledge returns compact cheat-sheets ported from little-coder.
// Kept short so they fit SLM budgets when keyword-scored in.
func AlgorithmKnowledge() []KnowledgeEntry {
	return []KnowledgeEntry{
		{
			Topic: "Binary Search", TokenCost: 90,
			Keywords: []string{
				"binary", "search", "sorted", "monotonic", "bisect", "lower bound",
				"upper bound", "minimize the maximum", "maximize the minimum", "log n",
			},
			Body: `Binary search on any monotonic predicate, not just arrays. Pattern: find min X
such that condition(X). Use lo+(hi-lo)//2. For rotated arrays, check which half
is sorted first. O(log n) whenever you see sorted/monotonic.`,
		},
		{
			Topic: "Dynamic Programming", TokenCost: 100,
			Keywords: []string{
				"dynamic programming", "dp", "memoize", "memoization", "knapsack",
				"subsequence", "number of ways", "edit distance", "climb stairs", "coins",
			},
			Body: `DP when overlapping subproblems + optimal substructure. Define state + recurrence.
Top-down @cache is easiest; bottom-up avoids recursion limits. Often keep only the
previous row to cut space. Signs: min cost, count ways, longest/shortest subsequence.`,
		},
		{
			Topic: "Two Pointers", TokenCost: 80,
			Keywords: []string{
				"two pointers", "two pointer", "left right", "sorted array", "pair sum",
				"palindrome", "sliding", "in-place", "remove duplicates",
			},
			Body: `Two pointers on sorted arrays / strings: opposite ends for pair-sum/palindrome,
or slow/fast for in-place filter. Sliding window is the generalized form for
subarray constraints. Prefer O(n) over nested loops when order helps.`,
		},
		{
			Topic: "BFS vs DFS", TokenCost: 90,
			Keywords: []string{
				"bfs", "dfs", "graph", "shortest path", "level order", "connected",
				"maze", "grid", "traversal", "queue", "stack", "recursion",
			},
			Body: `BFS (queue) for shortest path in unweighted graphs / level order. DFS (stack/
recursion) for connectivity, topo-ish exploration, backtracking. On grids mark
visited to avoid cycles. Prefer iterative BFS when depth can explode.`,
		},
		{
			Topic: "Recursion Backtracking", TokenCost: 90,
			Keywords: []string{
				"backtracking", "permute", "permutation", "combination", "subset",
				"n-queens", "sudoku", "search tree", "undo",
			},
			Body: `Build a partial solution, recurse, undo (pop) on failure. Prune early when
constraints break. For subsets/permutations track used indices. Always restore
state after exploring a branch.`,
		},
		{
			Topic: "Sorting Choice", TokenCost: 70,
			Keywords: []string{
				"sort", "sorted", "ordering", "comparator", "stable sort", "n log n",
				"priority", "heap",
			},
			Body: `Default to language stable sort (n log n). Need top-k repeatedly → heap.
Custom order → key/comparator. If already nearly sorted, consider that before
writing an exotic algorithm.`,
		},
		{
			Topic: "Hash vs Tree", TokenCost: 90,
			Keywords: []string{
				"lookup", "dictionary", "dict", "set", "hash", "hashtable", "map",
				"frequency", "count", "unique", "duplicate", "counter", "defaultdict",
				"collections", "membership",
			},
			Body: `Use dict/set (O(1) avg) for membership, frequency, dedup, grouping.
collections.Counter for counts; defaultdict(list) for grouping. Ordered keys /
range queries → sorted list + bisect. Never scan a list repeatedly for "exists?"
or "count". Pair-sum → set of complements (O(n)), not nested loops.`,
		},
		{
			Topic: "State-Space BFS", TokenCost: 120,
			Keywords: []string{
				"bucket", "pouring", "state space", "minimum moves", "shortest sequence",
				"reach goal", "transitions", "visited states", "water", "pour", "fill", "empty",
				"puzzle", "sliding",
			},
			Body: `MINIMUM moves to a goal → BFS over states. State = tuple of all relevant values.
Enumerate legal transitions; visited set on the state tuple; first goal pop is
min distance. Do not DFS for shortest unweighted paths.`,
		},
		{
			Topic: "IO Wrapper Counters", TokenCost: 120,
			Keywords: []string{
				"io wrapper", "wrap file", "read counter", "write counter", "nreads",
				"nwrites", "context manager", "__enter__", "__exit__", "passthrough",
				"delegate", "paasio", "file-like",
			},
			Body: `Wrap file-like: store self._wrapped. read/write delegate then increment counters
by RETURNED bytes (short reads count what came back). __enter__→self;
__exit__→forward to wrapped. Expose nreads/nwrites (+ byte totals). Threaded
tests → Lock around counter updates.`,
		},
		{
			Topic: "Ordered-Rule String Transform", TokenCost: 120,
			Keywords: []string{
				"pig latin", "string rule", "transform word", "vowel", "consonant",
				"cluster", "qu", "ordered rules", "first match", "prefix", "suffix",
				"translate word", "atbash", "rot13",
			},
			Body: `Encode rules as ordered (predicate, transform) pairs; apply FIRST match and stop.
Specific before general. Pig latin: vowel/xr/yt→ay; consonant(s)+qu as a unit;
y is consonant at start, vowel mid-word. Test each rule alone before combining.`,
		},
		{
			Topic: "Tree Re-Rooting", TokenCost: 120,
			Keywords: []string{
				"re-root", "reroot", "pov", "point of view", "tree rotation", "change root",
				"from_pov", "reparent", "path between nodes", "undirected tree",
			},
			Body: `Undirected tree → adjacency; DFS/BFS from new root; parent=came-from,
children=neighbors−parent. Path a→b: re-root at a, walk parents from b. Missing
node → None. Build a FRESH tree — never mutate the original across from_pov calls.`,
		},
		{
			Topic: "Tree Zipper", TokenCost: 130,
			Keywords: []string{
				"zipper", "tree navigation", "breadcrumb", "focus", "up", "down",
				"left", "right", "functional tree", "immutable tree", "cursor",
			},
			Body: `Zipper = (focus, trail). down pushes crumb (parent value + other siblings);
up pops and rebuilds parent. set_value replaces focus. to_tree = repeated up.
Equality = fully reconstructed trees, not raw (focus, trail) pairs.`,
		},
	}
}
