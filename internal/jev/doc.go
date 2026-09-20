// Package jev is a client for the TypeSafe System One API.
//
// A Client is safe for concurrent use by multiple goroutines. Any Clock, random source or
// RetryStatus predicate passed to New must be safe for concurrent use too.
//
// Timeouts are per attempt. A call can outlast one by the retry count, so bound the whole call
// with the context you pass in, or with WithTotalTimeout.
//
// Ported from the MIT licensed @typesafe-ai/sdk JavaScript client. See NOTICE in the repo root.
package jev
