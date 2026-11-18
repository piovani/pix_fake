// Package ledger holds accounts and balances: a simple balance per account with reservation (a payment in flight
// reserves the amount; settling confirms it, a rejection or timeout releases it). Money is integer centavos.
//
// Module boundary: other modules use ledger only through its exported API, never its tables, so it can become a
// service of its own later.
package ledger
