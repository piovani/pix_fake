// Package ledger holds accounts and balances: a simple balance per account with reservation. A payment in flight
// reserves the amount (available to reserved); settling confirms it (the money leaves), and a rejection or timeout
// releases it (the money comes back). Every movement is also recorded in an append-only log, which the statement will read.
// Money is integer centavos.
//
// Module boundary: other modules use the ledger only through its exported API, never its tables, so it can become a
// service of its own later.
package ledger
