# Data structures across common languages

| Concept | Python | JS / TS | Java | Go | Rust |
|---|---|---|---|---|---|
| Hash map | `dict` | `Map` | `HashMap` | `map[K]V` | `HashMap` |
| Hash set | `set` | `Set` | `HashSet` | `map[K]struct{}` | `HashSet` |
| Ordered map | `sortedcontainers.SortedDict` / sorted list + `bisect` | sorted array + binary search | `TreeMap` | sorted slice + `sort.Search` | `BTreeMap` |
| Deque | `collections.deque` | array as ring buffer (avoid `shift()` in hot loops: it is O(n)) | `ArrayDeque` | slice ring buffer / `container/list` | `VecDeque` |
| Heap | `heapq` | small binary-heap class (no builtin) | `PriorityQueue` | `container/heap` | `BinaryHeap` |
| Counter | `collections.Counter` | `Map<K, number>` | `HashMap<K,Integer>` / `merge` | `map[K]int` | `HashMap<K,usize>` / `entry` |
| LRU cache | `functools.lru_cache(maxsize=…)` / `OrderedDict` | `Map` insertion order + delete/re-set | `LinkedHashMap(accessOrder=true)` | `container/list` + map | `lru` crate |
| String builder | `"".join(parts)` / `io.StringIO` | `parts.join("")` | `StringBuilder` | `strings.Builder` | `String::with_capacity` + `push_str` |

## Complexity cheat sheet

| Operation | Array/list | Linked list | Hash map | Balanced tree | Heap |
|---|---|---|---|---|---|
| Index access | O(1) | O(n) | n/a | O(log n) | n/a |
| Search by value | O(n) | O(n) | O(1) avg | O(log n) | O(n) |
| Insert at end | O(1) amortized | O(1) | O(1) avg | O(log n) | O(log n) |
| Insert in middle | O(n) | O(1) given node | n/a | O(log n) | n/a |
| Delete | O(n) | O(1) given node | O(1) avg | O(log n) | O(log n) (top) |
| Min / max | O(n) | O(n) | O(n) | O(log n) | O(1) peek |

## Algorithm picks
- **Sorting:** use the language's builtin sort (O(n log n), stable in Python/Java objects/JS). Never hand-roll one.
- **Top-k of n:** use a heap of size k (O(n log k)) or `nlargest`. Fully sorting is O(n log n).
- **Dedupe:** use a set (O(n)). Sorting followed by unique is O(n log n).
- **Two-sum / pair finding:** use a hash map in one pass (O(n)), not nested loops.
- **Interval overlap:** sort by start, then sweep (O(n log n)).
- **Sliding-window aggregates:** use a deque or a running sum (O(n)).
- **Caching expensive pure functions:** memoize with a bounded size.
- **Paginating large tables:** use keyset pagination (`WHERE id > last ORDER BY id LIMIT k`). OFFSET pagination is O(offset).
