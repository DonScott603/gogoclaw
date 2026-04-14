# Architecture Notes

- REST requests are synchronous and include auth, rate limiting, audit logging, session lookup, engine execution, and persistence.
- Conversations are stored in SQLite with WAL mode enabled.
- Message processing may trigger provider calls, tool loops, memory lookup, and background summarization.
- File uploads are written into the inbox directory before the engine is invoked.
