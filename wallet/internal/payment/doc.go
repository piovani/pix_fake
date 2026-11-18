// Package payment holds the payment flows and their status (pending, settled, rejected): pay a key of another
// institution, book transfer between wallet users, receive, return, and the statement.
//
// Module boundary: it asks ledger to reserve and settle money, and pix to talk to Bacen. It never touches their tables.
package payment
