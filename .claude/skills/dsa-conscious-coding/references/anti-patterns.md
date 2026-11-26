# Anti-patterns: before and after

## Nested lookup: O(n·m) → O(n+m)
```python
# before
active = [u for u in users if u.id in [s.user_id for s in sessions]]
# after: O(n + m) time, O(m) space
session_ids = {s.user_id for s in sessions}
active = [u for u in users if u.id in session_ids]
```

## N+1 queries → one batched query
```python
# before: 1 + N round-trips
for order in orders:
    order.customer = db.get_customer(order.customer_id)
# after: 2 round-trips
customers = {c.id: c for c in db.get_customers({o.customer_id for o in orders})}
for order in orders:
    order.customer = customers[order.customer_id]
```
ORMs: use `select_related`/`prefetch_related` (Django), `joinedload`/`selectinload` (SQLAlchemy), `include` (Prisma), or `Preload` (GORM).

## String concatenation in a loop: O(n²) → O(n)
```js
// before
let out = ""; for (const row of rows) out += render(row);
// after
const out = rows.map(render).join("");
```

## Sort to find top-k: O(n log n) → O(n log k)
```python
# before
top = sorted(items, key=score, reverse=True)[:10]
# after
import heapq
top = heapq.nlargest(10, items, key=score)
```

## Repeated regex compile / parse inside loop
```python
# before
for line in lines:
    if re.match(r"^\d{4}-\d{2}-\d{2}", line): ...
# after
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}")
for line in lines:
    if DATE.match(line): ...
```

## Array `shift()` / `list.pop(0)` as a queue: O(n) per op → O(1)
```python
from collections import deque
q = deque(start); q.popleft()
```

## Unbounded recursion → explicit stack
```python
def walk(root):
    stack = [root]
    while stack:
        node = stack.pop()
        yield node
        stack.extend(node.children)
```

## Copying inside reduce/loops: O(n²) → O(n)
```js
// before
const byId = items.reduce((acc, it) => ({ ...acc, [it.id]: it }), {});
// after
const byId = new Map(items.map(it => [it.id, it]));
```

## Load-all → stream
```python
# before
rows = cursor.fetchall()
# after: O(batch) memory
while batch := cursor.fetchmany(1000):
    process(batch)
```
