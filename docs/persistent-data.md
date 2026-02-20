# Persistent Data

OpenBerth deployments get two ways to persist data across rebuilds.

## 1. `/data` Directory (Backend Apps)

Every dynamic container (Node.js, Python, Go) has a persistent `/data` directory. Use the `DATA_DIR` environment variable to reference it.

```js
// Node.js
const path = require('path');
const dbPath = path.join(process.env.DATA_DIR, 'app.db');
```

```python
# Python
import os
db_path = os.path.join(os.environ['DATA_DIR'], 'app.db')
```

This directory survives code updates and rebuilds. It is deleted when the deployment is destroyed.

## 2. `/_data/*` REST API (Any Deployment)

A built-in document store accessible via `fetch()` from any deployed page. No backend code needed — works with static HTML.

### Base URL

All requests go to `/_data/` on the deployment's own domain:

```
https://my-app-abc1.example.com/_data/votes
```

No authentication is required — requests are scoped to the deployment they originate from.

### Endpoints

#### List collections
```
GET /_data
-> {"collections": [{"name": "votes", "count": 42}, ...]}
```

#### Create a document
```
POST /_data/{collection}
Content-Type: application/json

{"option": "pizza", "count": 1}

-> 201 {"id": "cs1abc...", "collection": "votes", "data": {...}, "createdAt": "...", "updatedAt": "..."}
```

#### List documents
```
GET /_data/{collection}
GET /_data/{collection}?limit=10&offset=20

-> {"documents": [...], "total": 42, "limit": 100, "offset": 0}
```

Documents are returned newest first.

#### Get a document
```
GET /_data/{collection}/{id}

-> {"id": "...", "collection": "votes", "data": {...}, "createdAt": "...", "updatedAt": "..."}
```

#### Update a document
```
PUT /_data/{collection}/{id}
Content-Type: application/json

{"option": "pizza", "count": 2}

-> 200 {"id": "...", "collection": "votes", "data": {...}, "createdAt": "...", "updatedAt": "..."}
```

#### Delete a document
```
DELETE /_data/{collection}/{id}

-> 200 {"status": "deleted"}
```

#### Delete an entire collection
```
DELETE /_data/{collection}

-> 200 {"status": "deleted", "count": 42}
```

### Limits

| Limit | Value |
|-------|-------|
| Max document size | 100 KB |
| Max documents per collection | 10,000 |
| Max collections per deployment | 100 |
| Max total storage | 50 MB |

### CORS

All `/_data/*` endpoints return `Access-Control-Allow-Origin: *` so they work from any origin, including the deployment's own subdomain.

### Examples

#### HTML voting form (no backend needed)

```html
<!DOCTYPE html>
<html>
<body>
  <h1>Vote</h1>
  <button onclick="vote('pizza')">Pizza</button>
  <button onclick="vote('tacos')">Tacos</button>
  <div id="results"></div>

  <script>
    async function vote(option) {
      await fetch('/_data/votes', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({option, timestamp: new Date().toISOString()})
      });
      loadResults();
    }

    async function loadResults() {
      const {documents} = await fetch('/_data/votes').then(r => r.json());
      const counts = {};
      documents.forEach(d => {
        counts[d.data.option] = (counts[d.data.option] || 0) + 1;
      });
      document.getElementById('results').innerText = JSON.stringify(counts);
    }

    loadResults();
  </script>
</body>
</html>
```

#### Node.js with `/data` directory

```js
const express = require('express');
const Database = require('better-sqlite3');

const app = express();
const db = new Database(process.env.DATA_DIR + '/app.db');

db.exec(`CREATE TABLE IF NOT EXISTS visits (id INTEGER PRIMARY KEY, ts TEXT)`);

app.get('/', (req, res) => {
  db.prepare('INSERT INTO visits (ts) VALUES (?)').run(new Date().toISOString());
  const count = db.prepare('SELECT COUNT(*) as n FROM visits').get();
  res.send(`Visits: ${count.n}`);
});

app.listen(process.env.PORT);
```

#### Python FastAPI with `/data` directory

```python
import os, sqlite3
from fastapi import FastAPI

app = FastAPI()
db_path = os.path.join(os.environ["DATA_DIR"], "app.db")

def get_db():
    conn = sqlite3.connect(db_path)
    conn.execute("CREATE TABLE IF NOT EXISTS visits (id INTEGER PRIMARY KEY, ts TEXT)")
    return conn

@app.get("/")
def index():
    db = get_db()
    db.execute("INSERT INTO visits (ts) VALUES (datetime('now'))")
    db.commit()
    count = db.execute("SELECT COUNT(*) FROM visits").fetchone()[0]
    return {"visits": count}
```
