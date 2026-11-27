// Package med is the wallet's side of the MED (Mecanismo Especial de Devolução). As the participant of the payee: the
// participant of a payer reports, in the DICT, a payment the wallet received as a fraud, and asks for the money back.
//
//	infraction report (REFUND_REQUEST) -> the wallet acknowledges it and blocks the money (a cautionary block)
//	analysis (cmd/med)                 -> AGREED keeps the block for the refund; DISAGREED gives it back
//	report cancelled by the reporter   -> the block is given back
//	refund asked of the wallet          -> a return (pacs.004 FR01, or BE08 for an operational flaw) from the block,
//	                                      and from what else is available; settled, the refund is closed with its RtrId
//
// As the participant of the payer (contest.go), it reports a payment a customer contests, asks for the refund when the
// participant that received it agrees, and records what came back; the return itself is credited by the payment module.
//
// The DICT does not call the wallet: the worker reads its lists (SyncReports, SyncRefunds) and moves the refunds on
// (ProcessRefunds).
//
// Module boundary: it uses the ledger, payment and pix modules through their exported API, and only its own tables.
package med
