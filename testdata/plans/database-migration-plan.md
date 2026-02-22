# Database Migration: PostgreSQL to CockroachDB

## Motivation

- Need horizontal scaling for write-heavy workloads
- Current single-node PostgreSQL is approaching IOPS limits
- CockroachDB provides PostgreSQL wire compatibility

## Pre-Migration Checklist

- [ ] Audit all PostgreSQL-specific features in use (CTEs, window functions, etc.)
- [ ] Identify incompatible queries (e.g., advisory locks)
- [ ] Set up CockroachDB staging cluster (3 nodes)
- [ ] Run pgbench on both systems for baseline comparison

## Migration Steps

1. **Schema conversion** - Export schema, adjust data types
   - Replace `SERIAL` with `UUID` primary keys
   - Remove advisory lock usage
   - Convert `LISTEN/NOTIFY` to application-level polling

2. **Data migration** - Use `IMPORT INTO` for bulk load
   - Estimated data: ~50GB across 12 tables
   - Expected migration time: 2-3 hours

3. **Application changes**
   - Update connection strings
   - Replace `pg` driver with `pgx` (already compatible)
   - Adjust transaction isolation levels

4. **Validation**
   - Run full integration test suite against CockroachDB
   - Compare query plans for top 20 slowest queries
   - Verify row counts match across all tables

## Rollback Plan

Keep PostgreSQL running in read-only mode for 72 hours after cutover.
If critical issues found, revert DNS to PostgreSQL and replay missed writes from CockroachDB WAL.
