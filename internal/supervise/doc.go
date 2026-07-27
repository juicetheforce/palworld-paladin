// Package supervise — systemd unit ownership and control, genuine-readiness probes (REST up, not just process-exists), and crash/RAM-threshold restart supervision. Supervision MUST be suspendable during maintenance cycles (invariant I2).
//
// Design reference: docs/DESIGN.md §6.1, §6.9
package supervise
