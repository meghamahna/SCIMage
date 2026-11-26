---
name: dsa-conscious-coding
description: Data-structures-and-algorithms-conscious coding. Pick the right structure for the access pattern, state Big-O on non-trivial paths, and avoid accidental quadratic work, N+1 queries and wasted allocations. Use when writing or reviewing loops, lookups, searches, sorting, caching, batching, recursion, queries, or any hot path, and when asked about performance or complexity.
---

# DSA-conscious coding

Write the simplest code that has the **right complexity** for realistic input sizes. Correctness and clarity come first, then complexity.

## 1. Know the input size and access pattern
Before writing code, answer these questions:
- **n:** how large is n now and in a year (10? 10k? 10M)?
- **Hot or cold:** is the path hot (per request, per row, in a loop) or cold (startup, admin script)?
- **Operations:** lookup by key, membership, ordered iteration, min/max, range queries, insert/delete in the middle, FIFO/LIFO?

## 2. Choose the structure by operation

| Need | Use | Typical cost |
|---|---|---|
| Lookup or membership by key | hash map / set | O(1) avg |
| Ordered keys, range queries | sorted array + binary search, balanced tree | O(log n) |
| Repeated min/max or top-k | heap / priority queue | O(log n) push/pop; top-k O(n log k) |
| FIFO queue / sliding window | deque / ring buffer | O(1) both ends |
| Counting / frequency | counter / hash map | O(n) total |
| Prefix lookups / autocomplete | trie or sorted array + bisect | O(len) / O(log n) |
| Connectivity / grouping | union-find | ~O(α(n)) |
| Graph shortest path | BFS (unweighted), Dijkstra (weighted ≥ 0) | O(V+E), O(E log V) |
| Dedupe while keeping order | ordered set / dict keys | O(n) |
| LRU / bounded cache | ordered map + capacity | O(1) |

More detail, including language equivalents, is in [references/structures.md](references/structures.md).

## 3. State complexity on non-trivial paths
For any function with loops over unbounded input, recursion, or queries, add a short comment such as:
`# O(n log n) time, O(n) space; n = number of orders`

If it can't be stated simply, the design is probably wrong.

## 4. Anti-patterns to catch (and their fixes)
- **Nested lookups.** Searching list B for each item of A is O(n·m). Build a set or map of B once first.
- **`x in list` inside a loop.** Use a set.
- **N+1 queries.** A query per row in a loop. Batch it (`WHERE id IN (...)`), use a join, or eager-load.
- **Repeated work in loops:** a regex compile, a `len()` of an expensive generator, JSON parsing, or a sort inside the loop. Hoist it out.
- **String concatenation in loops.** Use join, a builder, or a buffer.
- **Sorting to get the min/max/top-k.** Use a single pass, a heap, or a selection algorithm.
- **Unbounded recursion** on user-controlled depth. Use iteration with an explicit stack, or a depth limit.
- **Copying in loops.** Slices, spreads, or `list + [x]` make the loop O(n²). Mutate a local accumulator instead.
- **Loading everything into memory** when streaming or pagination would do.
- **Linear scans for "latest"/"by id"** in data you control. Index it.

See [references/anti-patterns.md](references/anti-patterns.md) for before/after examples.

## 5. Worst-case and adversarial inputs
Code is judged by its worst realistic input, not its average one. Guard explicitly against:
- **Integer overflow** in sums, products, sizes and index arithmetic (use wider types, checked math, or `lo + (hi - lo) // 2` for midpoints).
- **Null, empty and out-of-bounds** values: missing keys, empty collections, negative or past-the-end indexes.
- **Hash flooding:** user-controlled keys that collide on purpose. Prefer the runtime's randomized hashing; cap request sizes.
- **Regex backtracking (ReDoS):** nested quantifiers such as `(a+)+` on user input. Use linear-time patterns or bound the input length.
- **Unbounded input sizes:** cap lengths, counts and depths at the boundary, and fail with a clear error.

## 6. Space and time trade-offs
- **Precompute vs. recompute.** Cache only when reads clearly outnumber writes, and always bound the cache.
- **Memory:** generators and iterators over materialized lists for large n. Avoid holding duplicates of large collections.
- **Memory layout:** contiguous arrays and vectors are faster than node-based structures (linked lists, trees of small objects) for scans, because they use the CPU cache well. Choose a linked structure only when its operations are actually needed.
- **Database:** add indexes for frequent `WHERE`/`JOIN`/`ORDER BY` columns, and remember that every index costs on write.

## 7. When NOT to optimize
- n is small and bounded (under ~1k) and the path is cold.
- The "faster" version is much harder to read and nobody has measured a problem.
- I/O dominates, so fix the round-trips before the CPU work.

In these cases, prefer clear code and leave a complexity comment if it isn't obvious.

## 8. Review checklist
- [ ] Input size assumptions stated; hot paths identified.
- [ ] No O(n²) or worse over unbounded input without a written justification.
- [ ] No queries or network calls inside loops.
- [ ] Right structure for each lookup (set/map rather than a list scan).
- [ ] Recursion is bounded; memory is bounded (streaming or pagination where needed).
- [ ] Worst-case inputs handled: overflow, null/empty, bounds, hash flooding, regex backtracking, size caps.
- [ ] Complexity comment on non-trivial functions.
- [ ] Measured (benchmark or profile) before any micro-optimization.
