# Performance Optimization Plan

## Profiling Results

Identified bottlenecks from production traces (p99 latency > 500ms):

1. **Dashboard API** - 12 sequential database queries (850ms p99)
2. **Search endpoint** - Full table scan on `documents.content` (1200ms p99)
3. **File upload** - Synchronous thumbnail generation (2000ms p99)

## Optimizations

### 1. Dashboard Query Batching

Replace sequential queries with a single CTE:

```sql
WITH stats AS (
  SELECT
    count(*) FILTER (WHERE created_at > now() - interval '7 days') as weekly_new,
    count(*) FILTER (WHERE status = 'active') as active_count,
    count(*) as total
  FROM projects
  WHERE user_id = $1
),
recent AS (
  SELECT id, name, updated_at
  FROM projects
  WHERE user_id = $1
  ORDER BY updated_at DESC
  LIMIT 5
)
SELECT * FROM stats, recent;
```

Expected improvement: 850ms -> 50ms

### 2. Full-Text Search Index

Add GIN index with tsvector:

```sql
ALTER TABLE documents ADD COLUMN search_vector tsvector;
CREATE INDEX idx_documents_search ON documents USING GIN(search_vector);

CREATE TRIGGER documents_search_update
  BEFORE INSERT OR UPDATE ON documents
  FOR EACH ROW EXECUTE FUNCTION
  tsvector_update_trigger(search_vector, 'pg_catalog.english', title, content);
```

Expected improvement: 1200ms -> 15ms

### 3. Async Thumbnail Generation

- Move to background job queue (pgboss or similar)
- Return upload response immediately with placeholder
- Client polls or uses WebSocket for completion notification

Expected improvement: 2000ms -> 100ms (upload response)

## Metrics to Track

- p50, p95, p99 latency for each endpoint
- Database connection pool utilization
- Background job queue depth
