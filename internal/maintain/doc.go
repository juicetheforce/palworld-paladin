// Package maintain — the shared maintenance state machine powering both settings commits and world restores: journal, single-flight lock, per-step rollback per the matrix, and live status events.
//
// Design reference: docs/DESIGN.md §6.3, §6.8, §6.9
package maintain
